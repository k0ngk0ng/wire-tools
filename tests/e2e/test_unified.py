#!/usr/bin/env python3
"""Process-level acceptance tests. Scratch and evidence stay in the repository."""
import concurrent.futures
import hashlib
import http.server
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import threading
import time
import unittest
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
BINARY = Path(os.environ.get('WIRECTL_TEST_BINARY', ROOT / 'bin/wirectl')).resolve()
SCRATCH = ROOT / '.cache/e2e'
EVIDENCE = ROOT / '.cache/evidence'

class Unified(unittest.TestCase):
    def setUp(self):
        self.dir = SCRATCH / self._testMethodName
        shutil.rmtree(self.dir, ignore_errors=True)
        self.dir.mkdir(parents=True)
        self.env = dict(os.environ, WIRECTL_HOME=str(self.dir), NO_PROXY='127.0.0.1,localhost')
        self.endpoint = self.dir / 'api.json'

    def runcli(self, *args, ok=True):
        p = subprocess.run([str(BINARY), *args], env=self.env, cwd=ROOT,
                           text=True, capture_output=True, timeout=60)
        if ok:
            self.assertEqual(p.returncode, 0, p.stdout + p.stderr)
        return p

    def request(self, method, path, body=None, auth=True):
        ep = json.loads(self.endpoint.read_text())
        req = urllib.request.Request('http://' + ep['address'] + path,
                                     data=None if body is None else json.dumps(body).encode(), method=method)
        req.add_header('Content-Type', 'application/json')
        if auth:
            req.add_header('Authorization', 'Bearer ' + ep['token'])
        with urllib.request.urlopen(req, timeout=45) as response:
            data = response.read()
            return json.loads(data) if data else None

    def tearDown(self):
        if self.endpoint.exists():
            try:
                self.request('POST', '/v1/runtime/shutdown', {})
                for _ in range(200):
                    if not self.endpoint.exists():
                        break
                    time.sleep(.05)
            except (OSError, ValueError):
                pass

    def test_help_is_self_contained(self):
        out = self.runcli('--help').stdout
        for command in ['connect', 'download', 'talk', 'update', 'api']:
            self.assertIn(command, out)
        self.assertNotIn('Installed plugin', out)
        self.assertFalse(self.endpoint.exists())

    def test_concurrent_first_use_and_auth(self):
        with concurrent.futures.ThreadPoolExecutor(max_workers=6) as pool:
            results = list(pool.map(lambda _: self.runcli('api', 'status', '--json'), range(6)))
        pids = {json.loads(p.stdout)['pid'] for p in results}
        self.assertEqual(len(pids), 1)
        ep = json.loads(self.endpoint.read_text())
        self.assertTrue(ep['address'].startswith('127.0.0.1:'))
        with self.assertRaises(urllib.error.HTTPError) as e:
            self.request('GET', '/v1/status', auth=False)
        self.assertEqual(e.exception.code, 401)
        if os.name != 'nt':
            self.assertEqual(self.endpoint.stat().st_mode & 0o777, 0o600)

    def test_stale_endpoint_recovers(self):
        self.dir.joinpath('api.json').write_text(json.dumps({'address': '127.0.0.1:1', 'token': 'a'*64}))
        status = json.loads(self.runcli('api', 'status', '--json').stdout)
        self.assertGreater(status['pid'], 0)
        self.assertIn('version', status)

    def test_http_download_without_manual_start(self):
        payload = b'unified wirectl end-to-end\n' * 8192
        class Source(http.server.BaseHTTPRequestHandler):
            def do_GET(s):
                s.send_response(200)
                s.send_header('Content-Length', str(len(payload)))
                s.end_headers()
                s.wfile.write(payload)
            def log_message(*args):
                pass
        server = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Source)
        thread = threading.Thread(target=server.serve_forever, daemon=True)
        thread.start()
        try:
            source = 'http://127.0.0.1:%s/evidence.bin' % server.server_port
            self.runcli('download', '--detach', source)
            deadline = time.monotonic() + 30
            while time.monotonic() < deadline:
                status = self.request('GET', '/v1/download/status')
                if status['jobs'] and status['jobs'][0]['status'] == 'complete':
                    break
                time.sleep(.1)
            self.assertEqual(status['jobs'][0]['status'], 'complete', status)
            dest = self.dir / 'download/downloads/evidence.bin'
            self.assertEqual(hashlib.sha256(dest.read_bytes()).hexdigest(), hashlib.sha256(payload).hexdigest())
            again = self.request('POST', '/v1/download/jobs', {'source': source})
            self.assertEqual(again['id'], status['jobs'][0]['id'])
            self.runcli('download', 'list')
        finally:
            server.shutdown()
            server.server_close()

    def test_talk_action_bootstraps_session(self):
        self.runcli('talk', 'init', '--listen', '127.0.0.1:0', '--input', 'none')
        self.runcli('talk', 'mute')
        status = self.request('GET', '/v1/talk/status')
        self.assertTrue(status['muted'])
        self.runcli('talk', 'unmute')
        self.assertFalse(self.request('GET', '/v1/talk/status')['muted'])

    def test_connect_resources_and_missing_credentials(self):
        self.runcli('api', 'status', '--json')
        result = self.request('GET', '/v1/connect/connections')
        self.assertIsInstance(result, (dict, list))
        bad = self.runcli('connect', 'resume', ok=False)
        self.assertNotEqual(bad.returncode, 0)
        self.assertNotIn('daemon start', bad.stderr)

    def test_invalid_request_does_not_break_other_features(self):
        self.runcli('api', 'status', '--json')
        with self.assertRaises(urllib.error.HTTPError):
            self.request('POST', '/v1/download/jobs', {'source': 'invalid://source'})
        self.assertIn('version', self.request('GET', '/v1/status'))

if __name__ == '__main__':
    SCRATCH.mkdir(parents=True, exist_ok=True)
    EVIDENCE.mkdir(parents=True, exist_ok=True)
    suite = unittest.defaultTestLoader.loadTestsFromTestCase(Unified)
    result = unittest.TextTestRunner(verbosity=2).run(suite)
    report = {'suite': 'unified-process-e2e', 'binary': str(BINARY), 'tests': result.testsRun,
              'failures': [{'test': str(t), 'detail': d} for t, d in result.failures],
              'errors': [{'test': str(t), 'detail': d} for t, d in result.errors],
              'successful': result.wasSuccessful()}
    (EVIDENCE / 'e2e.json').write_text(json.dumps(report, indent=2) + '\n')
    sys.exit(0 if result.wasSuccessful() else 1)
