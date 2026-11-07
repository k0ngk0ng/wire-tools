#!/usr/bin/env python3
"""Native user service lifecycle, only on disposable GitHub Actions runners.
The null-driver test binary must already have been built by test-session.py.
"""
import json
import os
from pathlib import Path
import subprocess
import sys
import tempfile
import time

if os.environ.get('GITHUB_ACTIONS') != 'true':
    raise SystemExit('Native service tests run only in disposable GitHub Actions runners')
root = Path(__file__).resolve().parents[1]
if sys.platform == 'darwin':
    probe = ['launchctl', 'print', f'gui/{os.getuid()}']
elif sys.platform.startswith('linux'):
    probe = ['systemctl', '--user', 'show-environment']
else:
    probe = ['schtasks', '/Query']
if subprocess.run(probe, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL).returncode:
    print('SKIP native service: this runner has no logged-in user service manager')
    raise SystemExit(0)
exe = root / '.cache' / ('talk-test.exe' if os.name == 'nt' else 'talk-test')
assert exe.is_file(), 'run test-session.py first'
with tempfile.TemporaryDirectory(dir=root / '.cache') as directory:
    state = Path(directory) / 'state'
    env = dict(os.environ, WIRE_TALK_HOME=str(state))
    def command(*args, check=True):
        r = subprocess.run([str(exe), *args], env=env, text=True, capture_output=True, timeout=45)
        if check and r.returncode:
            raise RuntimeError(f'{args}: {r.stdout} {r.stderr}')
        return r
    command('init', '--listen', '127.0.0.1:0')
    try:
        command('daemon', 'install')
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            status = command('status', '--json', check=False)
            if status.returncode == 0:
                assert json.loads(status.stdout)['input']
                break
            time.sleep(.2)
        else:
            log = state / 'daemon.log'
            raise AssertionError('Native service failed to become online: ' + (log.read_text() if log.exists() else 'no worker log'))
        command('daemon', 'stop')
        assert command('status', '--json', check=False).returncode != 0
        print('Native service registration, audio startup and stop passed')
    finally:
        command('daemon', 'stop', check=False)
