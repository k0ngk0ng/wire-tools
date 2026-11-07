#!/usr/bin/env python3
"""Run native checks and retain reproducible logs in .cache/evidence."""
import argparse
import os
from pathlib import Path
import subprocess
import sys
import time

from shell_tools import find_bash

ROOT=Path(__file__).resolve().parents[1]

def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--build-only',action='store_true')
    parser.add_argument('--go-only',action='store_true')
    args=parser.parse_args()
    env=dict(os.environ,CGO_ENABLED='1',GOWORK='off',GOTELEMETRY='off')
    for key,folder in {'GOCACHE':'go-build','GOMODCACHE':'go-mod','GOPATH':'go',
                       'GOTMPDIR':'tmp','TMPDIR':'tmp','TEMP':'tmp','TMP':'tmp'}.items():
        path=ROOT/'.cache'/folder
        path.mkdir(parents=True,exist_ok=True)
        env[key]=str(path)
    evidence=ROOT/'.cache/evidence'
    evidence.mkdir(parents=True,exist_ok=True)
    (ROOT/'bin').mkdir(exist_ok=True)
    suffix='.exe' if os.name=='nt' else ''
    def run(name,command):
        print('+ '+' '.join(map(str,command)),flush=True)
        with (evidence/(name+'.log')).open('w') as log:
            process=subprocess.Popen(list(map(str,command)),cwd=ROOT,env=env,stdout=subprocess.PIPE,stderr=subprocess.STDOUT,text=True,bufsize=1)
            for line in process.stdout:
                print(line,end='',flush=True)
                log.write(line)
            if process.wait()!=0:
                raise SystemExit(process.returncode)
    if args.build_only:
        run('build',['go','build','-trimpath','-o',ROOT/'bin'/('wirectl'+suffix),'./cmd/wirectl'])
        return
    if os.name=='nt':
        # The fetch script verifies its pinned official archive. Resolve its
        # destination inside Bash so MSYS and Windows agree on the same path.
        run('wintun-fetch',[find_bash(env),'-c',
            'exec "$BASH" components/connect/scripts/fetch-wintun.sh "$(pwd -P)/components/connect/.cache/check-wintun"'])
        wintun=ROOT/'components/connect/.cache/check-wintun/wintun.dll'
        if not wintun.is_file():
            raise SystemExit('verified Wintun runtime was not staged')
        env['WIRECTL_TEST_WINTUN']=str(wintun)
    run('shell-selection',[sys.executable,'scripts/test-shell-tools.py'])
    run('go-test',['go','test','-race','-p','1','-timeout','5m','./...'])
    run('go-vet',['go','vet','./...'])
    if args.go_only:
        return
    run('e2e-build',['go','build','-tags','talk_test_audio','-o',ROOT/'bin'/('wirectl-e2e'+suffix),'./cmd/wirectl'])
    env['WIRECTL_TEST_BINARY']=str(ROOT/'bin'/('wirectl-e2e'+suffix))
    run('e2e',[sys.executable,'tests/e2e/test_unified.py'])
    run('download-e2e',[sys.executable,'tests/e2e/test_download_api.py'])
    run('talk-e2e',[sys.executable,'tests/e2e/test_talk_api.py'])
    run('update-e2e',[sys.executable,'tests/e2e/test_update.py'])
    run('terminal-e2e',[sys.executable,'tests/e2e/test_terminal.py'])
    run('package-e2e',[sys.executable,'scripts/test-package.py'])

if __name__=='__main__':
    main()
