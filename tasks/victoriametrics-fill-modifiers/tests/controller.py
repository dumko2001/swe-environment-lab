#!/usr/bin/env python3
"""Private HTTP controller. Expected results are computed without candidate code."""
import json
import math
import os
from pathlib import Path
import random
import shutil
import signal
import socket
import subprocess
import sys
import tempfile
import time
import urllib.error
import urllib.parse
import urllib.request

BINARY = sys.argv[1]
OUTPUT = Path(sys.argv[2])
OUTPUT.mkdir(parents=True, exist_ok=True)
LOCAL = os.environ.get('VM_AUTHOR_LOCAL') == '1'
checks = []

def key(labels):
    return tuple(sorted(labels.items()))

def request(path, data=None):
    req = urllib.request.Request(base + path, data=data)
    if data is not None:
        req.add_header('Content-Type', 'application/json')
    with urllib.request.urlopen(req, timeout=12) as response:
        return response.read()

def query(expr, instant=False):
    args = {'query': expr, 'nocache': '1'}
    args.update({'time': str(start + 60)} if instant else {'start': str(start), 'end': str(start + 120), 'step': '20'})
    raw = json.loads(request('/api/v1/' + ('query' if instant else 'query_range') + '?' + urllib.parse.urlencode(args)))
    if raw.get('status') != 'success':
        raise AssertionError(raw)
    result = {}
    for series in raw['data']['result']:
        k = key(series['metric'])
        if k in result:
            raise AssertionError('duplicate result labels')
        result[k] = [(float(t), float(v)) for t, v in ([series['value']] if instant else series['values'])]
    return result

def expected(rows, instant=False):
    ts = [start + 60] if instant else [start + i * 20 for i in range(7)]
    result = {}
    for labels, values in rows:
        values = values if isinstance(values, list) else [values] * 7
        values = [values[3]] if instant else values
        samples = [(t, float(v)) for t, v in zip(ts, values) if v is not None and not math.isnan(v)]
        if samples:
            result[key(labels)] = samples
    return result

def check(name, expr, rows, instant=False):
    try:
        observed = query(expr, instant)
        wanted = expected(rows, instant)
        assert observed.keys() == wanted.keys(), f'label sets: got {observed}, want {wanted}'
        for k in wanted:
            assert len(observed[k]) == len(wanted[k]), f'samples {k}: got {observed[k]}, want {wanted[k]}'
            for (at, av), (et, ev) in zip(observed[k], wanted[k]):
                assert at == et and (av == ev or math.isclose(av, ev, rel_tol=1e-9, abs_tol=1e-9)), f'{k}: got {(at,av)}, want {(et,ev)}'
        checks.append({'case': name, 'passed': True})
    except Exception as exc:
        checks.append({'case': name, 'passed': False, 'detail': str(exc)[:3000]})

def check_error(name, expr):
    """Duplicate matching must stay a semantic error, not silently pick a row."""
    try:
        query(expr)
        raise AssertionError('ambiguous matching was accepted')
    except urllib.error.HTTPError as exc:
        try:
            body = json.load(exc)
            assert exc.code in (400, 422) and body.get('status') == 'error', 'not a query-semantic error'
            checks.append({'case': name, 'passed': True})
        except Exception as error:
            checks.append({'case': name, 'passed': False, 'detail': str(error)[:3000]})
    except Exception as exc:
        checks.append({'case': name, 'passed': False, 'detail': str(exc)[:3000]})


def pair_values(left, right, fill_left, fill_right, operator='+'):
    """Independent per-pair missingness oracle; None means no observation."""
    values = []
    for a, b in zip(left, right):
        if a is None and b is None:
            values.append(None)
            continue
        a = fill_left if a is None else a
        b = fill_right if b is None else b
        if a is None or b is None:
            values.append(None)
        else:
            values.append(a + b if operator == '+' else a - b)
    return values


def vec(value, **labels):
    args = ', '.join(json.dumps(s) for pair in labels.items() for s in pair)
    return f'label_set(({value}), {args})'

def union(*expressions):
    return '(' + ' or '.join(expressions) + ')'

def run_seed(seed):
    rng = random.Random(seed)
    suffix = ''.join(rng.choices('abcdefghijklmnopqrstuvwxyz', k=10))
    names = ('common_' + suffix, 'left_' + suffix, 'right_' + suffix)
    c, l, r = names
    a, b, x, y = [rng.randint(3, 23) for _ in range(4)]
    fl, fr = -rng.randint(2, 7), rng.randint(24, 35)
    lhs, rhs = 'vm_left_' + suffix, 'vm_right_' + suffix
    records = []
    for metric, label, value in ((lhs, c, a), (lhs, l, x), (rhs, c, b), (rhs, r, y)):
        records.append({'metric': {'__name__': metric, 'id': label}, 'values': [value] * 10, 'timestamps': [(start - 40 + i * 20) * 1000 for i in range(10)]})
    request('/api/v1/import', ('\n'.join(json.dumps(row) for row in records) + '\n').encode())
    request('/internal/force_flush')
    rows = lambda cv, lv=None, rv=None: [({'id': k}, v) for k, v in ((c, cv), (l, lv), (r, rv)) if v is not None]
    prefix = f'seed-{seed}/'
    # Imported data is essential: filter pushdown can lose unmatched label groups
    # before arithmetic sees them, even if synthetic-vector tests pass.
    check(prefix+'ingestion-control', lhs, [({'__name__': lhs, 'id': c}, a), ({'__name__': lhs, 'id': l}, x)])
    check(prefix+'ordinary-intersection', f'{lhs} + {rhs}', rows(a+b))
    check(prefix+'symmetric-fill', f'{lhs} + fill(0) {rhs}', rows(a+b, x, y))
    check(prefix+'left-fill-order', f'{lhs} - fill_left({fl}) {rhs}', rows(a-b, None, fl-y))
    check(prefix+'right-fill-only', f'{lhs} - fill_right({fr}) {rhs}', rows(a-b, x-fr))
    check(prefix+'different-fill-values', f'{lhs} - fill_left({fl}) fill_right({fr}) {rhs}', rows(a-b, x-fr, fl-y))
    check(prefix+'instant-consistency', f'{lhs} + fill(0) {rhs}', rows(a+b, x, y), True)
    check(prefix+'nested-aggregation', f'sum(({lhs} + fill_left({fl}) fill_right({fr}) {rhs}) * 2)', [({}, 2*(a+b+x+fr+fl+y))])
    check(prefix+'empty-left', f'vm_absent_{suffix} + fill_left({fl}) {rhs}', [({'id': c}, fl+b), ({'id': r}, fl+y)])
    check(prefix+'empty-right', f'{lhs} / fill_right(2) vm_absent_{suffix}', [({'id': c}, a/2), ({'id': l}, x/2)])
    check(prefix+'both-empty', f'vm_absent_{suffix} + fill(3) vm_missing_{suffix}', [])
    check(prefix+'wrong-side-does-not-create', f'vm_absent_{suffix} + fill_right(2) {rhs}', [])
    check(prefix+'scalar-control', f'{lhs} * fill(7) 2', [({'id': c}, a*2), ({'id': l}, x*2)])
    check(prefix+'logical-control', f'{lhs} and {rhs}', [({'__name__': lhs, 'id': c}, a)])
    check(prefix+'default-control', f'({lhs} + {rhs}) default 9', rows(a+b))
    # Label handling and group cardinality use computed vectors so these probes
    # remain independent of ingestion and lookback behavior.
    left = union(vec(a, id=c, zone='west'), vec(x, id=l, zone='west'))
    right = union(vec(b, id=c, zone='east'), vec(y, id=r, zone='east'))
    check(prefix+'on-label-projection', f'{left} + on(id) fill(0) {right}', rows(a+b, x, y))
    check(prefix+'ignoring-label-projection', f'{left} / ignoring(zone) fill(2) {right}', rows(a/b, x/2, 2/y))
    many = union(vec(a, id=c, worker='one'), vec(x, id=c, worker='two'), vec(y, id=l, worker='one'))
    one = vec(b, id=c)
    check(prefix+'group-left', f'{many} + on(id) group_left() fill_right({fr}) {one}', [({'id':c,'worker':'one'},a+b),({'id':c,'worker':'two'},x+b),({'id':l,'worker':'one'},y+fr)])
    check(prefix+'group-right', f'{one} - on(id) group_right() fill_left({fl}) {many}', [({'id':c,'worker':'one'},b-a),({'id':c,'worker':'two'},b-x),({'id':l,'worker':'one'},fl-y)])
    # A gap on both sides must stay absent; fill cannot fabricate observations.
    sparse_left = vec(f'time() <= {start+20}', id=c)
    sparse_right = vec(f'time() >= {start+100}', id=c)
    values = [start+fl, start+20+fl, None, None, None, start+100+fl, start+120+fl]
    check(prefix+'per-sample-gaps', f'{sparse_left} + fill({fl}) {sparse_right}', [({'id':c}, values)])
    check(prefix+'gap-instant-empty', f'{sparse_left} + fill({fl}) {sparse_right}', [({'id':c}, values)], True)
    sl = union(vec(a, id=c), vec(x, id=l))
    sr = union(vec(b, id=c), vec(y, id=r))
    check(prefix+'comparison-filter', f'{sl} < fill(0) {sr}', rows(a if a<b else None, None, 0))
    check(prefix+'comparison-bool', f'{sl} > bool fill(0) {sr}', rows(int(a>b), 1, 0))
    check(prefix+'metric-name-control', f'{lhs} * fill(0) 2 keep_metric_names', [({'__name__':lhs,'id':c}, a*2), ({'__name__':lhs,'id':l}, x*2)])

    # Sparse scalar fast paths must obey the same per-sample contract. Use
    # small constant values so floating-point tolerance cannot hide a bad fill.
    early = lambda value: f'{value} if time() <= {start+40}'
    late = lambda value: f'{value} if time() >= {start+80}'
    early_values = lambda value: [value] * 3 + [None] * 4
    late_values = lambda value: [None] * 4 + [value] * 3
    sparse = vec(late(a), id=c)
    av = late_values(a)
    check(prefix+'sparse-vector-scalar', f'{sparse} + fill_left({fl}) {b}', [({'id':c}, pair_values(av, [b]*7, fl, None))])
    check(prefix+'scalar-sparse-vector', f'{b} - fill_right({fr}) {sparse}', [({'id':c}, pair_values([b]*7, av, None, fr, '-'))])
    check(prefix+'sparse-scalar-pair', f'({early(a)}) + fill_left({fl}) fill_right({fr}) ({late(b)})', [({}, pair_values(early_values(a), late_values(b), fl, fr))])
    check(prefix+'nan-scalar-right', f'{sparse} + fill_right({fr}) NaN', [({'id':c}, pair_values(av, [None]*7, None, fr))])
    check(prefix+'nan-scalar-left', f'NaN - fill_left({fl}) {sparse}', [({'id':c}, pair_values([None]*7, av, fl, None, '-'))])
    check(prefix+'infinite-fill-left', f'{sparse} + fill_left(Inf) {b}', [({'id':c}, pair_values(av, [b]*7, math.inf, None))])
    check(prefix+'infinite-fill-right', f'{b} + fill_right(-Inf) {sparse}', [({'id':c}, pair_values([b]*7, av, None, -math.inf))])
    check(prefix+'nan-fill-stays-absent', f'{sparse} + fill_left(NaN) {b}', [({'id':c}, pair_values(av, [b]*7, math.nan, None))])
    check(prefix+'infinite-observation-not-missing', f'{vec("Inf", id=c)} + fill_left({fl}) {b}', [({'id':c}, math.inf)])

    # MetricsQL permits multiple matching series when copied group labels keep
    # each output unique. Presence is per output pair, not per matching group.
    left_group = union(vec(early(a), id=c, worker='one'), vec(late(x), id=c, worker='two'))
    right_group = union(vec(early(b), id=c, region='west'), vec(late(y), id=c, region='east'))
    group_rows = []
    reverse_rows = []
    for worker, lv in [('one', early_values(a)), ('two', late_values(x))]:
        for region, rv in [('west', early_values(b)), ('east', late_values(y))]:
            labels = {'id':c, 'worker':worker, 'region':region}
            group_rows.append((labels, pair_values(lv, rv, fl, fr)))
            reverse_rows.append((labels, pair_values(rv, lv, fl, fr, '-')))
    check(prefix+'group-left-pair-missingness', f'{left_group} + on(id) group_left(region) fill_left({fl}) fill_right({fr}) {right_group}', group_rows)
    check(prefix+'group-right-pair-missingness', f'{right_group} - on(id) group_right(region) fill_left({fl}) fill_right({fr}) {left_group}', reverse_rows)
    check(prefix+'nested-group-missingness', f'sum by (worker) ({left_group} + on(id) group_left(region) fill_left({fl}) fill_right({fr}) {right_group})', [({'worker':worker}, [sum(v for v in point if v is not None) if any(v is not None for v in point) else None for point in zip(*(values for labels,values in group_rows if labels['worker']==worker))]) for worker in ('one','two')])

    # Existing MetricsQL merges disjoint series sharing the matching key.
    # Substitution must not turn those disjoint fragments into duplicates.
    fragments = union(vec(early(b), id=c, source='old'), vec(late(y), id=c, source='new'))
    merged = [b]*3 + [None] + [y]*3
    check(prefix+'disjoint-matching-right', f'{vec(a,id=c)} + on(id) fill_right({fr}) {fragments}', [({'id':c}, pair_values([a]*7, merged, None, fr))])
    check(prefix+'disjoint-matching-left', f'{fragments} - on(id) fill_left({fl}) {vec(a,id=c)}', [({'id':c}, pair_values(merged, [a]*7, fl, None, '-'))])
    check_error(prefix+'overlapping-matching-stays-error', f'{union(vec(a,id=c,source="one"),vec(b,id=c,source="two"))} + on(id) fill(0) {vec(x,id=c)}')
    check_error(prefix+'group-result-collision-stays-error', f'{vec(a,id=c,worker="one")} + on(id) group_left(region) fill(0) {union(vec(b,id=c,source="one",region="west"),vec(y,id=c,source="two",region="west"))}')

    # A filled missing LHS has no original metric name to preserve in a
    # filtering comparison; the name of the present RHS must not leak into it.
    named_left = vec(a, id=c, __name__=lhs)
    named_right = vec(b, id=r, __name__=rhs)
    check(prefix+'missing-left-comparison-name', f'{named_left} < fill_left(0) {named_right}', [({'id':r}, 0)])
    check(prefix+'present-left-comparison-name', f'{named_left} > fill_right(0) {named_right}', [({'__name__':lhs,'id':c}, a)])

    # Logical/set operations retain their existing semantics. Fill is an
    # arithmetic/comparison feature, not permission to change set membership.
    check(prefix+'set-union-control', f'{lhs} or {rhs}', [({'__name__':lhs,'id':c},a), ({'__name__':lhs,'id':l},x), ({'__name__':rhs,'id':r},y)])
    check(prefix+'set-unless-control', f'{lhs} unless {rhs}', [({'__name__':lhs,'id':l},x)])
    check(prefix+'nested-fill-set-filter', f'({lhs} + fill(0) {rhs}) and {rhs}', rows(a+b, None, y))

runtime = Path(tempfile.mkdtemp(prefix='micro1-vm-runtime-'))
if not LOCAL:
    os.chown(runtime, 65532, 65532)
with socket.socket() as sock:
    sock.bind(('127.0.0.1', 0))
    port = sock.getsockname()[1]
base = f'http://127.0.0.1:{port}'
start = int(time.time() // 20) * 20 - 600
args = [BINARY, f'-httpListenAddr=127.0.0.1:{port}', f'-storageDataPath={runtime}/data', '-memory.allowedBytes=256MB', '-search.disableCache', '-search.maxQueryDuration=10s', '-retentionPeriod=1']
if not LOCAL:
    args = ['prlimit', '--nproc=128:128', '--nofile=512:512', '--core=0:0', '--', 'setpriv', '--reuid=65532', '--regid=65532', '--clear-groups', '--no-new-privs', '--inh-caps=-all', '--ambient-caps=-all', '--bounding-set=-all', 'env', '-i', 'PATH=/usr/bin:/bin', 'GOMAXPROCS=2'] + args
process = None
try:
    with (OUTPUT / 'server.log').open('wb') as log:
        process = subprocess.Popen(args, cwd=runtime, stdin=subprocess.DEVNULL, stdout=log, stderr=log, start_new_session=True)
        deadline = time.monotonic() + 45
        while True:
            try:
                request('/health')
                break
            except Exception:
                if process.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError('candidate server did not become healthy')
                time.sleep(.2)
        for seed in (127, 941, 2027):
            run_seed(seed)
except Exception as exc:
    checks.append({'case':'controller-or-startup', 'passed':False, 'detail':str(exc)})
finally:
    if process is not None:
        try:
            os.killpg(process.pid, signal.SIGKILL)
        except ProcessLookupError:
            pass
        process.wait()
    shutil.rmtree(runtime, ignore_errors=True)
reward = int(bool(checks) and all(c['passed'] for c in checks))
(OUTPUT / 'checks.json').write_text(json.dumps(checks, indent=2) + '\n')
(OUTPUT / 'status.json').write_text(json.dumps({'phase':'verify','status':'passed' if reward else 'behavioral_failure','reward':reward,'passed':sum(c['passed'] for c in checks),'total':len(checks)}) + '\n')
(OUTPUT / 'reward.txt').write_text(str(reward) + '\n')
print(json.dumps({'reward':reward,'passed':sum(c['passed'] for c in checks),'total':len(checks)}))
