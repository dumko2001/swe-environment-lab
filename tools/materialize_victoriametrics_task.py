#!/usr/bin/env python3
"""Materialize the pinned VictoriaMetrics task; --verify checks every output byte."""
import argparse
import hashlib
from pathlib import Path
import shutil

ROOT = Path(__file__).resolve().parent.parent
SOURCE = ROOT / 'task_support/victoriametrics-fill-modifiers'
DEST = ROOT / 'tasks/victoriametrics-fill-modifiers'
AUTHOR_ONLY_FILES = {Path('rubric.md')}

def digest(p):
    with p.open('rb') as f:
        return hashlib.file_digest(f, 'sha256').hexdigest()

def mapping():
    files = {}
    for p in SOURCE.rglob('*'):
        if not p.is_file() or '__pycache__' in p.parts:
            continue
        r = p.relative_to(SOURCE)
        if r.parts[0] in ('calibration', 'source') or r in AUTHOR_ONLY_FILES:
            continue
        files[r] = p
    for prefix in ('environment', 'tests'):
        for name in ('victoriametrics-source.tar.gz', 'SHA256SUMS'):
            files[Path(prefix) / 'source' / name] = SOURCE / 'source' / name
    return files

def main():
    ap = argparse.ArgumentParser()
    ap.add_argument('--verify', action='store_true')
    args = ap.parse_args()
    expected_sha = (SOURCE / 'source/SHA256SUMS').read_text().split()[0]
    assert digest(SOURCE / 'source/victoriametrics-source.tar.gz') == expected_sha
    files = mapping()
    if not args.verify:
        # Remove only known author-only files emitted by older materializations.
        for r in AUTHOR_ONLY_FILES:
            (DEST / r).unlink(missing_ok=True)
        for r, p in files.items():
            target = DEST / r
            target.parent.mkdir(parents=True, exist_ok=True)
            shutil.copy2(p, target)
    actual = {p.relative_to(DEST): p for p in DEST.rglob('*') if p.is_file()}
    assert actual.keys() == files.keys(), 'missing or unexpected materialized files'
    for r, p in files.items():
        assert digest(p) == digest(actual[r]), f'changed: {r}'
    print(f'verified {len(files)} files: {DEST}')

if __name__ == '__main__':
    main()
