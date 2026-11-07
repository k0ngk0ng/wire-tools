#!/usr/bin/env python3
"""Exercise real foreground/background process lifecycles with a null audio driver.
The test-only binary is built under .cache and is never used by package.py.
"""
import json
import os
import re
from pathlib import Path
import signal
import socket
import subprocess
import tempfile
import time

root = Path(__file__).resolve().parents[1]
env = dict(os.environ)
for var, folder in [('GOCACHE', 'go-build'), ('GOMODCACHE', 'go-mod'), ('GOTMPDIR', 'tmp'), ('TMPDIR', 'tmp')]:
    dest = root / '.cache' / folder
    dest.mkdir(parents=True, exist_ok=True)
    env[var] = str(dest)
exe = root / '.cache' / ('talk-test.exe' if os.name == 'nt' else 'talk-test')
subprocess.run(['go', 'build', '-tags', 'talk_test_audio', '-o', str(exe), './cmd/wirectl-talk'], env=env, cwd=root, check=True)
with tempfile.TemporaryDirectory(dir=root / '.cache') as directory:
    state = Path(directory) / 'state'
    env['WIRE_TALK_HOME'] = str(state)
    def command(*args, check=True):
        return subprocess.run([str(exe), *args], env=env, text=True, capture_output=True, timeout=25, check=check)
    def wait_online():
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            r = command('status', '--json', check=False)
            if r.returncode == 0:
                return json.loads(r.stdout)
            time.sleep(.05)
        raise AssertionError('session never became online')
    command('init', '--listen', '127.0.0.1:0')
    foreground = subprocess.Popen([str(exe), 'join'], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE, creationflags=subprocess.CREATE_NEW_PROCESS_GROUP if os.name == 'nt' else 0)
    try:
        assert wait_online()['peers'] == []
        assert 'State:' in command('status').stdout
        assert json.loads(command('daemon', 'status', '--json').stdout)['muted'] is False
        assert 'Usage:' in command('mute', '--help').stdout
        assert command('mute', 'unexpected', check=False).returncode != 0
        assert json.loads(command('status', '--json').stdout)['muted'] is False
        assert command('join', check=False).returncode != 0
        assert command('daemon', 'start', check=False).returncode != 0
        command('mute')
        assert json.loads(command('status', '--json').stdout)['muted'] is True
        command('unmute')
        assert json.loads(command('status', '--json').stdout)['muted'] is False
        watcher = subprocess.Popen([str(exe), 'watch'], env=env, stdout=subprocess.DEVNULL, stderr=subprocess.DEVNULL)
        time.sleep(.15)
        watcher.terminate()
        watcher.wait(timeout=5)
        assert command('status', '--json').returncode == 0
        json_watcher = subprocess.Popen([str(exe), 'watch', '--json'], env=env, stdout=subprocess.PIPE, stderr=subprocess.PIPE)
        time.sleep(1.2)
        json_watcher.terminate()
        json_out, _ = json_watcher.communicate(timeout=5)
        snapshots = [json.loads(line) for line in json_out.splitlines()]
        assert snapshots and all('sent_frames' in s for s in snapshots)
        assert command('status', '--json').returncode == 0
        # Console-free Windows CI cannot reliably deliver Ctrl+C. Its control
        # stop path is tested; Unix additionally proves SIGINT lifecycle cleanup.
        if os.name == 'nt':
            command('daemon', 'stop')
        else:
            foreground.send_signal(signal.SIGINT)
        out, err = foreground.communicate(timeout=5)
        assert foreground.returncode == 0, (out, err)
        assert command('status', '--json', check=False).returncode != 0
        command('daemon', 'start')
        wait_online()
        assert command('join', check=False).returncode != 0
        command('daemon', 'stop')
        assert command('status', '--json', check=False).returncode != 0
        # Start again proves all audio, UDP and process ownership was released.
        command('daemon', 'start')
        command('daemon', 'stop')
        # Native service worker must recover from unavailable resources instead
        # of exhausting a platform service manager's finite restart counter.
        blocker = socket.socket(socket.AF_INET, socket.SOCK_DGRAM)
        blocker.bind(('127.0.0.1', 0))
        config_path = state / 'config.json'
        config = json.loads(config_path.read_text())
        config['listen'] = '127.0.0.1:' + str(blocker.getsockname()[1])
        config_path.write_text(json.dumps(config))
        worker = subprocess.Popen([str(exe), '__serve'], env=env)
        try:
            time.sleep(.3)
            assert worker.poll() is None
            blocker.close()
            wait_online()
            command('daemon', 'stop')
            worker.wait(timeout=5)
            assert worker.returncode == 0
        finally:
            blocker.close()
            if worker.poll() is None:
                worker.kill()
                worker.wait()
        print('Foreground/background/mute/watch/stop/restart/resource recovery lifecycle passed')
    finally:
        command('daemon', 'stop', check=False)
        if foreground.poll() is None:
            foreground.kill()
            foreground.wait()

# Pairing uses real TCP while the existing audio room continues over real UDP.
# All profiles, subprocess logs and the null-driver binary remain in .cache.
with tempfile.TemporaryDirectory(dir=root / '.cache') as directory:
    base = Path(directory)
    host_state, guest_state, third_state = (base / name for name in ('host', 'guest', 'third'))
    def call(state, *args, check=True):
        return subprocess.run([str(exe), '--state-dir', str(state), *args], env=env,
                              text=True, capture_output=True, timeout=25, check=check)
    def online(state, minimum_peers=0):
        deadline = time.monotonic() + 8
        while time.monotonic() < deadline:
            result = call(state, 'status', '--json', check=False)
            if result.returncode == 0:
                status = json.loads(result.stdout)
                if len(status['peers']) >= minimum_peers and (minimum_peers == 0 or status['received_frames'] > 0):
                    return status
            time.sleep(.05)
        raise AssertionError(f'{state.name} did not become online with {minimum_peers} peers')
    # Reserve a number available to both protocols before starting the host.
    with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as tcp, socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as udp:
        tcp.bind(('127.0.0.1', 0))
        port = tcp.getsockname()[1]
        udp.bind(('127.0.0.1', port))
    address = f'127.0.0.1:{port}'
    call(host_state, 'init', '--listen', address)
    processes = []
    logs = []
    def invite(number):
        log_path = base / f'invite-{number}.txt'
        log = log_path.open('w')
        logs.append(log)
        proc = subprocess.Popen([str(exe), '--state-dir', str(host_state), 'invite'],
                                env=env, stdout=log, stderr=log)
        processes.append(proc)
        deadline = time.monotonic() + 5
        while time.monotonic() < deadline:
            match = re.search(r'Pairing code: ([0-9]{6})', log_path.read_text())
            if match:
                return proc, match[1]
            assert proc.poll() is None, log_path.read_text()
            time.sleep(.02)
        raise AssertionError('no invitation code')
    try:
        call(host_state, 'daemon', 'start')
        first, code = invite(1)
        # Neither concurrent invitations nor overwriting an existing profile
        # may consume the valid code or interrupt the ongoing audio session.
        assert call(host_state, 'invite', check=False).returncode != 0
        assert call(host_state, 'pair', address, '--code', code, check=False).returncode != 0
        wrong = '000000' if code != '000000' else '000001'
        assert call(guest_state, 'pair', address, '--code', wrong, check=False).returncode != 0
        assert not (guest_state / 'config.json').exists()
        call(guest_state, 'pair', address, '--code', code, '--listen', '127.0.0.1:0', '--headphones')
        assert first.wait(timeout=5) == 0
        host_config = json.loads((host_state / 'config.json').read_text())
        guest_config = json.loads((guest_state / 'config.json').read_text())
        assert guest_config['key'] == host_config['key']
        assert guest_config['peers'] == [address] and guest_config['headphones'] is True
        assert call(guest_state, 'status', check=False).returncode != 0  # pair alone opens no audio
        assert call(third_state, 'pair', address, '--code', code, check=False).returncode != 0
        assert not (third_state / 'config.json').exists()
        call(guest_state, 'daemon', 'start')
        online(host_state, 1)
        online(guest_state, 1)
        call(guest_state, 'daemon', 'stop')
        # Saved room/address survive restart: no code or invitation needed.
        call(guest_state, 'daemon', 'start')
        online(guest_state, 1)
        second, next_code = invite(2)
        third_log = (base / 'third.txt').open('w')
        logs.append(third_log)
        third = subprocess.Popen([str(exe), '--state-dir', str(third_state), 'join',
                                  address, '--code', next_code, '--listen', '127.0.0.1:0'],
                                 env=env, stdout=third_log, stderr=third_log)
        processes.append(third)
        assert second.wait(timeout=5) == 0
        online(third_state, 2)
        online(host_state, 2)
        call(third_state, 'daemon', 'stop')
        assert third.wait(timeout=5) == 0
        print('Numeric pairing/wrong code/single use/saved restart/three-member audio lifecycle passed')
    finally:
        for state in (host_state, guest_state, third_state):
            call(state, 'daemon', 'stop', check=False)
        for proc in processes:
            if proc.poll() is None:
                proc.kill()
                proc.wait(timeout=5)
        for log in logs:
            log.close()

# A native backend can hang in ma_device_uninit after control.json is removed.
# Exercise process exit, recording finalization, and lock release even in that
# state. The injected hang exists only in the talk_test_audio build.
import wave
with tempfile.TemporaryDirectory(dir=root / '.cache') as directory:
    for mode in ('join', '__serve'):
        profile=Path(directory)/mode
        def shutdown_call(*args, check=True):
            return subprocess.run([str(exe),'--state-dir',str(profile),*args],env=env,text=True,capture_output=True,timeout=20,check=check)
        shutdown_call('init','--listen','127.0.0.1:0')
        stuck_env=dict(env,WIRE_TALK_TEST_HANG_CLOSE='1')
        process=subprocess.Popen([str(exe),'--state-dir',str(profile),mode],env=stuck_env,stdout=subprocess.PIPE,stderr=subprocess.PIPE)
        try:
            deadline=time.monotonic()+8
            while time.monotonic()<deadline:
                if shutdown_call('status','--json',check=False).returncode==0:break
                time.sleep(.05)
            else:raise AssertionError('shutdown fixture never became online')
            recording=Path(directory)/(mode+'.wav')
            shutdown_call('record','start',str(recording))
            time.sleep(.15)
            shutdown_call('daemon','stop')
            process.communicate(timeout=8)
            assert process.returncode==1,f'{mode}: stuck native driver must terminate with failure, got {process.returncode}'
            assert not (profile/'control.json').exists()
            with wave.open(str(recording),'rb') as f:
                assert f.getnframes()>0,'recording not finalized after shutdown timeout'
            shutdown_call('daemon','start')
            shutdown_call('daemon','stop')
        finally:
            if process.poll() is None:process.kill();process.wait(timeout=5)
    print('Native shutdown hang: foreground/worker exit, WAV finalization and lock recovery passed')

# Disconnect each native direction while real UDP peers remain connected.
# Device availability is injected only into the test-tag build.
with tempfile.TemporaryDirectory(dir=root / '.cache') as directory:
    base = Path(directory)
    a, b = base / 'a', base / 'b'
    missing_in, missing_out = base / 'input.offline', base / 'output.offline'
    recovery_env = dict(env, WIRE_TALK_TEST_INPUT_OFFLINE=str(missing_in),
                        WIRE_TALK_TEST_OUTPUT_OFFLINE=str(missing_out))
    def call_device(state, *args, check=True):
        return subprocess.run([str(exe), '--state-dir', str(state), *args], env=env,
                              capture_output=True, text=True, timeout=20, check=check)
    def snapshot(state):
        r = call_device(state, 'status', '--json', check=False)
        return json.loads(r.stdout) if r.returncode == 0 else {}
    def await_device(predicate):
        end = time.monotonic() + 12
        while time.monotonic() < end:
            s = snapshot(a)
            if s and predicate(s): return s
            time.sleep(.1)
        raise AssertionError(f'device recovery failed: {snapshot(a)}')
    call_device(a, 'init', '--listen', '127.0.0.1:0')
    call_device(b, 'init', '--listen', '127.0.0.1:0')
    conf_a = json.loads((a/'config.json').read_text())
    conf_b = json.loads((b/'config.json').read_text())
    conf_b['key'] = conf_a['key']
    log = (base/'a.log').open('w')
    proc = subprocess.Popen([str(exe), '--state-dir', str(a), 'join'], env=recovery_env, stdout=log, stderr=log)
    try:
        first = await_device(lambda s: s['input_device']['online'] and s['output_device']['online'])
        conf_b['peers'] = [first['listen']]
        (b/'config.json').write_text(json.dumps(conf_b))
        call_device(b, 'daemon', 'start')
        connected = await_device(lambda s: len(s['peers']) == 1 and s['received_frames'] > 3)
        node_id = connected['id']
        call_device(a, 'mute')
        missing_in.touch()
        offline = await_device(lambda s: not s['input_device']['online'])
        assert offline['output_device']['online']
        continued = await_device(lambda s: s['received_frames'] > offline['received_frames'] + 10)
        assert continued['id'] == node_id and len(continued['peers']) == 1
        missing_in.unlink()
        restored = await_device(lambda s: s['input_device']['online'])
        assert restored['muted'] and restored['id'] == node_id
        missing_out.touch()
        offline = await_device(lambda s: not s['output_device']['online'])
        assert offline['input_device']['online']
        missing_out.unlink()
        restored = await_device(lambda s: s['output_device']['online'])
        assert restored['id'] == node_id and len(restored['peers']) == 1
        call_device(a, 'daemon', 'stop')
        proc.wait(timeout=5)
        assert proc.returncode == 0
    finally:
        call_device(b, 'daemon', 'stop', check=False)
        if proc.poll() is None: proc.kill(); proc.wait(timeout=5)
        log.close()
    print('Device hot unplug/reconnect: UDP peer and node identity preserved; mute retained')
