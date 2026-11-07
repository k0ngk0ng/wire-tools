#!/usr/bin/env python3
"""Install the reviewed sync workflow into the owner's existing package repos.
Uses the current gh login; no credentials are copied to GitHub Actions secrets.
"""
import argparse
import time
import base64
import json
from pathlib import Path
import subprocess

root = Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser()
p.add_argument('--repository', choices=['homebrew-tap', 'scoop-bucket'])
options = p.parse_args()
def api(endpoint, data=None, method=None):
    args = ['gh', 'api', endpoint]
    if method:
        args += ['--method', method]
    if data is not None:
        args += ['--input', '-']
    for attempt in range(3):
        r = subprocess.run(args, input=json.dumps(data) if data is not None else None, text=True, capture_output=True)
        if r.returncode == 0:
            return json.loads(r.stdout)
        if attempt == 2 or not any(word in r.stderr.lower() for word in ['timeout', 'timed out', '502', '503', '504', 'connection reset']):
            raise SystemExit(r.stderr)
        time.sleep(2)
for repo, kind in [('homebrew-tap', 'brew'), ('scoop-bucket', 'scoop')]:
    if options.repository and options.repository != repo:
        continue
    prefix = f'repos/k0ngk0ng/{repo}/git/'
    base = api(prefix + 'ref/heads/main')['object']['sha']
    tree = api(prefix + 'commits/' + base)['tree']['sha']
    entries = []
    for local, remote in [('packaging/sync-talk.py', 'scripts/sync-talk.py'), (f'packaging/sync-talk-{kind}.yml', '.github/workflows/sync-talk.yml')]:
        data = (root / local).read_bytes()
        blob = api(prefix + 'blobs', dict(content=base64.b64encode(data).decode(), encoding='base64'))['sha']
        entries.append(dict(path=remote, mode='100644', type='blob', sha=blob))
    new_tree = api(prefix + 'trees', dict(base_tree=tree, tree=entries))['sha']
    if new_tree == tree:
        print(f'{repo}: sync workflow already current')
        continue
    commit = api(prefix + 'commits', dict(message='Add verified talk release synchronization', tree=new_tree, parents=[base]))['sha']
    api(prefix + 'refs/heads/main', dict(sha=commit, force=False), 'PATCH')
    print(f'{repo}: {commit}')
