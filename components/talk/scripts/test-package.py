#!/usr/bin/env python3
"""Smoke-test the host/plugin contract, config, and an actual offline update."""
import hashlib
import json
import os
from pathlib import Path
import subprocess
import tarfile
import tempfile
import zipfile

root = Path(__file__).resolve().parents[1]
archives = sorted(list((root / 'dist').glob('*.tar.gz')) + list((root / 'dist').glob('*.zip')))
if len(archives) != 1:
    raise SystemExit('smoke test requires exactly one native archive in dist')
a = archives[0]
with tempfile.TemporaryDirectory(dir=root / '.cache') as tmp:
    directory = Path(tmp)
    if a.suffix == '.zip':
        with zipfile.ZipFile(a) as z:
            z.extractall(directory)
    else:
        with tarfile.open(a) as t:
            t.extractall(directory, filter='data')
    suffix = '.exe' if os.name == 'nt' else ''
    host = directory / 'bin' / ('wirectl' + suffix)
    env = dict(os.environ, WIRE_TALK_HOME=str(directory / 'state'))
    def run(*args, check=True):
        return subprocess.run([str(host), 'talk', *args], env=env, text=True, capture_output=True, check=check)
    assert 'devices' in run('--help').stdout
    before = run('version').stdout.strip()
    run('init', '--listen', '127.0.0.1:0')
    state = json.loads((directory / 'state' / 'config.json').read_text())
    assert state['key'] and state['listen'] == '127.0.0.1:0'
    assert run('init', check=False).returncode != 0
    sums = directory / 'SHA256SUMS'
    sums.write_text(f'{hashlib.sha256(a.read_bytes()).hexdigest()}  {a.name}\n')
    run('update', '--archive', str(a), '--checksums', str(sums))
    assert run('version').stdout.strip() == before
    print(f'Package smoke test passed: {a.name}')
