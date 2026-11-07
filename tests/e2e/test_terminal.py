#!/usr/bin/env python3
"""Terminal presentation remains local while all transfer operations use API."""
import json, os
from pathlib import Path
import select, shutil, subprocess, sys, time, unittest, urllib.request
ROOT=Path(__file__).resolve().parents[2]
BINARY=Path(os.environ.get('WIRECTL_TEST_BINARY',ROOT/'bin/wirectl')).resolve()
EVIDENCE=ROOT/'.cache/evidence'
class Terminal(unittest.TestCase):
 @unittest.skipIf(os.name=='nt','POSIX pseudoterminal verification')
 def test_download_dashboard_has_terminal_and_exits_without_stopping_runtime(self):
  import pty
  scratch=ROOT/'.cache/e2e/terminal'
  shutil.rmtree(scratch,ignore_errors=True);scratch.mkdir(parents=True)
  env=dict(os.environ,WIRECTL_HOME=str(scratch))
  start=subprocess.run([str(BINARY),'api','status','--json'],env=env,text=True,capture_output=True,timeout=30)
  self.assertEqual(start.returncode,0,start.stderr)
  master,slave=pty.openpty()
  process=None
  try:
   process=subprocess.Popen([str(BINARY),'download','watch'],stdin=slave,stdout=slave,stderr=slave,env=env,cwd=ROOT)
   os.close(slave);slave=None
   output=b'';deadline=time.monotonic()+10
   while time.monotonic()<deadline and b'\x1b[?1049h' not in output:
    ready,_,_=select.select([master],[],[],.1)
    if ready:
     try:output+=os.read(master,65536)
     except OSError:break
    if process.poll() is not None:break
   self.assertIn(b'\x1b[?1049h',output,output.decode(errors='replace'))
   os.write(master,b'q')
   self.assertEqual(process.wait(timeout=10),0)
   status=subprocess.run([str(BINARY),'api','status','--json'],env=env,text=True,capture_output=True,timeout=30)
   self.assertEqual(json.loads(status.stdout)['pid'],json.loads(start.stdout)['pid'])
  finally:
   if process is not None and process.poll() is None:process.kill();process.wait()
   if slave is not None:os.close(slave)
   os.close(master)
   ep=json.loads((scratch/'api.json').read_text())
   req=urllib.request.Request('http://'+ep['address']+'/v1/runtime/shutdown',data=b'{}',method='POST',headers={'Authorization':'Bearer '+ep['token']})
   with urllib.request.urlopen(req,timeout=10) as r:r.read()
if __name__=='__main__':
 EVIDENCE.mkdir(parents=True,exist_ok=True)
 result=unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(Terminal))
 (EVIDENCE/'terminal-e2e.json').write_text(json.dumps({'tests':result.testsRun,'successful':result.wasSuccessful(),'failures':[str(t) for t,_ in result.failures],'skipped':[str(t) for t,_ in result.skipped]},indent=2)+'\n')
 sys.exit(not result.wasSuccessful())
