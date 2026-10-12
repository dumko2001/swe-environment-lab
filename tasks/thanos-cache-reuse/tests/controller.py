#!/usr/bin/env python3
"""Independent root-owned HTTP controller; candidate code never imports this module."""
import concurrent.futures
import base64
import bcrypt
import secrets
import hashlib
import tempfile
import http.server
import json
import os
import random
import re
import socket
import socketserver
import subprocess
import sys
import threading
import time
import urllib.parse
import urllib.request
from pathlib import Path

candidate, trusted, runtime = sys.argv[1:]
processes = []
receipts = []
cache_read_receipts = []
checks = []
lock = threading.Lock()
rng = random.SystemRandom()
nonce = rng.randrange(10000, 90000)
base = 1700006400 + 86400 * rng.randrange(1, 100)

def port():
    with socket.socket() as s:
        s.bind(('127.0.0.1', 0))
        return s.getsockname()[1]

def launch(args, name, untrusted=False):
    log = open(Path(runtime) / (name + '.log'), 'wb')
    if untrusted and os.environ.get('MICRO1_LOCAL_TEST') != '1':
        args = ['/tests/common/run-candidate.sh', args[0], runtime] + args[1:]
    p = subprocess.Popen(args, stdout=log, stderr=subprocess.STDOUT, cwd=runtime)
    processes.append(p)
    return p

def get(url, headers=None):
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers or {}), timeout=20) as r:
        return r.read()

def ready(url, headers=None):
    deadline = time.monotonic() + 40
    while time.monotonic() < deadline:
        try:
            get(url, headers)
            return
        except Exception:
            if any(p.poll() is not None for p in processes):
                raise RuntimeError('service exited before readiness')
            time.sleep(.1)
    raise RuntimeError('readiness deadline')

def count():
    with lock:
        return len(receipts)

class Proxy(http.server.BaseHTTPRequestHandler):
    def log_message(self, *args):
        pass
    def do_POST(self):
        body = self.rfile.read(int(self.headers.get("Content-Length", "0"))).decode()
        self.path += ("&" if "?" in self.path else "?") + body
        self.do_GET()
    def do_GET(self):
        params = urllib.parse.parse_qs(urllib.parse.urlsplit(self.path).query)
        if '/api/v1/query_range' in self.path:
            with lock:
                receipts.append({'query': params.get('query'), 'start': params.get('start'), 'end': params.get('end'), 'step': params.get('step'), 'tenant': self.headers.get('THANOS-TENANT')})
        try:
            query = params.get('query', [''])[0]
            if query.startswith('micro1_hist_'):
                start = int(float(params['start'][0])); end = int(float(params['end'][0])); step = int(float(params['step'][0]))
                hist = [[t, {'count': str(t-base+1), 'sum': str((t-base+1)*2), 'buckets': [[0, '0', '4', str(t-base+1)]]}] for t in range(start, end+1, step)]
                data = json.dumps({'status': 'success', 'data': {'resultType': 'matrix', 'result': [{'metric': {'series': 'histogram'}, 'histograms': hist}]}}).encode()
            else:
                data = get(query_url + self.path, query_auth)
            self.send_response(200); self.send_header('Content-Type', 'application/json'); self.end_headers(); self.wfile.write(data)
        except Exception as e:
            self.send_response(502); self.end_headers(); self.wfile.write(str(e).encode())

def request(q, start, end, step, tenant='a', **extra):
    params = dict(query=q, start=start, end=end, step=step, max_source_resolution='0s', **extra)
    data = json.loads(get(front_url + '/api/v1/query_range?' + urllib.parse.urlencode(params), {'THANOS-TENANT': tenant}))
    assert data['status'] == 'success', data
    return data['data']['result']

def expect(q, start, end, step, tenant='a', **extra):
    result = request(q, start, end, step, tenant, **extra)
    if q.startswith('micro1_missing_'):
        assert not result, result
    elif q.startswith('micro1_hist_'):
        assert len(result) == 1, result
        assert result[0]['metric'] == {'series': 'histogram'}, result
        expected = [[t, {'count': str(t-base+1), 'sum': str((t-base+1)*2), 'buckets': [[0, '0', '4', str(t-base+1)]]}] for t in range(start, end+1, step)]
        assert result[0].get('histograms') == expected, result
        assert not result[0].get('values'), result
    elif q.startswith('label_replace'):
        adds = [int(v) for v in re.findall(r'time\(\)\s*\+\s*(\d+)', q)]
        assert len(result) == 2, result
        by_label = {r['metric'].get('instance'): r for r in result}
        assert set(by_label) == {'east', 'west'}, result
        for name, add in zip(['east', 'west'], adds):
            row = by_label[name]
            assert row['metric'] == {'instance': name}, row
            assert [v[0] for v in row['values']] == list(range(start,end+1,step)), row
            assert all(float(v)==t+add for t,v in row['values']), row
    else:
        assert len(result) == 1, result
        assert result[0]['metric'] == {}, result
        actual = result[0]['values']
        assert [x[0] for x in actual] == list(range(start, end+1, step)), actual
        add = int(q.split('+')[-1].rstrip(') '))
        assert all(float(v) == t + add for t, v in actual), actual
    return result

def cache_command(command):
    with socket.create_connection(('127.0.0.1', cache_port), timeout=3) as s:
        s.sendall(command.encode() + b'\r\n')
        data = b''
        while not data.endswith((b'END\r\n', b'OK\r\n')):
            chunk = s.recv(65536)
            if not chunk:
                raise RuntimeError('Memcached closed before command response completed')
            data += chunk
        return data.decode()

def reset():
    cache_command('flush_all')

def warm(q, start, end, step, **extra):
    before = count()
    expect(q, start, end, step, **extra)
    assert count() > before, "cold request bypassed observed downstream route"
    # Async cache writes have no fixed latency guarantee. Require two observed
    # stable exact hits within a bounded deadline, allowing the warm-up to settle.
    deadline = time.monotonic()+5
    stable = 0
    while stable < 2:
        assert time.monotonic() < deadline, 'ordinary exact cache hits did not stabilize'
        before = count()
        expect(q, start, end, step, **extra)
        stable = stable+1 if count()==before else 0
        time.sleep(.025)

def check(name, fn):
    try:
        fn()
        checks.append({'name': name, 'passed': True})
    except Exception as e:
        checks.append({'name': name, 'passed': False, 'error': str(e)[:1000]})

def lookup_reads_are_batched(commands):
    """Check the conventional ordinary-lookup plus batched-alternatives path.

    Keys are opaque. Multiple batches and a singleton tail are allowed.
    This does not model arbitrary multi-stage index protocols or prove scaling.
    """
    unique_keys = {key for command in commands for key in command['key_fingerprints']}
    singleton_reads = sum(len(set(command['key_fingerprints'])) <= 1 for command in commands)
    return len(commands) <= 1 or (singleton_reads <= 2 and len(commands) < len(unique_keys))

def forward_cache_commands(reader, upstream, observe):
    """Forward ASCII protocol frames without interpreting cached value bytes."""
    storage_commands = {'set', 'add', 'replace', 'append', 'prepend', 'cas'}
    simple_commands = {'delete', 'incr', 'decr', 'touch', 'stats', 'version',
                       'verbosity', 'flush_all', 'quit', 'mn', 'md', 'ma', 'me'}
    while True:
        line = reader.readline(65537)
        if not line:
            return
        if len(line) > 65536 or not line.endswith(b'\r\n'):
            raise RuntimeError('invalid or oversized Memcached command framing')
        fields = line[:-2].split()
        if not fields:
            raise RuntimeError('empty Memcached command')
        command = fields[0].decode('ascii')
        payload_size = None
        if command in storage_commands:
            if len(fields) < 5:
                raise RuntimeError('incomplete Memcached storage command')
            payload_size = int(fields[4])
        elif command == 'ms':
            if len(fields) < 3:
                raise RuntimeError('incomplete Memcached meta storage command')
            payload_size = int(fields[2])
        elif command in {'get', 'gets', 'gat', 'gats', 'mg'}:
            keys = fields[1:2] if command == 'mg' else fields[2:] if command in {'gat', 'gats'} else fields[1:]
            if not keys:
                raise RuntimeError('Memcached retrieval has no keys')
            observe(command, keys)
        elif command not in simple_commands:
            raise RuntimeError('unsupported Memcached command framing: '+command)
        upstream.sendall(line)
        if payload_size is not None:
            if not 0 <= payload_size <= 32 * 1024 * 1024:
                raise RuntimeError('invalid Memcached storage payload size')
            remaining = payload_size + 2
            chunks = []
            while remaining:
                data = reader.read(min(65536, remaining))
                if not data:
                    raise RuntimeError('Memcached storage payload ended early')
                chunks.append(data)
                remaining -= len(data)
            payload = b''.join(chunks)
            if not payload.endswith(b'\r\n'):
                raise RuntimeError('Memcached storage payload missing terminator')
            upstream.sendall(payload)

class CacheCommandRecorder(socketserver.ThreadingTCPServer):
    allow_reuse_address = True
    daemon_threads = True
    block_on_close = False

    def __init__(self, upstream_socket):
        self.upstream_socket = upstream_socket
        self.commands = []
        self.errors = []
        self.connections = set()
        self.mutex = threading.Lock()
        self.stopping = False
        super().__init__(('127.0.0.1', 0), CacheCommandForwarder)

    def observe(self, command, keys):
        # Treat keys as opaque identities; no candidate key format is assumed.
        with self.mutex:
            self.commands.append({'command': command, 'key_count': len(keys),
                                  'key_fingerprints': [hashlib.sha256(key).hexdigest() for key in keys]})

    def snapshot(self):
        with self.mutex:
            return list(self.commands), list(self.errors)

    def record_error(self, error):
        with self.mutex:
            if not self.stopping:
                self.errors.append(str(error))

    def close_connections(self):
        with self.mutex:
            self.stopping = True
            connections = list(self.connections)
        for connection in connections:
            try:
                connection.shutdown(socket.SHUT_RDWR)
            except OSError:
                pass
            connection.close()

class CacheCommandForwarder(socketserver.BaseRequestHandler):
    def handle(self):
        upstream = None
        closed = threading.Event()

        def transport_error(error):
            # EOF/reset/broken pipe are ordinary pooled-connection teardown.
            # Other transport failures remain evidence unless closure is ours.
            if isinstance(error, (ConnectionResetError, BrokenPipeError)):
                closed.set()
            elif not closed.is_set():
                self.server.record_error(error)

        try:
            upstream = socket.socket(socket.AF_UNIX, socket.SOCK_STREAM)
            upstream.settimeout(3)
            upstream.connect(self.server.upstream_socket)
            upstream.settimeout(None)
            with self.server.mutex:
                self.server.connections.update((self.request, upstream))
            def return_responses():
                try:
                    while True:
                        data = upstream.recv(65536)
                        if not data:
                            closed.set()
                            break
                        self.request.sendall(data)
                except OSError as error:
                    transport_error(error)
                finally:
                    closed.set()
                    try:
                        self.request.shutdown(socket.SHUT_RDWR)
                    except OSError:
                        pass
            response_thread = threading.Thread(target=return_responses, daemon=True)
            response_thread.start()
            with self.request.makefile('rb') as reader:
                forward_cache_commands(reader, upstream, self.server.observe)
            closed.set()
        except OSError as error:
            transport_error(error)
        except Exception as error:
            # Malformed protocol/payload errors are never normal peer closure.
            self.server.record_error(error)
        finally:
            closed.set()
            if upstream is not None:
                try:
                    upstream.shutdown(socket.SHUT_RDWR)
                except OSError:
                    pass
                upstream.close()
                with self.server.mutex:
                    self.server.connections.discard(upstream)
                    self.server.connections.discard(self.request)


def cold_cache_lookup_batching():
    """Count wire retrieval frames, not cmd_get (which counts individual keys)."""
    global front_url
    normal_url = front_url
    cache_dir = tempfile.TemporaryDirectory(prefix='micro1-private-memcached-')
    private_socket = str(Path(cache_dir.name) / 'cache.sock')
    recorder = recorder_thread = probe = private_cache = None
    try:
        # Only the trusted recorder can traverse this root-owned 0700 directory.
        # The candidate cannot bypass measurement via a backend TCP port.
        private_cache = launch(['memcached', '-s', private_socket, '-a', '600',
                                '-m', '32', '-u', 'root'], 'memcached-cache-read-bound')
        deadline = time.monotonic() + 10
        while True:
            try:
                with socket.socket(socket.AF_UNIX, socket.SOCK_STREAM) as connection:
                    connection.settimeout(3)
                    connection.connect(private_socket)
                    connection.sendall(b'version\r\n')
                    with connection.makefile('rb') as reader:
                        version = reader.readline(4096).decode().strip()
                    assert version.startswith('VERSION '), version
                break
            except OSError:
                if private_cache.poll() is not None or time.monotonic() > deadline:
                    raise RuntimeError('private Memcached readiness failed')
                time.sleep(.05)
        recorder = CacheCommandRecorder(private_socket)
        recorder_thread = threading.Thread(target=recorder.serve_forever, daemon=True)
        recorder_thread.start()
        probe_port = port()
        args = [
            f'--http-address=127.0.0.1:{probe_port}' if arg.startswith('--http-address=')
            else '--query-range.split-interval=72h' if arg.startswith('--query-range.split-interval=')
            else arg.replace(f'127.0.0.1:{cache_port}', f'127.0.0.1:{recorder.server_address[1]}')
            if arg.startswith('--query-range.response-cache-config=') else arg
            for arg in front_args
        ]
        probe = launch(args, 'frontend-cache-lookup-batching', True)
        front_url = f'http://127.0.0.1:{probe_port}'
        ready(front_url+'/-/ready')
        # Fresh dedicated cache, unique expression, and one unsplit range.
        start = (base // (3 * 86400)) * (3 * 86400)
        end = start + 2 * 86400
        step = 12 * 3600
        q = f'vector(time()+{secrets.randbits(48)})'
        before_downstream = count()
        before_commands, before_errors = recorder.snapshot()
        assert not before_errors, before_errors
        expect(q, start, end, step)
        commands, errors = recorder.snapshot()
        observed = commands[len(before_commands):]
        unique_keys = {key for command in observed for key in command['key_fingerprints']}
        cache_read_receipts.append({
            'name': 'cold_compatible_lookup_batching',
            'start': start, 'end': end, 'step': step,
            'memcached_version': version,
            'retrieval_commands': observed,
            'remote_read_commands': len(observed),
            'distinct_keys': len(unique_keys),
            'downstream_requests': count() - before_downstream,
            'recorder_errors': errors,
        })
        assert not errors, errors
        assert count() > before_downstream, 'cold request bypassed observed downstream route'
        # Permit multiple batches, one ordinary-key lookup, and a final
        # partial batch. A single-key design needs no batching.
        # This finite trace checks batching, not asymptotic complexity.
        singleton_reads = sum(len(set(command['key_fingerprints'])) <= 1 for command in observed)
        assert lookup_reads_are_batched(observed), (
            f'compatible lookup issued {singleton_reads} single-key reads '
            f'across {len(observed)} commands for {len(unique_keys)} distinct keys; '
            'alternative lookups must be batched')
    finally:
        front_url = normal_url
        if recorder is not None:
            recorder.close_connections()
        for process in (probe, private_cache):
            if process is not None:
                process.terminate()
                try:
                    process.wait(timeout=5)
                except subprocess.TimeoutExpired:
                    process.kill(); process.wait()
                processes.remove(process)
        if recorder is not None:
            recorder.shutdown()
            recorder.server_close()
            recorder_thread.join(timeout=2)
        cache_dir.cleanup()

def divisible():
    for dense, coarse in [(15,60), (20,60), (5,30), (30,120), (60,300)]:
        reset(); q=f'vector(time()+{nonce+dense+coarse})'
        warm(q,base,base+1800,dense)
        before=count()
        for step in [coarse,coarse,dense,coarse]:
            expect(q,base,base+1800,step)
            time.sleep(.1)
        assert count()==before, f'{dense}->{coarse} made downstream requests'

def longer_steps(dense, coarse):
    """Exercise the public step domain beyond the short dashboard examples."""
    reset()
    q = f'vector(time()+{nonce + 2000 + dense + coarse})'
    end = base + 4 * coarse
    warm(q, base, end, dense)
    before = count()
    # Keep the original dense entry intact while repeatedly zooming out. The
    # hour cases also cross the configured daily split boundary.
    for step in (coarse, coarse, dense, coarse):
        expect(q, base, end, step)
        time.sleep(.1)
    assert count() == before, f'{dense}->{coarse} made downstream requests'

def incompatible():
    reset(); q=f'vector(time()+{nonce+999})'
    warm(q,base,base+600,20)
    before=count(); expect(q,base,base+600,30)
    assert count()>before, 'nondivisible grid reused'
    reset(); q=f'vector(time()+{nonce+998})'
    warm(q,base,base+600,15)
    before=count(); expect(q,base+7,base+607,60)
    assert count()>before, 'misaligned grid reused'

def ordinary_cached_subrange(q, single_point=False):
    """An exact-step cache hit must respect the newly requested time bounds."""
    reset()
    warm(q, base, base+600, 60)
    before = count()
    start, end = (base+300, base+300) if single_point else (base+120, base+240)
    for _ in range(2):
        # expect compares exact timestamps, labels and float/histogram values.
        expect(q, start, end, 60)
        # Single-point float queries may use the baseline instant-query path.
        # Require exact output there; preserve the ordinary interval cache guarantee.
        if not single_point:
            assert count() == before, 'covered ordinary subrange was fetched again'
    # Narrow reads must not truncate the cached full interval either.
    before = count()
    expect(q, base, base+600, 60)
    assert count() == before, 'narrowing discarded ordinary cached coverage'

def ordinary_subrange_float():
    ordinary_cached_subrange(f'vector(time()+{nonce+6101})')

def ordinary_single_point_float():
    ordinary_cached_subrange(f'vector(time()+{nonce+6102})', single_point=True)

def ordinary_subrange_histogram():
    ordinary_cached_subrange(f'micro1_hist_narrow_{nonce}')

def ordinary_single_point_histogram():
    ordinary_cached_subrange(f'micro1_hist_single_{nonce}', single_point=True)

def ordinary_growth(q):
    reset()
    warm(q, base, base+300, 60)
    before = count()
    expect(q, base, base+600, 60)
    assert count() > before, 'expanded ordinary request never fetched missing coverage'
    deadline = time.monotonic()+5
    stable = 0
    while stable < 2:
        assert time.monotonic() < deadline, 'expanding an ordinary cache entry discarded previously cached coverage'
        before = count()
        expect(q, base, base+600, 60)
        stable = stable+1 if count() == before else 0
        time.sleep(.025)

def ordinary_growth_float():
    ordinary_growth(f'vector(time()+{nonce+6301})')

def ordinary_growth_histogram():
    ordinary_growth(f'micro1_hist_growth_{nonce}')

def ordinary_hit_with_partial_dense_entry():
    """Adding a shorter dense cache entry must not invalidate a full exact hit."""
    reset()
    q = f'vector(time()+{nonce+5701})'
    warm(q, base, base+600, 60)
    warm(q, base, base+120, 15)
    before = count()
    for _ in range(3):
        expect(q, base, base+600, 60)
        time.sleep(.1)
    assert count() == before, 'partial denser entry displaced a complete ordinary cache hit'

def partial():
    reset(); q=f'vector(time()+{nonce+997})'
    warm(q,base,base+300,15)
    before=count(); expect(q,base,base+600,60)
    assert count()>before, 'missing coverage never fetched'
    with lock:
        extra=receipts[before:]
    assert all(float(r['start'][0]) >= base+240 for r in extra), 'cached prefix was unnecessarily refetched'
    expect(q,base,base+600,60)
    expect(q,base,base+300,15)

def missing_final_timestamp(q):
    reset()
    warm(q,base,base+300,15)
    # Exactly one requested grid timestamp lies beyond the warm extent.
    # Require complete values, including that endpoint; fetch count is unrestricted.
    expect(q,base,base+360,60)

def missing_final_timestamp_float():
    missing_final_timestamp(f'vector(time()+{nonce+9101})')

def missing_final_timestamp_histogram():
    missing_final_timestamp(f'micro1_hist_tail_{nonce}')

def window_growth(q):
    reset()
    # A zoomed window has a missing prefix and overlaps a longer warm extent.
    # Expanding it later must recover the full tail, regardless of cache writes.
    warm(q,base+300,base+1800,15)
    before=count()
    expect(q,base,base+600,60)
    assert count()>before, 'missing prefix never fetched'
    # Allow asynchronous remote writes to become observable. Correct responses
    # are required on either side of a write, so no write-count is assumed.
    time.sleep(.2)
    expect(q,base,base+1800,60)
    expect(q,base+300,base+1800,15)
    expect(q,base,base+1800,60)

def window_growth_float():
    window_growth(f'vector(time()+{nonce+781})')

def window_growth_histogram():
    window_growth(f'micro1_hist_window_{nonce}')

def isolation():
    reset(); q=f'vector(time()+{nonce+996})'
    warm(q,base,base+600,15)
    before=count(); expect(q,base,base+600,60,tenant='b')
    assert count()>before, 'tenant cache identity collapsed'
    before=count(); expect(q,base,base+600,60,partial_response='false')
    assert count()>before, 'partial response identity collapsed'

def histograms():
    reset(); q=f'micro1_hist_{nonce}'
    warm(q,base,base+600,15)
    before=count()
    expect(q,base,base+600,60); time.sleep(.15); expect(q,base,base+600,60); expect(q,base,base+600,15)
    assert count()==before, 'histogram fallback still reaches downstream'

def labeled():
    reset()
    q=f'label_replace(vector(time()+{nonce+881}), "instance", "east", "", "") or label_replace(vector(time()+{nonce+882}), "instance", "west", "", "")'
    warm(q,base,base+600,15)
    before=count()
    expect(q,base,base+600,60)
    expect(q,base,base+600,30)
    assert count()==before, 'labeled series fallback still reaches downstream'

def empty():
    reset(); q=f'micro1_missing_{nonce}'
    warm(q,base,base+600,15)
    before=count(); expect(q,base,base+600,60)
    assert count()==before, 'empty cached result not reused'

def concurrent_reuse():
    reset(); q=f'vector(time()+{nonce+995})'
    warm(q,base,base+1800,15)
    before=count()
    with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
        list(pool.map(lambda s: expect(q,base,base+1800,s), [30,60,120,300,60,30]))
    assert count()==before, 'warm concurrent lookups missed'

def restart_reuse():
    global frontend
    reset(); q=f'vector(time()+{nonce+883})'
    warm(q,base,base+600,15)
    frontend.terminate(); frontend.wait(timeout=5); processes.remove(frontend)
    frontend=launch(front_args,'frontend-restarted',True)
    ready(front_url+'/-/ready')
    before=count()
    expect(q,base,base+600,60)
    assert count()==before, 'compatible reuse did not survive frontend replacement'

def boundary():
    reset(); q=f'vector(time()+{nonce+994})'
    warm(q,base+86100,base+86700,15)
    before=count(); expect(q,base+86100,base+86700,60)
    assert count()==before, 'split boundary lost compatible cache coverage'

try:
    Path(runtime).mkdir(parents=True, exist_ok=True)
    query_port, grpc_port, cache_port, front_port = [port() for _ in range(4)]
    query_url=f'http://127.0.0.1:{query_port}'
    front_url=f'http://127.0.0.1:{front_port}'
    auth_dir = tempfile.TemporaryDirectory(prefix='micro1-query-auth-')
    password = secrets.token_urlsafe(32)
    username = secrets.token_hex(12)
    query_auth = {'Authorization': 'Basic ' + base64.b64encode((username+':'+password).encode()).decode()}
    auth_file = Path(auth_dir.name) / 'web.yml'
    auth_file.write_text('basic_auth_users:\n  '+username+': '+bcrypt.hashpw(password.encode(),bcrypt.gensalt()).decode()+'\n')
    auth_file.chmod(0o600)
    cert = Path(auth_dir.name) / 'grpc.crt'
    key = Path(auth_dir.name) / 'grpc.key'
    subprocess.run(['openssl','req','-x509','-newkey','rsa:2048','-nodes','-keyout',str(key),'-out',str(cert),'-days','1','-subj','/CN=micro1-private-query'],check=True,stdout=subprocess.DEVNULL,stderr=subprocess.DEVNULL)
    launch([trusted,'query',f'--http-address=127.0.0.1:{query_port}',f'--grpc-address=127.0.0.1:{grpc_port}','--http.config='+str(auth_file),'--grpc-server-tls-cert='+str(cert),'--grpc-server-tls-key='+str(key),'--grpc-server-tls-client-ca='+str(cert),'--log.level=error'], 'query')
    ready(query_url+'/-/ready',query_auth)
    launch(['memcached','-p',str(cache_port),'-l','127.0.0.1','-m','32','-u','nobody'], 'memcached')
    deadline=time.monotonic()+10
    while True:
        try:
            cache_command('stats'); break
        except OSError:
            if time.monotonic()>deadline: raise
            time.sleep(.05)
    proxy=http.server.ThreadingHTTPServer(('127.0.0.1',0),Proxy)
    threading.Thread(target=proxy.serve_forever,daemon=True).start()
    cache = f'type: MEMCACHED\nconfig:\n  addresses: [127.0.0.1:{cache_port}]\n  timeout: 1s\n  expiration: 1h\n'
    front_args=[candidate,'query-frontend',f'--http-address=127.0.0.1:{front_port}',f'--query-frontend.downstream-url=http://127.0.0.1:{proxy.server_port}', '--no-query-range.align-range-with-step','--query-range.split-interval=24h','--query-range.response-cache-max-freshness=0s','--query-range.response-cache-config='+cache,'--log.level=error']
    frontend=launch(front_args, 'frontend',True)
    ready(front_url+'/-/ready')
    for name, fn in [('cold_compatible_lookup_batching',cold_cache_lookup_batching),('divisible_grids',divisible),('incompatible_grids',incompatible),('partial_coverage',partial),('ordinary_subrange_float',ordinary_subrange_float),('ordinary_single_point_float',ordinary_single_point_float),('ordinary_subrange_histogram',ordinary_subrange_histogram),('ordinary_single_point_histogram',ordinary_single_point_histogram),('ordinary_hit_with_partial_dense_entry',ordinary_hit_with_partial_dense_entry),('ordinary_growth_float',ordinary_growth_float),('ordinary_growth_histogram',ordinary_growth_histogram),('missing_final_timestamp_float',missing_final_timestamp_float),('missing_final_timestamp_histogram',missing_final_timestamp_histogram),('window_growth_float',window_growth_float),('window_growth_histogram',window_growth_histogram),('request_isolation',isolation),('native_histograms',histograms),('series_identity',labeled),('empty_results',empty),('concurrent_reuse',concurrent_reuse),('split_boundary',boundary),('frontend_replacement',restart_reuse)]:
        check(name,fn)
    # Together with divisible_grids, both requested and cached steps cover the
    # representative dashboard intervals through 12 hours. Each case reports alone.
    for dense, coarse in [(1,5), (10,20), (120,600), (300,900), (600,1800),
                          (900,3600), (1800,7200), (3600,10800),
                          (10800,21600), (21600,43200)]:
        check(f'domain_steps_{dense}_{coarse}',
              lambda dense=dense, coarse=coarse: longer_steps(dense, coarse))
except Exception as e:
    checks.append({'name':'infrastructure','passed':False,'error':str(e)})
finally:
    for p in reversed(processes):
        p.terminate()
    for p in processes:
        try: p.wait(timeout=5)
        except subprocess.TimeoutExpired: p.kill(); p.wait()
    report={'checks':checks,'downstream_receipts':receipts,'cache_read_receipts':cache_read_receipts,'passed': bool(checks) and all(c['passed'] for c in checks)}
    if os.environ.get('MICRO1_LOCAL_TEST') == '1':
        print(json.dumps(report,indent=2))
    else:
        Path('/logs/verifier/behavior.json').write_text(json.dumps(report,indent=2))
        Path('/logs/verifier/status.json').write_text(json.dumps({'status':'passed' if report['passed'] else 'behavioral_failure'}))
    sys.exit(0 if report['passed'] else 1)
