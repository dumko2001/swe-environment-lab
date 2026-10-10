#!/usr/bin/env python3
"""Smoke-check public seeded data in the actor's real running service stack.

This is a deployment check, not a hidden task verifier. Run capture before a
restart/rebuild and compare afterward to check public sample preservation.
"""
import argparse
import hashlib
import json
import socket
from pathlib import Path
import subprocess
import time
import urllib.parse
import urllib.request

TASKS = ('unix-socket-scraping', 'prometheus-subquery-semantics',
         'histogram-rate-semantics', 'start-timestamp-persistence',
         'stale-wal-expiry', 'thanos-cache-reuse', 'victoriametrics-fill-modifiers')


def get(base, path, params=None):
    url = base + path
    if params:
        url += '?' + urllib.parse.urlencode(params)
    with urllib.request.urlopen(url, timeout=15) as response:
        return json.load(response)


def query(base, expression, end, start=None, step=60):
    params = {'query': expression, 'time': end}
    path = '/api/v1/query'
    if start is not None:
        path = '/api/v1/query_range'
        params = {'query': expression, 'start': start, 'end': end, 'step': step}
    reply = get(base, path, params)
    assert reply.get('status') == 'success', (expression, reply)
    result = reply['data']['result']
    assert result, f'No real samples for {expression}'
    return sorted(result, key=lambda row: json.dumps(row['metric'], sort_keys=True))


def sample_count(rows, histogram=False):
    keys = ('histograms', 'histogram') if histogram else ('values', 'value')
    return sum(len(row.get(keys[0], [])) + int(keys[1] in row) for row in rows)


def cache_hits():
    with socket.create_connection(('127.0.0.1', 11211), timeout=3) as conn:
        conn.sendall(b'stats\r\n')
        body = b''
        while not body.endswith(b'END\r\n'):
            chunk = conn.recv(65536)
            if not chunk:
                raise RuntimeError('Memcached closed before complete stats')
            body += chunk
    stats = dict(line.split()[1:] for line in body.decode().splitlines() if line.startswith('STAT '))
    return int(stats['get_hits'])


def uds_exporters(root, phase):
    for label, expected in [('alpha', 11), ('beta', 29), ('secure', 47)]:
        command = ['curl', '--fail', '--silent', '--show-error', '--max-time', '5',
                   '--unix-socket', str(root / (label + '.sock'))]
        if label == 'secure':
            command += ['--cacert', str(root / 'cert.pem'), 'https://localhost:18080/metrics']
        else:
            command += ['http://localhost:18080/metrics']
        for attempt in range(30):
            result = subprocess.run(command, capture_output=True, text=True)
            if result.returncode == 0:
                break
            time.sleep(1)
        assert result.returncode == 0, f'{label} socket exporter unavailable: {result.stderr}'
        assert f'demo_exporter_value{{exporter="{label}"}} {expected}' in result.stdout, result.stdout
    if phase != 'oracle':
        return
    base = 'http://127.0.0.1:9090'
    for _ in range(45):
        targets = get(base, '/api/v1/targets')['data']['activeTargets']
        selected = {t['labels']['job']: t for t in targets if t['labels'].get('job') in ('alpha', 'beta', 'secure')}
        if len(selected) == 3 and all(t['health'] == 'up' for t in selected.values()):
            rows = query(base, 'demo_exporter_value', int(time.time()))
            values = {row['metric'].get('exporter'): float(row['value'][1]) for row in rows}
            if values == {'alpha': 11.0, 'beta': 29.0, 'secure': 47.0}:
                return
        time.sleep(1)
    raise AssertionError(f'Oracle must scrape all distinct HTTP/TLS socket targets: {selected}')


def smoke(task, phase):
    root = Path('/var/lib/task-env' if task == 'thanos-cache-reuse' else '/app/task-runtime')
    seed = json.loads((root / 'seed.json').read_text())
    samples = {}
    if task == 'victoriametrics-fill-modifiers':
        base = 'http://127.0.0.1:8428'
        for expression in ('demo_requests', 'demo_capacity'):
            rows = query(base, expression, seed['end'], seed['start'], seed['step'])
            assert len(rows) == 2 and sample_count(rows) >= 4, (expression, rows)
            samples[expression] = rows
        if phase == 'oracle':
            rows = query(base, 'demo_requests - fill(0) demo_capacity', seed['end'])
            values = {row['metric']['host']: float(row['value'][1]) for row in rows}
            assert values == {'shared': 12.0, 'requests_only': 10.0, 'capacity_only': -9.0}, values
    elif task == 'thanos-cache-reuse':
        # Exercise the frontend (not a direct shortcut to the producer).
        base = 'http://127.0.0.1:10906'
        for expression, histogram in [('playground_requests_total', False), ('playground_latency_seconds', True)]:
            # Instant histogram queries avoid demanding the cache task's fix at baseline.
            rows = query(base, expression, seed['end'], None if histogram else seed['start'], 60)
            minimum = 2 if histogram else 200
            assert len(rows) == 2 and sample_count(rows, histogram) >= minimum, (expression, rows)
            samples[expression] = rows
        # Observe actual cache use, allowing asynchronous writes to settle.
        before = cache_hits()
        deadline = time.monotonic() + 15
        while True:
            again = query(base, 'playground_requests_total', seed['end'], seed['start'], 60)
            assert again == samples['playground_requests_total'], 'Repeated frontend query changed'
            if cache_hits() > before:
                break
            assert time.monotonic() < deadline, 'Repeated query never hit Memcached'
            time.sleep(.1)
    else:
        base = 'http://127.0.0.1:9090'
        for expression, histogram in [('demo_counter', False), ('demo_histogram', True), ('demo_float_histogram', True)]:
            rows = query(base, expression, seed['fixed_end'], seed['fixed_start'], 15)
            assert sample_count(rows, histogram) == 121, (expression, rows)
            samples[expression] = rows
        if task == 'unix-socket-scraping':
            uds_exporters(root, phase)
    encoded = json.dumps({'task': task, 'seed': seed, 'samples': samples}, sort_keys=True, separators=(',', ':')).encode()
    return {'task': task, 'sha256': hashlib.sha256(encoded).hexdigest(),
            'metrics': list(samples), 'seed': seed}


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('task', choices=TASKS)
    parser.add_argument('--phase', choices=('baseline', 'oracle'), default='baseline')
    parser.add_argument('--mode', choices=('check', 'capture', 'compare'), default='check')
    parser.add_argument('--snapshot', type=Path, default=Path('/tmp/public-runtime-fingerprint.json'))
    args = parser.parse_args()
    fingerprint = smoke(args.task, args.phase)
    if args.mode == 'capture':
        args.snapshot.write_text(json.dumps(fingerprint, indent=2) + '\n')
    elif args.mode == 'compare':
        previous = json.loads(args.snapshot.read_text())
        assert previous == fingerprint, f'Public stored samples changed: {previous} -> {fingerprint}'
    print(json.dumps({'status': 'pass', 'phase': args.phase, 'mode': args.mode, **fingerprint}, sort_keys=True))


if __name__ == '__main__':
    main()
