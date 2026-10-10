#!/usr/bin/env python3
"""Inspect the built actor before any reference solution or private test upload."""
import hashlib
import json
import os
from pathlib import Path
import tarfile


def main():
    roots = [Path('/app'), Path('/opt/prometheus-input'), Path('/opt/input'),
             Path('/opt/task-runtime'), Path('/opt/task-env'), Path('/opt/task-environment')]
    forbidden = []
    archives = []
    guides = []
    for private in ('/solution', '/tests'):
        if Path(private).exists():
            forbidden.append(private)
    for root in roots:
        if not root.exists():
            continue
        for current, dirs, files in os.walk(root, followlinks=False):
            if '.git' in dirs:
                forbidden.append(str(Path(current)/'.git'))
                dirs.remove('.git')
            for name in files:
                path = Path(current)/name
                if name in ('SOURCE.md', 'oracle.patch', 'reference.patch', 'solution.patch'):
                    forbidden.append(str(path))
                if name.endswith('.tar.gz') and ('input' in str(root)):
                    with tarfile.open(path) as archive:
                        members = archive.getmembers()
                        vcs = [m.name for m in members if '.git' in Path(m.name).parts]
                        forbidden.extend(vcs)
                        archives.append({'path': str(path), 'members': len(members), 'vcs_entries': vcs})
                if name in ('ENVIRONMENT.md', 'STORAGE.md'):
                    guides.append({'path': str(path), 'sha256': hashlib.sha256(path.read_bytes()).hexdigest()})
    result = {'uid': os.getuid(), 'forbidden_paths': forbidden, 'source_archives': archives,
              'guides': guides, 'scope': 'Private uploads, VCS history and provenance markers; semantic hint audit is separate.'}
    print(json.dumps(result, indent=2))
    if forbidden:
        raise SystemExit('Unexpected private/reference/VCS material in actor image')


if __name__ == '__main__':
    main()
