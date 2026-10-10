#!/usr/bin/env python3
"""Check domain policy in one owned 4-CPU/8-GiB sandbox, then delete it.

No model requests or credentials are sent into the sandbox. HTTPS checks make
unauthenticated GETs. A deny response received over HTTP is conservatively
reported as inconclusive rather than mistaken for a successful egress block.
"""
from __future__ import annotations

import argparse
import json
import os
import re
from pathlib import Path
import shlex
import sys
import uuid
from urllib.parse import urlsplit

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))
from private_eval.network_restricted_codex import normalize_domains

# This script runs in the disposable sandbox and uses only the standard library.
PROBE = r'''
import hashlib,http.server,json,socket,socketserver,sys,threading,urllib.error,urllib.request
from pathlib import Path
urls=json.loads(sys.argv[1])
results={}
for name,url in urls.items():
    try:
        with urllib.request.urlopen(url,timeout=7) as r:
            results[name]={'kind':'http_response','status':r.status,'body_sha256':hashlib.sha256(r.read(4096)).hexdigest()}
    except urllib.error.HTTPError as e:
        results[name]={'kind':'http_response','status':e.code,'body_sha256':hashlib.sha256(e.read(4096)).hexdigest()}
    except Exception as e:
        results[name]={'kind':'transport_failure','error_type':type(e).__name__}
try:
    with socket.create_connection(('1.1.1.1',443),timeout=7):
        results['direct_ip']={'kind':'tcp_connected'}
except Exception as e:
    results['direct_ip']={'kind':'transport_failure','error_type':type(e).__name__}
class Handler(http.server.BaseHTTPRequestHandler):
    def do_GET(self):
        body=b'owned-preflight-loopback-ok'
        self.send_response(200);self.send_header('Content-Length',str(len(body)));self.end_headers();self.wfile.write(body)
    def log_message(self,*args):pass
server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
threading.Thread(target=server.serve_forever,daemon=True).start()
try:
    with urllib.request.urlopen('http://127.0.0.1:'+str(server.server_port),timeout=5) as r:
        results['loopback_http']={'ok':r.read()==b'owned-preflight-loopback-ok'}
finally:server.shutdown();server.server_close()
class UnixServer(socketserver.UnixStreamServer):pass
path='/tmp/domain-preflight.sock';Path(path).unlink(missing_ok=True)
server=UnixServer(path,Handler)
threading.Thread(target=server.serve_forever,daemon=True).start()
try:
    with socket.socket(socket.AF_UNIX,socket.SOCK_STREAM) as client:
        client.settimeout(5);client.connect(path);client.sendall(b'GET / HTTP/1.0\r\nHost: localhost\r\n\r\n')
        chunks=[]
        while True:
            chunk=client.recv(4096)
            if not chunk:break
            chunks.append(chunk)
        results['unix_socket_http']={'ok':b'owned-preflight-loopback-ok' in b''.join(chunks)}
finally:server.shutdown();server.server_close();Path(path).unlink(missing_ok=True)
print(json.dumps(results,sort_keys=True))
'''


def load_daytona_env(path):
    permitted = {'DAYTONA_API_KEY', 'DAYTONA_API_URL', 'DAYTONA_TARGET'}
    for raw in path.read_text().splitlines():
        line = raw.strip()
        if not line or line.startswith('#'):
            continue
        if line.startswith('export '):
            line = line[7:]
        key, sep, value = line.partition('=')
        if not sep:
            raise ValueError('Malformed environment file entry')
        key = key.strip()
        if key not in permitted:
            continue
        value = value.strip()
        if value.startswith(('"', "'")):
            parts = shlex.split(value)
            if len(parts) != 1:
                raise ValueError('Malformed quoted environment value')
            value = parts[0]
        os.environ[key] = value
    if not os.environ.get('DAYTONA_API_KEY'):
        raise ValueError('DAYTONA_API_KEY missing')


def main():
    parser = argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--env-file', type=Path, required=True)
    parser.add_argument('--allowed-domain', action='append', required=True)
    parser.add_argument('--allowed-url', required=True, help='Unauthenticated HTTPS GET endpoint on an allowed host; no query/userinfo')
    parser.add_argument('--receipt', type=Path, required=True)
    args = parser.parse_args()
    domains = normalize_domains(args.allowed_domain)
    parsed = urlsplit(args.allowed_url)
    if parsed.scheme != 'https' or parsed.hostname not in domains or parsed.username or parsed.password or parsed.query or parsed.fragment or parsed.port not in (None, 443):
        parser.error('--allowed-url must be credential-free HTTPS on an exact allowed hostname')
    blocked = {'github': 'https://github.com/', 'raw_github': 'https://raw.githubusercontent.com/',
               'search': 'https://www.bing.com/'}
    if any(urlsplit(url).hostname in domains for url in blocked.values()):
        parser.error('A blocked-test host is present in the allowlist')
    if args.receipt.exists():
        parser.error('Receipt already exists; choose a fresh path to preserve history')
    args.receipt.parent.mkdir(parents=True, exist_ok=True)
    load_daytona_env(args.env_file)
    from daytona import Daytona, CreateSandboxFromImageParams, Image, Resources, ListSandboxesQuery
    client = Daytona()
    owner = str(uuid.uuid4())
    labels = {'micro1.owner': owner, 'micro1.purpose': 'domain-policy-preflight'}
    sandbox_name = 'micro1-network-' + owner[:12]
    receipt = {'owner': owner, 'labels': labels, 'requested_domains': domains,
               'resources': {'cpu': 4, 'memory_gib': 8, 'disk_gib': 10},
               'status': 'not_started', 'stage': 'initialization', 'cleanup': 'not_created'}
    sandbox = None
    returned_created_id = None
    def save():
        args.receipt.write_text(json.dumps(receipt, indent=2) + '\n')
    allowed_urls = {'allowed:' + domain: (args.allowed_url if domain == parsed.hostname else 'https://' + domain + '/') for domain in domains}
    receipt['allowed_urls'] = allowed_urls
    def run_probe():
        urls = {**allowed_urls, **blocked}
        response = sandbox.process.exec('python3 /tmp/domain_probe.py ' + shlex.quote(json.dumps(urls)), timeout=60)
        if response.exit_code:
            raise RuntimeError('Connectivity probe did not finish successfully')
        return json.loads(response.result)
    save()
    try:
        receipt['stage'] = 'create_sandbox'
        save()
        sandbox = client.create(CreateSandboxFromImageParams(
            image=Image.debian_slim('3.12'), resources=Resources(cpu=4, memory=8, disk=10),
            labels=labels, name=sandbox_name,
            auto_stop_interval=15, auto_delete_interval=0, ephemeral=True), timeout=300)
        returned_created_id = sandbox.id
        receipt.update(sandbox_id=sandbox.id, cleanup='pending', status='created')
        save()
        receipt['stage'] = 'verify_ownership_resources'
        save()
        sandbox.refresh_data()
        if sandbox.labels.get('micro1.owner') != owner:
            raise RuntimeError('Created sandbox ownership label mismatch')
        if sandbox.cpu != 4 or sandbox.memory != 8:
            raise RuntimeError('Sandbox resource readback is not exactly 4 CPU / 8 GiB')
        sandbox.fs.upload_file(PROBE.encode(), '/tmp/domain_probe.py')
        receipt['stage'] = 'baseline_probe'
        save()
        receipt['baseline'] = run_probe()
        receipt['stage'] = 'baseline_validation'
        save()
        # Baseline must be reachable to avoid calling an ordinary outage a block.
        if any(receipt['baseline'][name]['kind'] != 'http_response' for name in allowed_urls):
            raise RuntimeError('An allowed HTTPS host is unavailable before policy')
        reachable_controls = [name for name in blocked if receipt['baseline'][name]['kind'] == 'http_response']
        receipt['inconclusive_baseline_controls'] = sorted(set(blocked) - set(reachable_controls))
        if not {'github', 'raw_github'}.issubset(reachable_controls):
            raise RuntimeError('Repository controls unavailable before policy')
        if receipt['baseline']['direct_ip']['kind'] != 'tcp_connected':
            raise RuntimeError('Direct-IP control unavailable before policy')
        receipt['stage'] = 'apply_domain_policy'
        save()
        sandbox.update_network_settings(domain_allow_list=','.join(domains))
        receipt['stage'] = 'policy_readback'
        save()
        sandbox.refresh_data()
        actual = normalize_domains(sandbox.domain_allow_list or [])
        receipt['readback_domains'] = actual
        if actual != domains or sandbox.network_allow_list or sandbox.network_block_all:
            raise RuntimeError('Provider policy readback does not match the requested restriction')
        receipt['stage'] = 'restricted_probe'
        save()
        receipt['restricted'] = run_probe()
        receipt['stage'] = 'restricted_validation'
        checks = receipt['restricted']
        allowed = all(checks[name]['kind'] == 'http_response' and checks[name]['status'] == receipt['baseline'][name]['status'] for name in allowed_urls)
        denied = all(checks[name]['kind'] == 'transport_failure' for name in (*reachable_controls, 'direct_ip'))
        local = checks['loopback_http']['ok'] and checks['unix_socket_http']['ok']
        receipt['checks'] = {'allowed_https': allowed, 'reachable_external_controls_denied': denied, 'tested_external_controls': reachable_controls + ['direct_ip'], 'local_services': local}
        receipt['status'] = 'pass' if allowed and denied and local else 'failed_or_inconclusive'
    except Exception as error:
        receipt['status'] = 'failed'
        receipt['error_type'] = type(error).__name__
        receipt['failed_stage'] = receipt['stage']
        message = str(error)
        for name, value in os.environ.items():
            if value and any(part in name for part in ('KEY', 'TOKEN', 'SECRET', 'PASSWORD')):
                message = message.replace(value, '[redacted]')
        message = re.sub(r'(?i)(bearer\s+|dtn_|sk-)[A-Za-z0-9._-]+', '[redacted]', message)
        receipt['error_message_redacted'] = message[:500]
    finally:
        if sandbox is None and receipt['status'] == 'failed':
            # A create timeout can occur after the provider allocated a sandbox.
            # Recover only the unique name AND ownership label from this attempt.
            try:
                matches = [item for item in client.list(ListSandboxesQuery(labels=labels))
                           if item.name == sandbox_name and item.labels.get('micro1.owner') == owner]
                if len(matches) == 1:
                    sandbox = matches[0]
                    receipt.update(sandbox_id=sandbox.id, cleanup='pending_recovered_create')
                elif matches:
                    receipt['cleanup'] = 'ambiguous_owned_create_requires_followup'
            except Exception as error:
                receipt['cleanup'] = 'create_recovery_failed_requires_followup'
                receipt['cleanup_error_type'] = type(error).__name__
        if sandbox is not None:
            try:
                # Delete only the exact object returned by this create operation;
                # additionally recheck its unique run ownership label.
                if sandbox.id != receipt['sandbox_id']:
                    raise RuntimeError('Cleanup sandbox identity changed')
                # The immutable ID directly returned by our single create call is
                # ownership evidence even if a later metadata refresh fails. A
                # recovered uncertain create additionally requires exact labels.
                if sandbox.id != returned_created_id:
                    sandbox.refresh_data()
                    if sandbox.name != sandbox_name or sandbox.labels.get('micro1.owner') != owner:
                        raise RuntimeError('Recovered cleanup ownership could not be verified')
                receipt['cleanup_stage'] = 'delete_owned_sandbox'
                save()
                sandbox.delete(timeout=60)
                receipt['cleanup'] = 'deleted'
            except Exception as error:
                receipt['cleanup'] = 'failed_requires_followup'
                receipt['cleanup_error_type'] = type(error).__name__
        save()
    print(json.dumps({'status': receipt['status'], 'cleanup': receipt['cleanup'],
                      'sandbox_id': receipt.get('sandbox_id'), 'receipt': str(args.receipt)}))
    return 0 if receipt['status'] == 'pass' and receipt['cleanup'] == 'deleted' else 1


if __name__ == '__main__':
    try:
        sys.exit(main())
    except Exception as error:
        print(json.dumps({'status': 'failed_before_managed_run', 'error_type': type(error).__name__}))
        sys.exit(1)
