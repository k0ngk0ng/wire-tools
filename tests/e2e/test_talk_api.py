#!/usr/bin/env python3
"""Two-process local API acceptance tests using real UDP and authenticated PAKE.

Run against a wirectl binary built with -tags=talk_test_audio. This verifies the
software audio/network path, not microphone or speaker hardware. All scratch and
reports remain below the checkout; reports intentionally omit tokens and keys.
"""
import hashlib
import json
import os
from pathlib import Path
import shutil
import signal
import socket
import struct
import subprocess
import sys
import time
import unittest
import urllib.error
import urllib.request
import wave

ROOT = Path(__file__).resolve().parents[2]
BINARY = Path(os.environ.get('WIRECTL_TEST_BINARY', ROOT / 'bin/wirectl')).resolve()
SCRATCH = ROOT / '.cache/e2e/talk-process'
EVIDENCE = ROOT / '.cache/evidence'
FIXTURE = ROOT / 'components/talk/internal/media/testdata/tone.wav'
PROOF = []


def available_room_address():
    """Find one loopback port simultaneously available for UDP and TCP."""
    for _ in range(20):
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as udp:
            udp.bind(('127.0.0.1', 0))
            address = udp.getsockname()
            with socket.socket(socket.AF_INET, socket.SOCK_STREAM) as tcp:
                try:
                    tcp.bind(address)
                except OSError:
                    continue
                return '%s:%d' % address
    raise RuntimeError('could not reserve a loopback room port')


class Runtime:
    def __init__(self, home):
        self.home = home
        home.mkdir(parents=True)
        self.env = dict(os.environ, WIRECTL_HOME=str(home), NO_PROXY='127.0.0.1,localhost')
        self.opener = urllib.request.build_opener(urllib.request.ProxyHandler({}))

    def cli(self, *args):
        p = subprocess.run([str(BINARY), *args], env=self.env, cwd=ROOT,
                           capture_output=True, text=True, timeout=45)
        if p.returncode:
            raise AssertionError('CLI %s failed: %s' % (' '.join(args[:3]), p.stderr))
        return p.stdout

    def endpoint(self):
        return json.loads((self.home / 'api.json').read_text())

    def start(self):
        return json.loads(self.cli('api', 'status', '--json'))

    def request(self, method, path, body=None):
        endpoint = self.endpoint()
        payload = None if body is None else json.dumps(body).encode()
        req = urllib.request.Request('http://' + endpoint['address'] + '/v1/talk' + path,
                                     data=payload, method=method)
        req.add_header('Content-Type', 'application/json')
        req.add_header('Authorization', 'Bearer ' + endpoint['token'])
        try:
            with self.opener.open(req, timeout=30) as r:
                data = r.read()
                return json.loads(data) if data else None
        except urllib.error.HTTPError as e:
            # Error responses contain diagnostics only. Request bodies and
            # endpoint capabilities must never be included in report output.
            diagnostic = e.read(4096).decode(errors='replace')
            raise AssertionError('%s %s: HTTP %d %s' % (method, path, e.code, diagnostic)) from None

    def status(self):
        return self.request('GET', '/status')

    def close(self):
        endpoint_path = self.home / 'api.json'
        if not endpoint_path.exists():
            return
        try:
            ep = self.endpoint()
            req = urllib.request.Request('http://' + ep['address'] + '/v1/runtime/shutdown',
                                         data=b'{}', method='POST')
            req.add_header('Authorization', 'Bearer ' + ep['token'])
            with self.opener.open(req, timeout=10) as response:
                response.read()
            deadline = time.monotonic() + 10
            while endpoint_path.exists() and time.monotonic() < deadline:
                time.sleep(.05)
        except (OSError, ValueError):
            pass


def wait_for(fetch, predicate, label, timeout=15):
    deadline = time.monotonic() + timeout
    last = None
    while time.monotonic() < deadline:
        last = fetch()
        if predicate(last):
            return last
        time.sleep(.05)
    raise AssertionError('timed out waiting for ' + label)


class TalkProcesses(unittest.TestCase):
    def setUp(self):
        self.path = SCRATCH / self._testMethodName
        shutil.rmtree(self.path, ignore_errors=True)
        self.path.mkdir(parents=True)
        self.a, self.b = Runtime(self.path / 'a'), Runtime(self.path / 'b')

    def tearDown(self):
        self.a.close()
        self.b.close()

    def pair(self, mode, ring_output=None, input_device='none', output_device=''):
        self.a.start()
        self.b.start()
        address_a, address_b = available_room_address(), available_room_address()
        while address_b == address_a:
            address_b = available_room_address()
        local = {'input': input_device, 'output': output_device}
        if ring_output is not None:
            local['ring_output'] = ring_output
        self.a.request('POST', '/config', {'listen': address_a, 'mode': mode, **local})
        self.a.request('POST', '/session', {})
        invitation = self.a.request('POST', '/invitations', {})
        self.b.request('POST', '/pair', {'address': invitation['address'], 'code': invitation['code'],
                                       'listen': address_b, **local})
        # An ordinary action transparently starts B's saved session.
        self.b.cli('talk', 'mute')
        self.assertTrue(self.b.status()['muted'])
        self.b.cli('talk', 'unmute')
        self.assertFalse(self.b.status()['muted'])
        wait_for(self.a.status, lambda s: len(s.get('peers', [])) == 1, 'first peer discovery')
        wait_for(self.b.status, lambda s: len(s.get('peers', [])) == 1, 'second peer discovery')
        self.assertEqual(self.a.status()['mode'], mode)
        self.assertEqual(self.b.status()['mode'], mode)

    def transfer_and_record(self, phase='initial'):
        recording = self.b.home / ('received-' + phase + '.wav')
        before = self.b.status()['received_frames']
        self.b.request('POST', '/media', {'action': 'record-start', 'file': str(recording)})
        self.a.request('POST', '/media', {'action': 'input-start', 'file': str(FIXTURE),
                                         'mode': 'replace', 'loop': True})
        # Input-session mute affects the microphone, not explicitly sent files.
        self.a.cli('talk', 'mute')
        self.assertTrue(self.a.status()['muted'])
        self.b.cli('talk', 'mute', 'output')
        self.assertTrue(self.b.status()['output_muted'])
        snapshot = wait_for(self.b.status,
                            lambda s: s['received_frames'] >= before + 30 and
                                      s['media']['recording']['bytes'] >= 16000,
                            'real UDP audio reception and recording', timeout=20)
        self.a.request('POST', '/media', {'action': 'input-stop'})
        self.b.request('POST', '/media', {'action': 'record-stop'})
        self.a.cli('talk', 'unmute')
        self.b.cli('talk', 'unmute', 'output')
        self.assertFalse(self.a.status()['muted'])
        self.assertFalse(self.b.status()['output_muted'])
        with wave.open(str(recording), 'rb') as wav:
            self.assertEqual((wav.getnchannels(), wav.getsampwidth(), wav.getframerate()), (1, 2, 16000))
            pcm = wav.readframes(wav.getnframes())
        samples = struct.unpack('<%dh' % (len(pcm) // 2), pcm)
        self.assertGreater(max(abs(s) for s in samples), 100, 'received only silence')
        PROOF.append({'test': self._testMethodName, 'phase': phase, 'mode': snapshot['mode'],
                      'received_frames_delta': snapshot['received_frames'] - before,
                      'recording_bytes': len(pcm), 'peak_pcm': max(abs(s) for s in samples),
                      'recording_sha256': hashlib.sha256(recording.read_bytes()).hexdigest()})

    def test_repeated_session_requests_with_json_body(self):
        self.a.start()
        self.a.request('POST','/config',{'listen':'127.0.0.1:0','input':'none'})
        for _ in range(100):
            status=self.a.request('POST','/session',{})
            self.assertIn('version',status)
        self.a.request('DELETE','/session')
        self.assertFalse(self.a.status()['active'])

    def test_room_pair_media_leave_and_crash_recovery(self):
        self.pair('room')
        self.transfer_and_record()
        old = self.a.endpoint()
        old_pid = old['pid']
        if os.name == 'nt':
            subprocess.run(['taskkill', '/F', '/PID', str(old_pid)], check=True, capture_output=True)
        else:
            os.kill(old_pid, signal.SIGKILL)
        time.sleep(.2)
        # Stale endpoint remains after an abrupt kill; the next CLI action must
        # re-establish the API and its session without a daemon command.
        self.assertTrue((self.a.home / 'api.json').exists())
        self.a.cli('talk', 'mute')
        self.assertNotEqual(self.a.endpoint()['pid'], old_pid)
        self.assertTrue(self.a.status()['muted'])
        wait_for(self.a.status, lambda s: len(s.get('peers', [])) == 1, 'peer discovery after runtime crash')
        self.transfer_and_record(phase='after-crash')
        self.a.cli('talk', 'leave')
        status = self.a.status()
        self.assertFalse(status['active'])
        self.assertTrue(status['configured'])
        self.a.cli('talk', 'join')
        self.assertEqual(self.a.status()['mode'], 'room')
        self.a.cli('talk', 'leave')

    def test_direct_pair_call_media_and_hangup(self):
        self.pair('direct')
        self.assertEqual(self.a.status()['call']['state'], 'idle')
        self.a.cli('talk', 'call', 'dial')
        wait_for(self.b.status, lambda s: s['call']['state'] == 'ringing', 'incoming direct call')
        self.b.cli('talk', 'call', 'answer')
        wait_for(self.a.status, lambda s: s['call']['state'] == 'active', 'active caller')
        wait_for(self.b.status, lambda s: s['call']['state'] == 'active', 'active receiver')
        self.transfer_and_record()
        self.b.cli('talk', 'call', 'hangup')
        wait_for(self.a.status, lambda s: s['call']['state'] == 'idle', 'remote hangup propagation')
        self.assertEqual(self.b.status()['call']['state'], 'idle')
        self.a.cli('talk', 'leave')
        self.b.cli('talk', 'leave')
        self.assertFalse(self.a.status()['active'])
        self.assertFalse(self.b.status()['active'])

    def test_ring_output_cli_and_pair_configuration(self):
        self.a.cli('talk', 'init', '--mode', 'direct', '--listen', '127.0.0.1:0',
                   '--input', 'none', '--ring-output', 'missing-usb-speaker')
        config = self.a.request('GET', '/config')
        self.assertEqual(config['ring_output'], 'missing-usb-speaker')
        self.assertNotIn('key', config)
        self.a.cli('talk', 'join')
        s = wait_for(self.a.status,
                     lambda s: s.get('output_device', {}).get('online'), 'call output ready')
        self.assertFalse(s['ring_output_device']['online'])
        self.assertTrue(s['ring_output_device']['error'])
        PROOF.append({'test': self._testMethodName, 'missing_ringer_has_no_fallback': True})

    def test_direct_separate_ringer_and_handset_button(self):
        # Named endpoints use two native Null device instances. They validate
        # routed PCM independently, without claiming physical USB validation.
        events = self.path / 'handset-events.jsonl'
        events.touch()
        unplugged = self.path / 'ringer-unplugged'
        self.b.env.update(WIRE_TALK_TEST_HANDSET_EVENTS=str(events),
                          WIRE_TALK_TEST_RING_OUTPUT_OFFLINE=str(unplugged))
        self.a.start()
        output = 'talk-test-ring-output'
        self.pair('direct', ring_output=output, input_device='')
        self.b.cli('plugin', 'enable', 'spacetouch')
        wait_for(lambda: json.loads(self.b.cli('plugin', 'status', 'spacetouch', '--json')),
                 lambda s: s['online'], 'USB controller plugin ready')
        for node in (self.a, self.b):
            self.assertEqual(node.request('GET', '/config')['ring_output'], output)
            wait_for(node.status, lambda s: s['input_device']['online'] and
                     s['output_device']['online'] and s['ring_output_device']['online'],
                     'all three audio endpoints')
            node.request('POST', '/media', {'action': 'input-start', 'file': str(FIXTURE),
                                           'mode': 'replace', 'loop': True})

        def key(value):
            with events.open('a') as f:
                f.write(json.dumps({'usage_page': 12, 'usage': 205, 'value': value}) + '\n')

        before = {n: n.status()['sent_frames'] for n in (self.a, self.b)}
        preanswer = self.b.home / 'preanswer.wav'
        self.b.request('POST', '/media', {'action': 'record-start', 'file': str(preanswer)})
        self.a.cli('talk', 'call', 'dial')
        ringing = wait_for(self.b.status,
                           lambda s: s['call']['state'] == 'ringing' and
                           s['ring_output_device']['level']['peak'] > 0,
                           'incoming ringtone on separate speaker')
        self.assertEqual(ringing['output_device']['level']['peak'], 0)
        caller = wait_for(self.a.status,
                          lambda s: s['output_device']['level']['peak'] > 0,
                          'caller ringback remains in handset')
        self.assertEqual(caller['ring_output_device']['level']['peak'], 0)
        for node in before:
            self.assertEqual(node.status()['sent_frames'], before[node])
        self.b.request('POST', '/media', {'action': 'record-stop'})
        with wave.open(str(preanswer), 'rb') as wav:
            self.assertFalse(any(wav.readframes(wav.getnframes())), 'ringtone leaked into recording')

        # A missing ringer cannot reopen a different speaker or stop the handset.
        unplugged.touch()
        offline = wait_for(self.b.status, lambda s: not s['ring_output_device']['online'],
                           'ringer unplug')
        self.assertTrue(offline['input_device']['online'])
        self.assertTrue(offline['output_device']['online'])
        self.assertEqual(offline['output_device']['level']['peak'], 0)
        unplugged.unlink()
        wait_for(self.b.status, lambda s: s['ring_output_device']['online'] and
                 s['ring_output_device']['level']['peak'] > 0, 'ringer reconnect')

        key(0)  # Release cannot answer.
        time.sleep(.15)
        self.assertEqual(self.b.status()['call']['state'], 'ringing')
        key(1)
        key(1)  # Holding the play key cannot answer then immediately hang up.
        wait_for(self.a.status, lambda s: s['call']['state'] == 'active', 'button answer caller')
        active = wait_for(self.b.status, lambda s: s['call']['state'] == 'active' and
                          s['received_frames'] > 5 and s['output_device']['level']['peak'] > 0 and
                          s['ring_output_device']['level']['peak'] == 0,
                          'voice only in handset after answer')
        wait_for(self.a.status, lambda s: s['received_frames'] > 5, 'reverse voice')
        key(0)
        unplugged.touch()
        previous = self.b.status()['received_frames']
        wait_for(self.b.status, lambda s: not s['ring_output_device']['online'] and
                 s['received_frames'] > previous + 10 and s['output_device']['online'],
                 'active call survives ringer loss')
        unplugged.unlink()
        recovered = wait_for(self.b.status, lambda s: s['ring_output_device']['online'],
                             'silent ringer recovers during call')
        self.assertEqual(recovered['ring_output_device']['level']['peak'], 0)
        self.assertEqual(recovered['call']['state'], 'active')
        key(1)
        key(0)
        wait_for(self.a.status, lambda s: s['call']['state'] == 'idle', 'button hangup')
        stopped = wait_for(self.b.status, lambda s: s['call']['state'] == 'idle' and
                           s['output_device']['level']['peak'] == 0 and
                           s['ring_output_device']['level']['peak'] == 0,
                           'both outputs silent after hangup')
        # Cancelling a subsequent incoming call also clears the ringer.
        self.a.cli('talk', 'call', 'dial')
        wait_for(self.b.status, lambda s: s['call']['state'] == 'ringing', 'second incoming call')
        self.a.cli('talk', 'call', 'hangup')
        wait_for(self.b.status, lambda s: s['call']['state'] == 'idle' and
                 s['ring_output_device']['level']['peak'] == 0, 'cancel stops ringer')
        PROOF.append({'test': self._testMethodName, 'ringing': ringing,
                      'active': active, 'hangup': stopped,
                      'preanswer_recording_sha256': hashlib.sha256(preanswer.read_bytes()).hexdigest(),
                      'ringer_reconnect_during_call': True})

    def test_direct_legacy_ringtone_uses_call_output(self):
        self.pair('direct')
        self.a.cli('talk', 'call', 'dial')
        incoming = wait_for(self.b.status, lambda s: s['call']['state'] == 'ringing' and
                            s['output_device']['level']['peak'] > 0, 'legacy single-output ringtone')
        self.assertNotIn('ring_output_device', incoming)
        self.a.cli('talk', 'call', 'hangup')

    def test_same_ring_and_call_output_share_one_endpoint(self):
        self.a.start()
        output = next(d['id'] for d in self.a.request('GET', '/devices')
                      if d['kind'] == 'output' and d['default'])
        self.pair('direct', ring_output=output, output_device=output)
        self.a.cli('talk', 'call', 'dial')
        incoming = wait_for(self.b.status, lambda s: s['call']['state'] == 'ringing' and
                            s['output_device']['level']['peak'] > 0, 'shared output ringtone')
        self.assertNotIn('ring_output_device', incoming)
        self.a.cli('talk', 'call', 'hangup')

    def test_devices_set_preserves_room_and_requires_disconnect(self):
        self.pair('direct')
        config_file = self.a.home / 'talk' / 'config.json'
        before = json.loads(config_file.read_text())
        with self.assertRaisesRegex(AssertionError, 'disconnect'):
            self.a.cli('talk', 'devices', 'set', '--ring-output', 'talk-test-ring-output')
        self.assertEqual(json.loads(config_file.read_text()), before)
        self.a.cli('talk', 'leave')
        output = next(d['id'] for d in self.a.request('GET', '/devices')
                      if d['kind'] == 'output' and d['default'])
        self.a.cli('talk', 'devices', 'set', '--input', 'none', '--output', output,
                   '--ring-output', 'talk-test-ring-output')
        updated = json.loads(config_file.read_text())
        for key in set(before) | set(updated):
            if key not in ('input', 'output', 'ring_output'):
                self.assertEqual(before.get(key), updated.get(key), 'changed saved room field ' + key)
        self.assertEqual(updated['ring_output'], 'talk-test-ring-output')
        self.assertFalse(self.a.status()['active'])
        with self.assertRaisesRegex(AssertionError, 'HTTP 400'):
            self.a.request('PATCH', '/config', {})
        with self.assertRaisesRegex(AssertionError, 'HTTP 400'):
            self.a.request('PATCH', '/config', {'key': 'must-not-replace-room-key'})
        partial = self.a.request('PATCH', '/config', {'ring_output': ''})
        self.assertEqual(partial['ring_output'], '')
        self.assertEqual(partial['output'], output)
        self.assertEqual(partial['input'], 'none')
        self.a.cli('talk', 'devices', 'set', '--ring-output', 'talk-test-ring-output')
        self.a.cli('talk', 'join')
        wait_for(self.a.status, lambda s: s['ring_output_device']['online'], 'changed ring output ready')
        PROOF.append({'test': self._testMethodName, 'saved_room_preserved': True,
                      'requires_disconnected_session': True, 'partial_update_preserves_other_routes': True})


if __name__ == '__main__':
    SCRATCH.mkdir(parents=True, exist_ok=True)
    EVIDENCE.mkdir(parents=True, exist_ok=True)
    suite = (unittest.defaultTestLoader.loadTestsFromNames(sys.argv[1:], sys.modules[__name__])
             if len(sys.argv) > 1 else unittest.defaultTestLoader.loadTestsFromTestCase(TalkProcesses))
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    report = {'suite': 'talk-process-api-e2e', 'binary': str(BINARY),
              'audio_backend': 'talk_test_audio software driver; no physical device validation',
              'tests': result.testsRun, 'successful': result.wasSuccessful(), 'proof': PROOF,
              'failures': [{'test': str(t), 'detail': detail} for t, detail in result.failures],
              'errors': [{'test': str(t), 'detail': detail} for t, detail in result.errors]}
    (EVIDENCE / 'talk-process-e2e.json').write_text(json.dumps(report, indent=2) + '\n')
    sys.exit(0 if result.wasSuccessful() else 1)
