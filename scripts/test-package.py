#!/usr/bin/env python3
"""Exercise the packaging CLI against real archives; never writes outside checkout."""
import hashlib
import json
import os
from pathlib import Path
import platform
import shutil
import subprocess
import sys
import tarfile
import unittest

ROOT = Path(__file__).resolve().parents[1]
SCRATCH = ROOT / '.cache/package-e2e'
TARGETS = ('darwin-arm64', 'linux-amd64', 'linux-arm64', 'windows-amd64')

class PackageE2E(unittest.TestCase):
    def setUp(self):
        self.work = SCRATCH / self._testMethodName
        shutil.rmtree(self.work, ignore_errors=True)
        self.work.mkdir(parents=True)

    def run_script(self, script, *args, ok=True):
        p = subprocess.run([sys.executable, str(ROOT/'scripts'/script), *map(str,args)],
                           cwd=ROOT, capture_output=True, text=True, timeout=60)
        if ok:
            self.assertEqual(p.returncode, 0, p.stdout+p.stderr)
        return p

    @unittest.skipIf(os.name == 'nt', 'fixture is a POSIX executable')
    def test_native_package_round_trip(self):
        binary = self.work/'wirectl'
        binary.write_text('#!/bin/sh\nprintf "9.8.7\\n"\n')
        binary.chmod(0o755)
        runtime = self.work/'runtime'
        (runtime/'libexec/wirectl-download/bin').mkdir(parents=True)
        (runtime/'libexec/wirectl-download/bin/fixture-engine').write_bytes(b'engine fixture')
        out = self.work/'dist'
        self.run_script('package.py', '--version','9.8.7','--binary',binary,
                        '--runtime-dir',runtime,'--output',out)
        name = next(out.glob('*.tar.gz'))
        sums = (out/'SHA256SUMS').read_text()
        self.assertIn(hashlib.sha256(name.read_bytes()).hexdigest()+'  '+name.name,sums)
        extracted = self.work/'install'
        extracted.mkdir()
        with tarfile.open(name) as package:
            package.extractall(extracted, filter='data')
        self.assertEqual(subprocess.check_output([str(extracted/'bin/wirectl')],text=True),'9.8.7\n')
        self.assertEqual((extracted/'libexec/wirectl-download/bin/fixture-engine').read_bytes(),b'engine fixture')
        self.assertEqual(json.loads((extracted/'wirectl-package.json').read_text())['version'],'9.8.7')
        self.assertFalse((extracted/'bin/wirectl-download').exists())

    def releases(self):
        dist = self.work/'dist'
        dist.mkdir()
        lines=[]
        for target in TARGETS:
            name='wirectl-9.8.7-'+target+('.zip' if target.startswith('windows') else '.tar.gz')
            path=dist/name
            path.write_bytes(('real checksum fixture '+target).encode())
            lines.append(hashlib.sha256(path.read_bytes()).hexdigest()+'  '+name)
        (dist/'SHA256SUMS').write_text('\n'.join(lines)+'\n')
        return dist

    def test_manifests_use_verified_archives(self):
        dist = self.releases()
        generated=self.work/'generated'
        self.run_script('manifests.py','--version','9.8.7','--dist',dist,'--output',generated)
        scoop=json.loads((generated/'bucket/wirectl.json').read_text())
        self.assertEqual(scoop['version'],'9.8.7')
        self.assertEqual(scoop['architecture']['64bit']['hash'],hashlib.sha256((dist/'wirectl-9.8.7-windows-amd64.zip').read_bytes()).hexdigest())
        formula=(generated/'Formula/wirectl.rb').read_text()
        self.assertIn('libexec.install',formula)
        self.assertNotIn('depends_on "wirectl"',formula)
        self.assertIn('wire-tools/releases/download/v9.8.7',formula)

    def test_manifests_reject_tampered_archive(self):
        dist=self.releases()
        (dist/'wirectl-9.8.7-linux-arm64.tar.gz').write_bytes(b'tampered')
        p=self.run_script('manifests.py','--version','9.8.7','--dist',dist,
                          '--output',self.work/'generated',ok=False)
        self.assertNotEqual(p.returncode,0)
        self.assertFalse((self.work/'generated/bucket/wirectl.json').exists())

    def test_invalid_version_does_not_create_package(self):
        p=self.run_script('package.py','--version','../../bad','--output',self.work/'dist',ok=False)
        self.assertNotEqual(p.returncode,0)
        self.assertFalse((self.work/'dist').exists())

if __name__=='__main__':
    SCRATCH.mkdir(parents=True,exist_ok=True)
    evidence=ROOT/'.cache/evidence'
    evidence.mkdir(parents=True,exist_ok=True)
    result=unittest.TextTestRunner(verbosity=2).run(unittest.defaultTestLoader.loadTestsFromTestCase(PackageE2E))
    (evidence/'package-e2e.json').write_text(json.dumps({'suite':'package-e2e','tests':result.testsRun,'successful':result.wasSuccessful(),'failures':[str(t) for t,_ in result.failures],'errors':[str(t) for t,_ in result.errors]},indent=2)+'\n')
    sys.exit(not result.wasSuccessful())
