#!/usr/bin/env python3
"""Interactive macOS E2E: real AB17X controls/microphone and a loopback peer.

Use after make build. Dial with the handset, then try volume keys and speak.
The simulated peer answers automatically. Hang up with the handset; it then
calls back, allowing a real incoming ringtone/answer/hangup check. All state,
logs and received microphone WAVs stay under .cache/handset-e2e.
"""
import json
import math
import os
from pathlib import Path
import struct
import subprocess
import sys
import time
import wave

root = Path(__file__).resolve().parents[1]
base = root / '.cache' / 'handset-e2e' / time.strftime('%Y%m%d-%H%M%S')
base.mkdir(parents=True)
env = dict(os.environ, GORACE='atexit_sleep_ms=0')
for key, folder in [('GOCACHE', 'go-build'), ('GOMODCACHE', 'go-mod'), ('GOTMPDIR', 'tmp'), ('TMPDIR', 'tmp')]:
    path = root / '.cache' / folder
    path.mkdir(parents=True, exist_ok=True)
    env[key] = str(path)
native = root / 'bin' / 'wirectl-talk'
remote = base / 'remote-talk'
processes, logs = [], []
history = []


def cli(node, *args, check=True):
    exe = native if node == 'native' else remote
    r = subprocess.run([str(exe), '--state-dir', str(base / node), *args], env=env, capture_output=True, text=True, timeout=10)
    if check and r.returncode:
        raise AssertionError((node, args, r.stdout, r.stderr))
    return r


def status(node):
    r = cli(node, 'status', '--json', check=False)
    return json.loads(r.stdout) if r.returncode == 0 else {}


def start(node):
    log = (base / (node + '.log')).open('w'); logs.append(log)
    p = subprocess.Popen([str(native if node == 'native' else remote), '--state-dir', str(base / node), 'join'], env=env, stdout=log, stderr=log)
    processes.append(p)
    until = time.monotonic() + 10
    while time.monotonic() < until:
        s = status(node)
        if s:
            return s
        if p.poll() is not None:
            raise AssertionError((node, (base / (node + '.log')).read_text()))
        time.sleep(.1)
    raise AssertionError(node + ' did not start')


def event(kind, **details):
    row = {'event': kind, 'time': time.time(), **details}
    history.append(row)
    (base / 'results.json').write_text(json.dumps(history, indent=2))
    print(json.dumps({k: v for k, v in row.items() if k not in ('native', 'remote', 'snapshot')}), flush=True)


assert sys.platform == 'darwin', 'real handset test requires macOS'
devices = json.loads(subprocess.run([str(native), 'devices', '--json'], env=env, capture_output=True, text=True, check=True).stdout)
microphone = next(d['id'] for d in devices if d['kind'] == 'input' and d['name'] == 'AB17X USB Audio')
speaker = next(d['id'] for d in devices if d['kind'] == 'output' and d['name'] == 'Mac mini Speakers')
subprocess.run(['go', 'build', '-tags', 'talk_test_audio', '-o', str(remote), './cmd/wirectl-talk'], cwd=root, env=env, check=True)
try:
    cli('native', 'init', '--mode', 'direct', '--listen', '127.0.0.1:0', '--input', microphone, '--output', speaker)
    first = start('native')
    cli('remote', 'init', '--mode', 'direct', '--listen', '127.0.0.1:0', '--input', 'none')
    conf = json.loads((base / 'remote' / 'config.json').read_text())
    conf.update(key=json.loads((base / 'native' / 'config.json').read_text())['key'], peers=[first['listen']])
    (base / 'remote' / 'config.json').write_text(json.dumps(conf))
    start('remote')
    ready_deadline = time.monotonic() + 12
    while time.monotonic() < ready_deadline:
        s = status('native')
        if s.get('handset', {}).get('online') and s['input_device']['online'] and len(s['peers']) == 1:
            break
        time.sleep(.2)
    else:
        event('not-ready', snapshot=status('native'))
        raise AssertionError('native microphone/handset not ready')
    event('ready', profile=str(base / 'native'), remote_profile=str(base / 'remote'), snapshot=s)
    recording = base / 'microphone.wav'
    stage, previous, active_since, ring_since = 0, None, None, None
    deadline = time.monotonic() + 240
    while time.monotonic() < deadline:
        a, b = status('native'), status('remote')
        view = (a['call']['state'], b['call']['state'], a['output_gain_db'])
        if view != previous:
            event('state', states=view, native=a, remote=b)
            previous = view
        now = time.monotonic()
        if stage == 0 and b['call']['state'] == 'ringing':
            if ring_since is None:
                ring_since = now
            if now - ring_since >= 2:
                cli('remote', 'record', 'start', str(recording))
                cli('remote', 'call', 'answer')
                stage, active_since = 1, now
        elif stage == 1 and a['call']['state'] == b['call']['state'] == 'idle':
            cli('remote', 'record', 'stop')
            event('outgoing-hangup', native=a, remote=b)
            stage, active_since = 2, now
        elif stage == 2 and now - active_since > 2:
            cli('remote', 'call', 'dial')
            stage = 3
            event('incoming-ringing')
        elif stage == 3 and a['call']['state'] == b['call']['state'] == 'active':
            stage, active_since = 4, now
            event('incoming-answered')
        elif stage == 4 and a['call']['state'] == b['call']['state'] == 'idle':
            event('incoming-hangup')
            break
        time.sleep(.1)
    else:
        event('timeout', stage=stage)
        raise AssertionError('interactive handset lifecycle was not completed')
    if recording.exists():
        cli('remote', 'record', 'stop', check=False)
        with wave.open(str(recording), 'rb') as f:
            values = struct.unpack('<' + 'h' * f.getnframes(), f.readframes(f.getnframes()))
        rms = math.sqrt(sum(v*v for v in values) / max(1, len(values)))
        event('microphone-recording', path=str(recording), rms=rms, peak=max((abs(v) for v in values), default=0))
        assert rms > 1, 'no microphone audio reached the simulated peer'
    event('complete', artifacts=str(base))
finally:
    for node in ('native', 'remote'):
        cli(node, 'daemon', 'stop', check=False)
    for p in processes:
        if p.poll() is None:
            p.kill(); p.wait(timeout=5)
    for log in logs:
        log.close()
