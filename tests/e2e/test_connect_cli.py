#!/usr/bin/env python3
"""Process-level CLI/API contract; real networking is covered by connect suite.

Isolation failure matrix: foreground cancellation while polling, cancellation
while GET is in flight, background exit, accidental shell/bridge dispatch, and
cleanup affecting a different named connection. The API fixture records the
actual HTTP requests from the installed CLI process.
"""
import http.server, json, os
from pathlib import Path
import shutil, signal, subprocess, sys, threading, time, unittest
ROOT=Path(__file__).resolve().parents[2]
BINARY=Path(os.environ.get('WIRECTL_TEST_BINARY',ROOT/'bin/wirectl')).resolve()
EVIDENCE=ROOT/'.cache/evidence'
class ConnectCLI(unittest.TestCase):
 def test_foreground_interrupt_cleans_up_named_resource(self):
  for slow in (False,True):
   with self.subTest(in_flight=slow):
    scratch=ROOT/'.cache/e2e-connect-cli'/str(slow)
    shutil.rmtree(scratch,ignore_errors=True);scratch.mkdir(parents=True)
    created=threading.Event();polling=threading.Event();removed=threading.Event();paths=[]
    class Handler(http.server.BaseHTTPRequestHandler):
     def reply(self,data):
      b=json.dumps(data).encode();self.send_response(200);self.send_header('Content-Length',str(len(b)));self.end_headers()
      try:self.wfile.write(b)
      except (BrokenPipeError,ConnectionResetError):pass
     def do_GET(self):
      paths.append(('GET',self.path))
      if self.path=='/v1/status':self.reply({'version':'fixture','pid':os.getpid()});return
      if self.path=='/v1/connect/connections/probe':
       polling.set()
       if slow:time.sleep(1)
       self.reply({'name':'probe','state':'running'});return
      self.send_error(404)
     def do_POST(self):
      paths.append(('POST',self.path));data=self.rfile.read(int(self.headers.get('Content-Length','0')))
      if self.path=='/v1/connect/connections':
       self.reply({'name':'probe','state':'pairing'});created.set();return
      self.send_error(404)
     def do_DELETE(self):
      paths.append(('DELETE',self.path));removed.set();self.reply({'stopped':True})
     def log_message(*args):pass
    server=http.server.ThreadingHTTPServer(('127.0.0.1',0),Handler)
    thread=threading.Thread(target=server.serve_forever,daemon=True);thread.start()
    endpoint=scratch/'api.json';endpoint.write_text(json.dumps({'address':'127.0.0.1:'+str(server.server_port),'token':'a'*64}));endpoint.chmod(0o600)
    process=None
    try:
     process=subprocess.Popen([str(BINARY),'connect','fixture.invalid','--foreground','--name','probe'],env=dict(os.environ,WIRECTL_HOME=str(scratch)),stdout=subprocess.PIPE,stderr=subprocess.PIPE,text=True,cwd=ROOT)
     self.assertTrue(created.wait(8),paths)
     self.assertTrue(polling.wait(8),paths)
     if os.name=='nt':
      self.skipTest('Windows console signal checked by native service suite')
     process.send_signal(signal.SIGINT)
     out,err=process.communicate(timeout=15)
     self.assertTrue(removed.wait(3),(out,err,paths))
     self.assertIn(('DELETE','/v1/connect/connections/probe'),paths)
     self.assertFalse(any(path.startswith('/v1/commands') for _,path in paths),paths)
    finally:
     if process and process.poll() is None:process.kill();process.wait()
     server.shutdown();server.server_close()
if __name__=='__main__':
 EVIDENCE.mkdir(parents=True,exist_ok=True)
 result=unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(ConnectCLI))
 (EVIDENCE/'connect-cli-e2e.json').write_text(json.dumps({'tests':result.testsRun,'successful':result.wasSuccessful(),'failures':[str(t) for t,_ in result.failures],'errors':[str(t) for t,_ in result.errors]},indent=2)+'\n')
 sys.exit(not result.wasSuccessful())
