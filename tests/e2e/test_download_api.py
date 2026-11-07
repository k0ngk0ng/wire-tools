#!/usr/bin/env python3
"""Real-process download API acceptance: pause/resume, crash/restart and dedup."""
import concurrent.futures
import hashlib
import http.server
import json
import os
import re
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
                except (BrokenPipeError, ConnectionResetError, ConnectionAbortedError):
                    pass
            def log_message(*args):
                pass
        self.source = http.server.ThreadingHTTPServer(('127.0.0.1', 0), Source)
        threading.Thread(target=self.source.serve_forever, daemon=True).start()
        self.url = f'http://127.0.0.1:{self.source.server_port}/transfer.bin'
        self.start_runtime()
        self._diagnostic_stages=set()
        self.diagnostics('initialized',probe_version=True)

    def diagnostics(self,stage,probe_version=False):
        if os.environ.get('WIRECTL_DOWNLOAD_DIAGNOSTICS')!='1' or stage in self._diagnostic_stages:
            return
        self._diagnostic_stages.add(stage)
        out=EVIDENCE/'download-diagnostics'/self._testMethodName/stage
        out.mkdir(parents=True,exist_ok=True)
        try:
            def clean(text):
                lines=[]
                for line in text.splitlines():
                    if any(key in line.lower() for key in ('cookie','authorization','rpc-secret','bearer ','token:')):
                        continue
                    lines.append(re.sub(r'(?i)\b[0-9a-f]{64}\b','[redacted]',line))
                return '\n'.join(lines)+'\n'
            for rel in ('runtime.log','download/logs/aria2.log','download/aria2/session.txt'):
                file=self.dir/rel
                if file.is_file():
                    text=file.read_bytes()[-256*1024:].decode('utf-8',errors='replace')
                    if rel.endswith('session.txt'):
                        text='\n'.join(line for line in text.splitlines() if '://' not in line or line.strip()==self.url)
                    (out/rel.replace('/','-')).write_text(clean(text))
            jobs=self.dir/'download/jobs.json'
            if jobs.exists():
                data=json.loads(jobs.read_text())
                data['jobs']=[j for j in data.get('jobs',[]) if j.get('source')==self.url]
                (out/'jobs.json').write_text(json.dumps(data,indent=2)+'\n')
            files=[]
            for file in (self.dir/'download/downloads').glob('*'):
                if file.is_file():
                    files.append({'name':file.name,'size':file.stat().st_size,'sha256':hashlib.sha256(file.read_bytes()).hexdigest()})
            (out/'files.json').write_text(json.dumps(files,indent=2)+'\n')
            # The token remains in memory only. Persist a narrow native task
            # view so restored GID changes can be distinguished from empty input.
            config=self.dir/'download/aria2/aria2.conf'
            if config.exists():
                options=dict(line.split('=',1) for line in config.read_text().splitlines() if '=' in line)
                secret=options.get('rpc-secret','');port=options.get('rpc-listen-port','')
                if secret and port.isdecimal():
                    tasks={}
                    opener=urllib.request.build_opener(urllib.request.ProxyHandler({}))
                    keys=['gid','status','completedLength','totalLength','errorMessage']
                    for method,args in [('tellActive',[keys]),('tellWaiting',[0,50,keys]),('tellStopped',[0,50,keys])]:
                        body=json.dumps({'jsonrpc':'2.0','id':'diagnostics','method':'aria2.'+method,'params':['token:'+secret]+args}).encode()
                        request=urllib.request.Request('http://127.0.0.1:'+port+'/jsonrpc',data=body,headers={'Content-Type':'application/json'})
                        try:
                            with opener.open(request,timeout=1) as response:
                                tasks[method]=json.load(response).get('result')
                        except Exception as error:
                            tasks[method]={'error':type(error).__name__}
                    (out/'native-tasks.json').write_text(json.dumps(tasks,indent=2)+'\n')
            if probe_version:
                candidates=[]
                bundle=os.environ.get('WIRECTL_BUNDLE_DIR')
                if bundle:
                    candidates.append(Path(bundle)/'libexec/wirectl-download/bin'/('aria2c.exe' if os.name=='nt' else 'aria2c'))
                found=shutil.which('aria2c')
                if found:candidates.append(Path(found))
                for binary in candidates:
                    if binary.is_file():
                        result=subprocess.run([str(binary),'--version'],text=True,capture_output=True,timeout=10)
                        (out/'engine.txt').write_text(str(binary)+'\n'+clean(result.stdout+result.stderr))
                        break
        except Exception as error:
            (out/'capture-error.txt').write_text(type(error).__name__+': '+str(error)+'\n')

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
        job=next(j for j in self.request('GET', '/v1/download/status')['jobs'] if j['id'] == id)
        if job['status']=='unknown': self.diagnostics('native-task-unknown')
        return job

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
        self.diagnostics('before-teardown')
        try:
            self.request('POST', '/v1/runtime/shutdown', {})
            for _ in range(400):
                if not self.dir.joinpath('api.json').exists():
                    break
                time.sleep(.025)
        finally:
            self.diagnostics('after-shutdown')
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
        self.diagnostics('before-crash')
        os.kill(old_pid, signal.SIGTERM if os.name == 'nt' else signal.SIGKILL)
        time.sleep(.1)
        status = self.start_runtime()
        self.assertNotEqual(status['pid'], old_pid)
        self.diagnostics('after-restart')
        self.verify_complete(id)
        self.assertTrue(any(x > 0 for x in self.range_offsets), self.range_offsets)
        self.assertEqual(len(self.request('GET', '/v1/download/status')['jobs']), 1)

    def test_graceful_restart_recovers_partial(self):
        job=self.request('POST','/v1/download/jobs',{'source':self.url})
        id=job['id']
        self.wait(id,lambda j:j['completed']>32768,'partial transfer')
        old_pid=self.ep['pid']
        self.diagnostics('before-orderly-stop')
        self.request('POST','/v1/runtime/shutdown',{})
        for _ in range(1200):
            if not (self.dir/'api.json').exists(): break
            time.sleep(.025)
        self.assertFalse((self.dir/'api.json').exists(),'graceful shutdown did not finish')
        self.diagnostics('after-orderly-stop')
        status=self.start_runtime()
        self.diagnostics('after-restart')
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
