#!/usr/bin/env python3
"""Verify whole-package updates using real binaries and separate API processes."""
import hashlib
import io
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import time
import unittest
import urllib.request
import zipfile

ROOT=Path(__file__).resolve().parents[2]
BINARY=Path(os.environ.get('WIRECTL_TEST_BINARY',ROOT/'bin/wirectl')).resolve()
SCRATCH=ROOT/'.cache/e2e/update-process'
EVIDENCE=ROOT/'.cache/evidence'
NEW_VERSION='9.8.7'
PROOF=b'unified dependency generation\n'

class UpdateProcesses(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        SCRATCH.mkdir(parents=True,exist_ok=True)
        EVIDENCE.mkdir(parents=True,exist_ok=True)
        cls.fixtures=SCRATCH/'fixtures'
        shutil.rmtree(cls.fixtures,ignore_errors=True)
        cls.fixtures.mkdir()
        env=dict(os.environ,CGO_ENABLED='1',GOWORK='off',GOTELEMETRY='off')
        for key,folder in {'GOCACHE':'go-build','GOMODCACHE':'go-mod','GOPATH':'go',
                           'GOTMPDIR':'tmp','TMPDIR':'tmp','TEMP':'tmp','TMP':'tmp'}.items():
            path=ROOT/'.cache'/folder
            path.mkdir(parents=True,exist_ok=True)
            env[key]=str(path)
        cls.new_binary=cls.fixtures/('wirectl.exe' if os.name=='nt' else 'wirectl')
        with (EVIDENCE/'update-build.log').open('w') as log:
            subprocess.run(['go','build','-tags','talk_test_audio','-ldflags','-X main.version='+NEW_VERSION,
                            '-o',str(cls.new_binary),'./cmd/wirectl'],cwd=ROOT,env=env,
                           stdout=log,stderr=subprocess.STDOUT,check=True,timeout=300)
        runtime=cls.fixtures/'runtime/libexec/wirectl-download'
        runtime.mkdir(parents=True)
        (runtime/'update-proof.txt').write_bytes(PROOF)
        cls.dist=cls.fixtures/'dist'
        subprocess.run([sys.executable,'scripts/package.py','--version',NEW_VERSION,
                        '--binary',str(cls.new_binary),'--runtime-dir',str(cls.fixtures/'runtime'),
                        '--output',str(cls.dist)],cwd=ROOT,env=env,check=True,timeout=60)
        cls.archive=next(p for p in cls.dist.iterdir() if p.suffix in ('.gz','.zip'))

    def setUp(self):
        self.path=SCRATCH/self._testMethodName
        shutil.rmtree(self.path,ignore_errors=True)
        (self.path/'install/bin').mkdir(parents=True)
        self.binary=self.path/'install/bin'/('wirectl.exe' if os.name=='nt' else 'wirectl')
        shutil.copy2(BINARY,self.binary)
        self.before=hashlib.sha256(self.binary.read_bytes()).hexdigest()
        self.env=dict(os.environ,WIRECTL_HOME=str(self.path/'state'),NO_PROXY='127.0.0.1,localhost')

    def cli(self,*args,ok=True):
        p=subprocess.run([str(self.binary),*map(str,args)],env=self.env,cwd=ROOT,
                         capture_output=True,text=True,timeout=60)
        if ok:
            self.assertEqual(p.returncode,0,p.stdout+p.stderr)
        return p

    def tearDown(self):
        endpoint=self.path/'state/api.json'
        if endpoint.exists():
            try:
                ep=json.loads(endpoint.read_text())
                req=urllib.request.Request('http://'+ep['address']+'/v1/runtime/shutdown',
                    data=b'{}',method='POST',headers={'Authorization':'Bearer '+ep['token'],'Content-Type':'application/json'})
                with urllib.request.build_opener(urllib.request.ProxyHandler({})).open(req,timeout=10) as response:
                    response.read()
                for _ in range(400):
                    if not endpoint.exists():
                        break
                    time.sleep(.05)
            except (OSError,ValueError):
                pass

    def unchanged(self):
        self.assertEqual(hashlib.sha256(self.binary.read_bytes()).hexdigest(),self.before)
        self.assertFalse((self.path/'install/.wirectl/versions'/NEW_VERSION).exists())

    def test_bad_checksum_leaves_installation_unchanged(self):
        bad=self.path/'SHA256SUMS'
        bad.write_text('0'*64+'  '+self.archive.name+'\n')
        p=self.cli('update','--archive',self.archive,'--checksums',bad,ok=False)
        self.assertNotEqual(p.returncode,0)
        self.unchanged()

    def test_verified_update_replaces_program_and_dependency_generation(self):
        old=json.loads(self.cli('api','status','--json').stdout)
        self.cli('update','--archive',self.archive,'--checksums',self.dist/'SHA256SUMS')
        self.assertEqual(self.cli('version').stdout.strip(),NEW_VERSION)
        generation=self.path/'install/.wirectl/versions'/NEW_VERSION
        self.assertEqual((generation/'libexec/wirectl-download/update-proof.txt').read_bytes(),PROOF)
        self.assertNotEqual(hashlib.sha256(self.binary.read_bytes()).hexdigest(),self.before)
        new=json.loads(self.cli('api','status','--json').stdout)
        self.assertEqual(new['version'],NEW_VERSION)
        self.assertNotEqual(old['pid'],new['pid'])

    def test_checksums_do_not_allow_path_traversal(self):
        archive=self.path/self.archive.name
        if archive.suffix=='.zip':
            with zipfile.ZipFile(archive,'w') as z:
                z.writestr('../escaped.txt',b'must never be extracted')
        else:
            with tarfile.open(archive,'w:gz') as t:
                data=b'must never be extracted'
                entry=tarfile.TarInfo('../escaped.txt')
                entry.size=len(data)
                t.addfile(entry,io.BytesIO(data))
        sums=self.path/'SHA256SUMS'
        sums.write_text(hashlib.sha256(archive.read_bytes()).hexdigest()+'  '+archive.name+'\n')
        p=self.cli('update','--archive',archive,'--checksums',sums,ok=False)
        self.assertNotEqual(p.returncode,0)
        self.unchanged()
        self.assertFalse((self.path.parent/'escaped.txt').exists())
        self.assertFalse((self.path/'escaped.txt').exists())

    @unittest.skipUnless(os.name=='nt','Windows Scoop PowerShell delegation')
    def test_windows_scoop_uses_powershell_command(self):
        tools=self.path/'tools'
        tools.mkdir()
        log=self.path/'scoop-call.json'
        script=tools/'scoop.ps1'
        script.write_text('$args | ConvertTo-Json -Compress | Set-Content -Path $env:WIRECTL_MANAGER_LOG\nexit 0\n')
        (self.binary.parent/'.wirectl-package-manager').write_text('scoop\n')
        self.env['PATH']=str(tools)+os.pathsep+self.env.get('PATH','')
        self.env['WIRECTL_MANAGER_LOG']=str(log)
        self.cli('update')
        self.assertEqual(json.loads(log.read_text(encoding='utf-8-sig')),['update','wirectl'])
        self.unchanged()

    @unittest.skipIf(os.name=='nt','package manager fixture uses a POSIX executable')
    def test_managed_installation_delegates_from_same_update_command(self):
        manager=self.path/'tools'
        manager.mkdir()
        log=self.path/'package-manager-call.json'
        brew=manager/'brew'
        # No shell expands user data: the fixture receives its log path through environment.
        brew.write_text('#!/usr/bin/env python3\nimport json,os,sys\nopen(os.environ["WIRECTL_MANAGER_LOG"],"w").write(json.dumps(sys.argv[1:]))\n')
        brew.chmod(0o755)
        (self.binary.parent/'.wirectl-package-manager').write_text('homebrew\n')
        self.env['PATH']=str(manager)+os.pathsep+self.env.get('PATH','')
        self.env['WIRECTL_MANAGER_LOG']=str(log)
        self.cli('update')
        self.assertEqual(json.loads(log.read_text()),['upgrade','k0ngk0ng/wire-tools/wirectl'])
        self.unchanged()

if __name__=='__main__':
    EVIDENCE.mkdir(parents=True,exist_ok=True)
    result=unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(UpdateProcesses))
    (EVIDENCE/'update-e2e.json').write_text(json.dumps({'suite':'whole-package-update-e2e','tests':result.testsRun,'successful':result.wasSuccessful(),'failures':[{'test':str(t),'detail':detail} for t,detail in result.failures],'errors':[{'test':str(t),'detail':detail} for t,detail in result.errors]},indent=2)+'\n')
    sys.exit(not result.wasSuccessful())
