#!/usr/bin/env python3
"""Validate the collected snapshot without executing candidate-controlled code."""
import os
import hashlib
import json
from pathlib import Path
import shutil
import sys
import tarfile

out = Path('/logs/verifier')
archive = Path('/app/verifier-artifacts/candidate.tar.gz')
checksum = archive.with_name('candidate.sha256')

def reject(reason, infrastructure=False):
    (out / 'status.json').write_text(json.dumps({'phase': 'prepare', 'status': 'infrastructure_error' if infrastructure else 'invalid_candidate', 'reason': reason}))
    sys.exit(20 if infrastructure else 10)

if not all(os.access(p, os.X_OK) for p in ('/usr/local/go/bin/go',)):
    reject('trusted executable unavailable', True)

if not archive.is_file() or not checksum.is_file():
    reject('missing collected artifact', True)
try:
    expected = checksum.read_text().split()[0]
    if hashlib.file_digest(archive.open('rb'), 'sha256').hexdigest() != expected:
        reject('checksum mismatch')
    with tarfile.open(archive) as tf:
        members = tf.getmembers()
        if len(members) > 40000 or sum(m.size for m in members) > 600 * 1024**2:
            reject('archive exceeds source budget')
        seen = set()
        for m in members:
            p = Path(m.name)
            if p.is_absolute() or '..' in p.parts or not p.parts or p.parts[0] != 'victoriametrics' or not (m.isfile() or m.isdir()):
                reject('unsafe archive member')
            if str(p) in seen:
                reject('duplicate archive member')
            seen.add(str(p))
        dst = Path('/tmp/vm-candidate')
        shutil.rmtree(dst, ignore_errors=True)
        dst.mkdir()
        # All links and special entries are rejected before extraction.
        tf.extractall(dst)
    candidate = dst / 'victoriametrics'
    reference = Path('/opt/reference/source')
except (OSError, ValueError, tarfile.TarError) as exc:
    reject(type(exc).__name__)
(out / 'status.json').write_text(json.dumps({'phase': 'prepare', 'status': 'ready'}))
