#!/usr/bin/env python3
"""Verify an actual installed archive, runtime assets and independent CLI process."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import shutil
import subprocess
import sys
import tarfile
import time
import urllib.request
import zipfile

ROOT=Path(__file__).resolve().parents[1]
p=argparse.ArgumentParser(description=__doc__)
p.add_argument('archive',type=Path)
a=p.parse_args()
work=ROOT/'.cache/installed-package-e2e'/a.archive.stem
shutil.rmtree(work,ignore_errors=True)
work.mkdir(parents=True)
install=work/'installation'
install.mkdir()
if a.archive.suffix=='.zip':
    with zipfile.ZipFile(a.archive) as z:
        for member in z.infolist():
            target=(install/member.filename).resolve()
            if not target.is_relative_to(install):
                raise SystemExit('unsafe archive member')
        z.extractall(install)
else:
    with tarfile.open(a.archive) as t:
        for member in t.getmembers():
            target=(install/member.name).resolve()
            if not target.is_relative_to(install) or not (member.isfile() or member.isdir()):
                raise SystemExit('unsafe archive member')
        t.extractall(install)
metadata=json.loads((install/'wirectl-package.json').read_text())
for name,digest in metadata['files'].items():
    assert hashlib.sha256((install/name).read_bytes()).hexdigest()==digest,name
binary=install/'bin'/('wirectl.exe' if os.name=='nt' else 'wirectl')
env=dict(os.environ,WIRECTL_HOME=str(work/'state'))
assert subprocess.check_output([str(binary),'version'],env=env,text=True).strip()==metadata['version']
help_text=subprocess.check_output([str(binary),'--help'],env=env,text=True)
for command in ('connect','download','talk','update'):
    assert command in help_text,command
try:
    status=json.loads(subprocess.check_output([str(binary),'api','status','--json'],env=env,text=True,timeout=60))
    assert status['version']==metadata['version'],status
    assert status['pid']>0,status
finally:
    endpoint=work/'state/api.json'
    if endpoint.is_file():
        ep=json.loads(endpoint.read_text())
        req=urllib.request.Request('http://'+ep['address']+'/v1/runtime/shutdown',data=b'{}',method='POST',headers={'Authorization':'Bearer '+ep['token'],'Content-Type':'application/json'})
        with urllib.request.urlopen(req,timeout=30) as response:
            response.read()
        for _ in range(400):
            if not endpoint.exists():
                break
            time.sleep(.05)
        assert not endpoint.exists(),'runtime did not shut down'
evidence=ROOT/'.cache/evidence'
evidence.mkdir(parents=True,exist_ok=True)
(evidence/'installed-package.json').write_text(json.dumps({'suite':'installed-package-e2e','archive':a.archive.name,'version':metadata['version'],'platform':metadata['platform'],'files_verified':len(metadata['files']),'successful':True},indent=2)+'\n')
print('Installed package verified:',a.archive.name)
