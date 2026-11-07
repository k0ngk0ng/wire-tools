#!/usr/bin/env python3
"""Real-process download API acceptance: pause/resume, crash/restart and dedup."""
import concurrent.futures
import hashlib
import http.server
import json
import os
from pathlib import Path
import shutil
import signal
import subprocess
import threading
import time
import unittest
import urllib.error
import urllib.request

ROOT = Path(__file__).resolve().parents[2]
BINARY = Path(os.environ.get('WIRECTL_TEST_BINARY', ROOT / 'bin/wirectl')).resolve()
SCRATCH = ROOT / '.cache/e2e-download'
EVIDENCE = ROOT / '.cache/evidence'
PAYLOAD = bytes(range(256)) * 16384

class DownloadAPI(unittest.TestCase):
    def setUp(self):
        self.dir = SCRATCH / self._testMethodName
        shutil.rmtree(self.dir, ignore_errors=True)
        self.dir.mkdir(parents=True)
        self.env = dict(os.environ, WIRECTL_HOME=str(self.dir), NO_PROXY='127.0.0.1,localhost')
        self.range_offsets = []
        self.requests = 0
        owner = self
        class Source(http.server.BaseHTTPRequestHandler):
            def do_GET(s):
                owner.requests += 1
                offset = 0
                if value := s.headers.get('Range'):
                    offset = int(value.removeprefix('bytes=').split('-')[0])
                    owner.range_offsets.append(offset)
                s.send_response(206 if offset else 200)
                s.send_header('Content-Length', str(len(PAYLOAD) - offset))
                s.send_header('Accept-Ranges', 'bytes')
                if offset:
                    s.send_header('Content-Range', f'bytes {offset}-{len(PAYLOAD)-1}/{len(PAYLOAD)}')
                s.end_headers()
                try:
                    for p in range(offset, len(PAYLOAD), 8192):
                        s.wfile.write(PAYLOAD[p:p + 8192])
                        s.wfile.flush()
                        time.sleep(float(os.environ.get("WIRECTL_TEST_SOURCE_DELAY", ".003")))
                except (BrokenPipeError, ConnectionResetError):
                    pass
            def log_message(*args):
                pass
        self.source = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Source)
        threading.Thread(target=self.source.serve_forever, daemon=True).start()
        self.url = f'http://127.0.0.1:{self.source.server_port}/transfer.bin'
        self.start_runtime()

    def start_runtime(self):
        proc = subprocess.run([str(BINARY), 'api', 'status', '--json'], cwd=ROOT,
                              env=self.env, text=True, capture_output=True, timeout=45)
        self.assertEqual(proc.returncode, 0, proc.stdout + proc.stderr)
        self.ep = json.loads((self.dir / 'api.json').read_text())
        return json.loads(proc.stdout)

    def request(self, method, path, body=None):
        req = urllib.request.Request('http://' + self.ep['address'] + path,
                                     data=None if body is None else json.dumps(body).encode(), method=method)
        req.add_header('Authorization', 'Bearer ' + self.ep['token'])
        req.add_header('Content-Type', 'application/json')
        try:
            with urllib.request.urlopen(req, timeout=40) as response:
                data = response.read()
                return json.loads(data) if data else None
        except urllib.error.HTTPError as e:
            self.fail(f'{method} {path}: HTTP {e.code}: {e.read().decode()}')

    def status(self, id):
        return next(j for j in self.request('GET', '/v1/download/status')['jobs'] if j['id'] == id)

    def wait(self, id, predicate, description):
        deadline = time.monotonic() + 30
        while time.monotonic() < deadline:
            job = self.status(id)
            if predicate(job):
                return job
            time.sleep(.04)
        self.fail(f'timed out waiting for {description}: {job}')

    def verify_complete(self, id):
        self.wait(id, lambda j: j['status'] == 'complete', 'completed download')
        data = self.dir.joinpath('download/downloads/transfer.bin').read_bytes()
        self.assertEqual(hashlib.sha256(data).hexdigest(), hashlib.sha256(PAYLOAD).hexdigest())

    def tearDown(self):
        try:
            self.request('POST', '/v1/runtime/shutdown', {})
            for _ in range(400):
                if not self.dir.joinpath('api.json').exists():
                    break
                time.sleep(.025)
        finally:
            self.source.shutdown()
            self.source.server_close()

    def test_tracker_and_login_maintenance_with_runtime_running(self):
        tracker = 'http://127.0.0.1:12345/announce'
        def cli(*args):
            result=subprocess.run([str(BINARY),*args],env=self.env,cwd=ROOT,text=True,capture_output=True,timeout=45)
            self.assertEqual(result.returncode,0,result.stdout+result.stderr)
            return result.stdout
        cli('download','bt','trackers','add',tracker)
        self.assertIn(tracker,self.request('GET','/v1/download/trackers'))
        cli('download','bt','trackers','remove',tracker)
        self.assertNotIn(tracker,self.request('GET','/v1/download/trackers'))
        session={'origin':'https://example.com','user_agent':'e2e','cookies':[{'name':'sid','value':'private-cookie','domain':'example.com','path':'/','secure':True}]}
        saved=self.request('POST','/v1/download/auth/sessions',session)
        self.assertTrue(saved['saved'])
        cli('download','logout','https://example.com')
        remaining=list((self.dir/'download/auth').glob('*.json'))
        self.assertEqual(remaining,[])

    def test_pause_resume_and_remove(self):
        job = self.request('POST', '/v1/download/jobs', {'source': self.url})
        id = job['id']
        self.wait(id, lambda j: j['completed'] > 16384, 'partial transfer')
        self.request('POST', f'/v1/download/jobs/{id}/pause', {})
        self.assertEqual(self.status(id)['status'], 'paused')
        self.request('POST', f'/v1/download/jobs/{id}/resume', {})
        self.verify_complete(id)
        self.assertTrue(any(x > 0 for x in self.range_offsets), self.range_offsets)
        self.request('POST', f'/v1/download/jobs/{id}/remove', {})
        self.assertEqual(self.status(id)['status'], 'removed')

    def test_crash_restart_recovers_partial(self):
        job = self.request('POST', '/v1/download/jobs', {'source': self.url})
        id = job['id']
        self.wait(id, lambda j: j['completed'] > int(os.environ.get('WIRECTL_TEST_CRASH_BYTES', '32768')), 'partial transfer')
        old_pid = self.ep['pid']
        os.kill(old_pid, signal.SIGTERM if os.name == 'nt' else signal.SIGKILL)
        time.sleep(.1)
        status = self.start_runtime()
        self.assertNotEqual(status['pid'], old_pid)
        self.verify_complete(id)
        self.assertTrue(any(x > 0 for x in self.range_offsets), self.range_offsets)
        self.assertEqual(len(self.request('GET', '/v1/download/status')['jobs']), 1)

    def test_graceful_restart_recovers_partial(self):
        job=self.request('POST','/v1/download/jobs',{'source':self.url})
        id=job['id']
        self.wait(id,lambda j:j['completed']>32768,'partial transfer')
        old_pid=self.ep['pid']
        self.request('POST','/v1/runtime/shutdown',{})
        for _ in range(1200):
            if not (self.dir/'api.json').exists(): break
            time.sleep(.025)
        self.assertFalse((self.dir/'api.json').exists(),'graceful shutdown did not finish')
        status=self.start_runtime()
        self.assertNotEqual(status['pid'],old_pid)
        terminal=self.wait(id,lambda j:j['status'] in ('complete','error'),'resumed transfer')
        self.assertEqual(terminal['status'],'complete',terminal)
        self.verify_complete(id)

    def test_concurrent_submissions_are_idempotent(self):
        with concurrent.futures.ThreadPoolExecutor(max_workers=10) as pool:
            jobs = list(pool.map(lambda _: self.request('POST', '/v1/download/jobs', {'source': self.url}), range(10)))
        ids = {j['id'] for j in jobs}
        self.assertEqual(len(ids), 1)
        self.verify_complete(next(iter(ids)))
        self.assertEqual(len(self.request('GET', '/v1/download/status')['jobs']), 1)
        self.assertEqual(self.requests, 1)

if __name__ == '__main__':
    SCRATCH.mkdir(parents=True, exist_ok=True)
    EVIDENCE.mkdir(parents=True, exist_ok=True)
    started = time.monotonic()
    result = unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(DownloadAPI))
    report = {'suite': 'download-process-e2e', 'binary': str(BINARY), 'tests': result.testsRun,
              'seconds': round(time.monotonic() - started, 3), 'successful': result.wasSuccessful(),
              'failures': [{'test': str(t), 'detail': d} for t, d in result.failures],
              'errors': [{'test': str(t), 'detail': d} for t, d in result.errors]}
    EVIDENCE.joinpath(os.environ.get('WIRECTL_TEST_REPORT', 'download-process-e2e.json')).write_text(json.dumps(report, indent=2) + '\n')
    raise SystemExit(0 if result.wasSuccessful() else 1)
