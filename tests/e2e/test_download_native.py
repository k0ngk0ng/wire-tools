#!/usr/bin/env python3
"""Unified managed-runtime E2E using real aria2/aMule and owned local fixtures."""
import ast
import base64
import concurrent.futures
import hashlib
import http.server
import importlib.util
import json
import os
from pathlib import Path
import re
import shutil
import socket
import struct
import subprocess
import sys
import threading
import time
import unittest
from urllib.parse import parse_qs, urlsplit
from urllib.request import Request, urlopen

sys.dont_write_bytecode = True
ROOT = Path(__file__).resolve().parents[2]
BINARY = Path(os.environ.get('WIRECTL_TEST_BINARY', ROOT / 'bin/wirectl')).resolve()
BUNDLE = Path(os.environ['WIRECTL_BUNDLE_DIR']).resolve()
ENGINES = BUNDLE / 'libexec/wirectl-download/bin'
SCRATCH = ROOT / '.cache/e2e-native'
EVIDENCE = ROOT / '.cache/evidence'

# Reuse the imported, independently exercised peer/tracker fixture definitions
# without executing the legacy script's CLI/daemon harness.
fixture_path = ROOT / 'components/download/scripts/test-bt-recovery.py'
syntax = ast.parse(fixture_path.read_text(), str(fixture_path))
nodes = [n for n in syntax.body if isinstance(n, (ast.FunctionDef, ast.ClassDef)) and n.name in ('bencode', 'Tracker', 'Seed')]
fixtures = dict(globals(), base_env=dict(os.environ, TMPDIR=str(SCRATCH)))
exec(compile(ast.Module(body=nodes, type_ignores=[]), str(fixture_path), 'exec'), fixtures)
bencode, Tracker, Seed = (fixtures[n] for n in ('bencode', 'Tracker', 'Seed'))
spec = importlib.util.spec_from_file_location('owned_ed2k_fixture', ROOT / 'components/download/scripts/ed2k_test_server.py')
ed2k = importlib.util.module_from_spec(spec)
spec.loader.exec_module(ed2k)

used_ports = set()
def free_port():
    while True:
        with socket.socket() as s:
            s.bind(('127.0.0.1', 0))
            port = s.getsockname()[1]
        if 1024 < port < 65532 and all(abs(port-p) > 4 for p in used_ports):
            used_ports.add(port)
            return port

class Profile:
    def __init__(self, directory, **changes):
        self.dir = directory
        self.env = dict(os.environ, WIRECTL_HOME=str(directory), TMPDIR=str(directory), NO_PROXY='127.0.0.1,localhost')
        directory.mkdir(parents=True)
        self.cli('download', 'init', '--downloads', str(directory / 'download/downloads'))
        path = directory / 'download/config.json'
        self.config = json.loads(path.read_text())
        self.config.update({k: free_port() for k in ('aria2_port', 'amule_ec_port', 'bt_port', 'ed2k_port', 'kad_port', 'auth_proxy_port')})
        self.config.update(aria2_binary=str(ENGINES / 'aria2c'), amuled_binary=str(ENGINES / 'amuled'), amulecmd_binary=str(ENGINES / 'amulecmd'), server_list_urls=[])
        self.config.update(changes)
        path.write_text(json.dumps(self.config))
        self.ep = json.loads((directory / 'api.json').read_text())

    def cli(self, *args):
        p = subprocess.run([str(BINARY), *args], env=self.env, cwd=ROOT, text=True, capture_output=True, timeout=90)
        if p.returncode:
            raise AssertionError(f'{args}: {p.stdout} {p.stderr}')
        return p.stdout

    def request(self, method, path, body=None):
        req = Request('http://' + self.ep['address'] + '/v1/download' + path,
                      data=None if body is None else json.dumps(body).encode(), method=method,
                      headers={'Authorization': 'Bearer ' + self.ep['token'], 'Content-Type': 'application/json'})
        with urlopen(req, timeout=90) as r:
            return json.load(r)

    def wait(self, check, label, timeout=90):
        deadline = time.monotonic() + timeout
        while time.monotonic() < deadline:
            if value := check():
                return value
            time.sleep(.2)
        raise AssertionError('Timed out: ' + label)

    def ec(self, command):
        p = subprocess.run([str(ENGINES / 'amulecmd'), '--config-file=../amule/remote.conf', '--command=' + command],
                           cwd=self.dir / 'download', env=dict(self.env, LC_ALL='en_US.UTF-8'), text=True,
                           capture_output=True, timeout=40)
        if p.returncode:
            raise AssertionError(p.stdout + p.stderr)
        return p.stdout

    def start_amule(self):
        search = self.request('POST', '/searches', {'query': 'owned fixture', 'type': 'ed2k', 'sources': ['ed2k'], 'timeout_seconds': 10, 'ed2k_mode': 'server'})
        def complete():
            result = self.request('GET', '/searches/' + search['id'])
            return result if result['status'] in ('complete', 'partial', 'failed') else None
        result = self.wait(complete, 'native EC provider startup', 60)
        if result['status'] == 'failed':
            raise AssertionError(result)
        self.ec('disconnect kad')

    def stop(self):
        ep = self.ep
        req = Request('http://' + ep['address'] + '/v1/runtime/shutdown', data=b'{}', method='POST',
                      headers={'Authorization': 'Bearer ' + ep['token'], 'Content-Type': 'application/json'})
        try:
            with urlopen(req, timeout=30) as r:
                r.read()
            self.wait(lambda: not (self.dir / 'api.json').exists(), 'runtime shutdown', 40)
        except OSError:
            pass

class NativeDownload(unittest.TestCase):
    def setUp(self):
        self.dir = SCRATCH / self._testMethodName
        shutil.rmtree(self.dir, ignore_errors=True)
        self.dir.mkdir(parents=True)
        self.profiles = []

    def profile(self, name, **changes):
        p = Profile(self.dir / name, **changes)
        self.profiles.append(p)
        return p

    def tearDown(self):
        for p in reversed(self.profiles):
            p.stop()

    def test_torrent_peer_transfer_and_seeding(self):
        seed_dir = self.dir / 'seed'
        seed_dir.mkdir()
        payload = hashlib.sha256(b'owned unified torrent fixture').digest() * 65536
        filename = 'owned-torrent.bin'
        (seed_dir / filename).write_bytes(payload)
        bt_port, rpc_port = free_port(), free_port()
        tracker = Tracker(bt_port)
        seed = Seed(ENGINES / 'aria2c', seed_dir, rpc_port, bt_port, tracker.url)
        try:
            info = {b'length': len(payload), b'name': filename.encode(), b'piece length': 16384,
                    b'pieces': b''.join(hashlib.sha1(payload[i:i+16384]).digest() for i in range(0, len(payload), 16384))}
            torrent = bencode({b'announce': tracker.url.encode(), b'info': info})
            torrent_path = self.dir / 'fixture.torrent'
            torrent_path.write_bytes(torrent)
            seed.wait_ready()
            seed.wait_seeding(seed.add(torrent), len(payload))
            p = self.profile('leecher', trackers=[tracker.url], seed_ratio=0)
            job = p.request('POST', '/jobs', {'source': str(torrent_path)})
            self.assertEqual(job['engine'], 'aria2')
            def finished():
                j = next(j for j in p.request('GET', '/status')['jobs'] if j['id'] == job['id'])
                if j['status'] == 'error':
                    raise AssertionError(j)
                return j if j['completed'] == len(payload) and j['status'] in ('seeding', 'complete') else None
            result = p.wait(finished, 'torrent peer transfer', 90)
            self.assertEqual(hashlib.sha256((p.dir / 'download/downloads' / filename).read_bytes()).hexdigest(), hashlib.sha256(payload).hexdigest())
            self.assertTrue(tracker.requests)
            p.request('POST', f"/jobs/{job['id']}/remove", {})
            self.assertEqual(p.request('GET', '/status')['jobs'][0]['status'], 'removed')
        finally:
            seed.close()
            tracker.close()

    def test_ed2k_local_server_search_and_transfer(self):
        with socket.socket(socket.AF_INET, socket.SOCK_DGRAM) as route:
            route.connect(('192.0.2.1', 9))
            address = route.getsockname()[0]
        self.assertFalse(address.startswith('127.'), 'aMule requires an assigned non-loopback interface')
        server = ed2k.Server((address, 0), ed2k.Handler)
        threading.Thread(target=server.serve_forever, daemon=True).start()
        try:
            seed = self.profile('seed')
            leecher = self.profile('leecher')
            payload = hashlib.sha256(b'owned unified ED2K fixture').digest() * 4096
            filename = 'owned-ed2k.bin'
            for p in (seed, leecher):
                directory = p.dir / 'download/amule'
                directory.mkdir()
                (directory / 'amule.conf').write_text('[eMule]\nFilterLanIPs=0\n')
                met = b'\xe0' + struct.pack('<I', 1) + socket.inet_aton(address) + struct.pack('<HI', server.server_address[1], 0)
                (directory / 'server.met').write_bytes(met)
                (p.dir / 'download/downloads').mkdir()
            (seed.dir / 'download/downloads' / filename).write_bytes(payload)
            seed.start_amule()
            def shared_hash():
                match = re.search(r'(?im)^\s*(?:>\s*)?([0-9a-f]{32})[^\r\n]*' + filename, seed.ec('show shared'))
                return match.group(1).lower() if match else None
            digest = seed.wait(shared_hash, 'seed hashes and shares file', 30)
            leecher.start_amule()
            leecher.wait(lambda: len(server.client_ids) >= 2, 'both native cores logged into local server', 30)
            server.configure_search(bytes.fromhex(digest), filename, len(payload), address, seed.config['ed2k_port'])
            job = leecher.request('POST', '/jobs', {'source': f'ed2k://|file|{filename}|{len(payload)}|{digest}|/'})
            self.assertEqual(job['engine'], 'amule')
            def finished():
                j = next(j for j in leecher.request('GET', '/status')['jobs'] if j['id'] == job['id'])
                if j['status'] == 'error':
                    raise AssertionError(j)
                return j if j['status'] == 'complete' else None
            leecher.wait(finished, 'ED2K peer transfer', 120)
            self.assertEqual(hashlib.sha256((leecher.dir / 'download/downloads' / filename).read_bytes()).hexdigest(), hashlib.sha256(payload).hexdigest())
            self.assertGreater(server.get_sources_count, 0)
        finally:
            server.shutdown()
            server.server_close()

if __name__ == '__main__':
    SCRATCH.mkdir(parents=True, exist_ok=True)
    EVIDENCE.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    result = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(NativeDownload))
    report = {'suite': 'download-native-protocol-process-e2e', 'binary': str(BINARY), 'bundle': str(BUNDLE),
              'tests': result.testsRun, 'seconds': round(time.monotonic()-started, 3), 'successful': result.wasSuccessful(),
              'failures': [{'test': str(t), 'detail': d} for t, d in result.failures],
              'errors': [{'test': str(t), 'detail': d} for t, d in result.errors]}
    EVIDENCE.joinpath('download-native-protocol-e2e.json').write_text(json.dumps(report, indent=2) + '\n')
    raise SystemExit(0 if result.wasSuccessful() else 1)
