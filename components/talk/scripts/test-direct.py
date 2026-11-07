#!/usr/bin/env python3
"""Real CLI/UDP call lifecycle. Keeps JSON evidence, logs and WAVs in .cache/direct-e2e.

Failure scenarios: audio before consent/after hangup; repeated/released/unrelated
keys; cancel/redial; simultaneous dialing; stale audio; third member; mode
mismatch; peer restart/offline; wrong mode; lost call control; daemon shutdown.
Native audio and handset input use explicitly test-only backends.
"""
import json
import math
import os
from pathlib import Path
import re
import socket
import struct
import subprocess
import threading
import time
import wave

root = Path(__file__).resolve().parents[1]
base = root / '.cache' / 'direct-e2e' / time.strftime('%Y%m%d-%H%M%S')
base.mkdir(parents=True)
env = dict(os.environ)
env['GORACE'] = 'atexit_sleep_ms=0'
for key, folder in [('GOCACHE', 'go-build'), ('GOMODCACHE', 'go-mod'), ('GOTMPDIR', 'tmp'), ('TMPDIR', 'tmp')]:
    path = root / '.cache' / folder
    path.mkdir(parents=True, exist_ok=True)
    env[key] = str(path)
exe = base / ('talk.exe' if os.name == 'nt' else 'talk')
subprocess.run(['go', 'build', '-race', '-tags', 'talk_test_audio', '-o', str(exe), './cmd/wirectl-talk'], cwd=root, env=env, check=True)
processes, handles, results = [], [], []


class LossyLink:
    """A real UDP hop drops and reorders encrypted control and voice packets."""
    def __init__(self, host):
        self.host = (host.rsplit(':', 1)[0], int(host.rsplit(':', 1)[1]))
        self.client = None
        self.sock = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        self.sock.bind(('127.0.0.1', 0))
        self.sock.settimeout(.02)
        self.address = '127.0.0.1:' + str(self.sock.getsockname()[1])
        self.stop = threading.Event()
        self.count = self.dropped = self.reordered = 0
        self.duplicates = 0
        self.seen = set()
        self.thread = threading.Thread(target=self.run, daemon=True)
        self.thread.start()

    def run(self):
        pending = []
        while not self.stop.is_set():
            now = time.monotonic()
            ready = [p for p in pending if p[0] <= now]
            pending = [p for p in pending if p[0] > now]
            for _, data, target in ready:
                self.sock.sendto(data, target)
            try:
                data, sender = self.sock.recvfrom(2048)
            except (socket.timeout, ConnectionResetError):
                # Windows reports ICMP port-unreachable when the fixture peer
                # stops. Keep the UDP hop alive so its restarted peer can return.
                continue
            if sender == self.host:
                target = self.client
            else:
                self.client = sender
                target = self.host
            if target is None:
                continue
            signature = (sender, data)
            if signature in self.seen:
                self.duplicates += 1
            self.seen.add(signature)
            self.count += 1
            if self.count % 5 == 0:
                self.dropped += 1
                continue
            if self.count % 7 == 0:
                self.reordered += 1
                pending.append((now + .32, data, target))
            else:
                self.sock.sendto(data, target)

    def close(self):
        self.stop.set()
        self.thread.join(timeout=2)
        self.sock.close()
        (base / 'network.json').write_text(json.dumps({'datagrams': self.count, 'dropped': self.dropped, 'reordered': self.reordered, 'duplicates': self.duplicates}, indent=2))


link = None


def cli(node, *args, check=True):
    p = subprocess.run([str(exe), '--state-dir', str(base / node), *args], env=env, capture_output=True, text=True, timeout=15)
    if check and p.returncode:
        raise AssertionError((node, args, p.stdout, p.stderr))
    return p


def status(node):
    return json.loads(cli(node, 'status', '--json').stdout)


def wait(predicate, label, seconds=10):
    until = time.monotonic() + seconds
    while time.monotonic() < until:
        try:
            if predicate():
                return
        except (AssertionError, json.JSONDecodeError):
            pass
        time.sleep(.06)
    raise AssertionError(label)


def evidence(label):
    states = {node: status(node) for node in ('a', 'b')}
    results.append({'scenario': label, 'passed': True, 'snapshots': states})
    (base / 'results.json').write_text(json.dumps(results, indent=2))
    print('PASS', label, flush=True)


def start(node):
    events = base / (node + '-keys.jsonl')
    events.touch(exist_ok=True)
    log = (base / (node + '-' + str(len(processes)) + '.log')).open('w')
    handles.append(log)
    p = subprocess.Popen([str(exe), '--state-dir', str(base / node), 'join'], env=dict(env, WIRE_TALK_TEST_HANDSET_EVENTS=str(events)), stdout=log, stderr=log)
    processes.append(p)
    wait(lambda: cli(node, 'status', '--json', check=False).returncode == 0, node + ' startup')
    return p


def key(node, usage=205, value=1):
    with (base / (node + '-keys.jsonl')).open('a') as f:
        f.write(json.dumps({'usage_page': 12, 'usage': usage, 'value': value}) + '\n')


def button(node):
    key(node)
    key(node, value=0)


def states(a, b):
    return status('a')['call']['state'] == a and status('b')['call']['state'] == b


def idle_no_voice():
    before = {n: status(n) for n in ('a', 'b')}
    time.sleep(.5)
    for n in before:
        s = status(n)
        assert s['sent_frames'] == before[n]['sent_frames'], (n, 'idle sent audio')
        assert s['received_frames'] == before[n]['received_frames'], (n, 'idle received audio')


def recording(label, seconds=.6):
    path = base / (label + '.wav')
    cli('b', 'record', 'start', str(path))
    time.sleep(seconds)
    cli('b', 'record', 'stop')
    with wave.open(str(path), 'rb') as f:
        values = struct.unpack('<' + 'h' * f.getnframes(), f.readframes(f.getnframes()))
    return math.sqrt(sum(v*v for v in values) / max(1, len(values)))


try:
    # The test is written before the implementation: this must reject the old CLI.
    cli('a', 'init', '--mode', 'direct', '--listen', '127.0.0.1:0')
    cli('bad', 'init', '--mode', 'invalid', check=False)
    assert not (base / 'bad' / 'config.json').exists()
    start('a')
    address = status('a')['listen']
    # Pairing must carry mode automatically, without a second --mode flag.
    # Use the runtime-selected UDP port for the invitation's TCP listener.
    conf = json.loads((base / 'a' / 'config.json').read_text())
    conf['listen'] = address
    (base / 'a' / 'config.json').write_text(json.dumps(conf))
    invitation_log = base / 'invite.log'
    log = invitation_log.open('w'); handles.append(log)
    invitation = subprocess.Popen([str(exe), '--state-dir', str(base / 'a'), 'invite'], env=env, stdout=log, stderr=log)
    processes.append(invitation)
    wait(lambda: re.search(r'Pairing code: (\d{6})', invitation_log.read_text()), 'invitation')
    code = re.search(r'Pairing code: (\d{6})', invitation_log.read_text())[1]
    cli('b', 'pair', address, '--code', code, '--listen', '127.0.0.1:0')
    assert invitation.wait(timeout=5) == 0
    assert json.loads((base / 'b' / 'config.json').read_text())['mode'] == 'direct'
    link = LossyLink(address)
    guest = json.loads((base / 'b' / 'config.json').read_text())
    guest['peers'] = [link.address]
    (base / 'b' / 'config.json').write_text(json.dumps(guest))
    start('b')
    wait(lambda: len(status('a')['peers']) == len(status('b')['peers']) == 1, 'peer discovery')
    assert states('idle', 'idle')
    idle_no_voice()
    assert recording('idle') == 0
    assert link.duplicates == 0, 'IPv4 seed and discovered peer sent duplicate datagrams'
    assert json.loads(cli('a', 'call', 'status', '--json').stdout)['state'] == 'idle'
    assert cli('a', 'call', 'answer', check=False).returncode != 0
    assert cli('a', 'call', 'hangup', check=False).returncode != 0
    evidence('pairing inherits direct mode; idle heartbeat without voice')

    # Unrelated keys and key-up cannot dial; a held/repeated press cannot answer
    # or immediately hang up the same call.
    key('a', usage=99); key('a', usage=99, value=0); key('a', value=0)
    time.sleep(.2)
    assert states('idle', 'idle')
    key('a'); key('a'); key('a')
    wait(lambda: states('dialing', 'ringing'), 'dial/ring')
    time.sleep(.4)
    assert states('dialing', 'ringing')
    wait(lambda: status('b')['output_device']['level']['peak'] > 0, 'no local ringtone')
    idle_no_voice()
    assert recording('ringing') == 0, 'ringtone leaked into received recording'
    key('a', value=0)
    evidence('functional key dials once; remote rings; no pre-answer audio')

    button('b')
    wait(lambda: states('active', 'active'), 'answer')
    wait(lambda: status('a')['received_frames'] > 3 and status('b')['received_frames'] > 3, 'duplex voice')
    tone = base / 'tone.wav'
    with wave.open(str(tone), 'wb') as f:
        f.setparams((1, 2, 16000, 0, 'NONE', 'not compressed'))
        f.writeframes(b''.join(struct.pack('<h', int(5000*math.sin(2*math.pi*440*i/16000))) for i in range(16000)))
    cli('a', 'input', 'start', str(tone), '--loop')
    assert recording('active') > 500, 'accepted call did not carry audible PCM'
    # Software playback gain is bounded and persistent; release does nothing.
    key('b', usage=233); key('b', usage=233, value=0)
    wait(lambda: status('b')['output_gain_db'] == 2, 'volume up')
    key('b', usage=234); key('b', usage=234, value=0)
    wait(lambda: status('b')['output_gain_db'] == 0, 'volume down')
    evidence('answer starts duplex audio; handset volume keys adjust playback')

    button('a')
    wait(lambda: states('idle', 'idle'), 'hangup')
    time.sleep(.2)
    idle_no_voice()
    assert recording('hung-up') == 0, 'queued speech replayed after hangup'
    evidence('hangup immediately gates audio and clears queued speech')
    button('b')
    wait(lambda: states('ringing', 'dialing'), 'reverse dial')
    button('b')
    wait(lambda: states('idle', 'idle'), 'cancel dialing')
    button('a')
    wait(lambda: states('dialing', 'ringing'), 'redial')
    button('b')
    wait(lambda: states('active', 'active'), 'second answer')
    button('b')
    wait(lambda: states('idle', 'idle'), 'callee hangs up')
    evidence('both ends can dial, cancel and hang up; redial requires fresh answer')

    # Reordering/loss across simultaneous presses must converge without opening
    # either microphone until one side explicitly answers the selected call.
    button('a'); button('b')
    wait(lambda: states('dialing', 'ringing') or states('ringing', 'dialing') or states('active', 'active'), 'simultaneous dial convergence')
    if not states('active', 'active'):
        receiver = 'b' if status('b')['call']['state'] == 'ringing' else 'a'
        idle_no_voice()
        button(receiver)
        wait(lambda: states('active', 'active'), 'answer simultaneous call')
    cli('b', 'daemon', 'stop')
    wait(lambda: status('a')['call']['state'] == 'idle', 'offline hangup', seconds=10)
    start('b')
    wait(lambda: len(status('a')['peers']) == 1 and states('idle', 'idle'), 'restart returns to idle')
    idle_no_voice()
    evidence('simultaneous dialing converges; disconnect and restart never auto-answer')

    # A third authenticated process must not enter the established 1:1 link.
    cli('third', 'init', '--mode', 'direct', '--listen', '127.0.0.1:0')
    third = json.loads((base / 'third' / 'config.json').read_text())
    third.update(key=conf['key'], peers=[address])
    (base / 'third' / 'config.json').write_text(json.dumps(third))
    start('third'); time.sleep(1.3)
    assert len(status('a')['peers']) == 1
    assert cli('a', 'group', 'mode', 'default', 'room', check=False).returncode != 0, 'mode changed in live session'
    evidence('third member excluded; live mode changes rejected')
    cli('third', 'daemon', 'stop')
    cli('third', 'group', 'mode', 'default', 'room')
    start('third'); time.sleep(1.2)
    assert states('idle', 'idle')
    idle_no_voice()
    evidence('room-mode peer cannot open direct microphones')
    # A missed call must return to standby, including when control packets drop.
    button('a')
    wait(lambda: states('dialing', 'ringing'), 'timeout fixture dial')
    wait(lambda: states('idle', 'idle'), 'unanswered call did not time out', seconds=48)
    idle_no_voice()
    evidence('unanswered call times out without sending audio')
    # Offline mode migration is available for existing paired configurations.
    cli('a', 'daemon', 'stop'); cli('b', 'daemon', 'stop')
    cli('a', 'group', 'mode', 'default', 'room')
    assert json.loads((base / 'a' / 'config.json').read_text())['mode'] == 'room'
    cli('a', 'group', 'mode', 'default', 'direct')
    assert json.loads((base / 'a' / 'config.json').read_text())['mode'] == 'direct'
    assert json.loads(cli('a', 'group', 'status', '--json').stdout)['mode'] == 'direct'
    assert link.dropped > 0 and link.reordered > 0
    print('Artifacts:', base, flush=True)
finally:
    for node in ('a', 'b', 'third'):
        cli(node, 'daemon', 'stop', check=False)
    for p in processes:
        if p.poll() is None:
            p.kill(); p.wait(timeout=5)
    for h in handles:
        h.close()
    if link:
        link.close()
    for path in base.glob('*.log'):
        assert 'DATA RACE' not in path.read_text(), str(path)
