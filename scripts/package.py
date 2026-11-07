#!/usr/bin/env python3
"""Build one native wirectl distribution with reproducible repository-local staging."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import shutil
import subprocess
import sys
import tarfile
import zipfile

ROOT = Path(__file__).resolve().parents[1]
TARGETS = ('darwin-arm64', 'linux-amd64', 'linux-arm64', 'windows-amd64')
VERSION = re.compile(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(?:-[0-9A-Za-z.-]+)?\Z')


def local(path):
    path = Path(path).resolve()
    if not path.is_relative_to(ROOT):
        raise ValueError('output must stay inside this checkout: '+str(path))
    return path


def environment():
    env = dict(os.environ)
    for key, value in {'GOCACHE':'go-build','GOMODCACHE':'go-mod','GOPATH':'go',
                       'GOTMPDIR':'tmp','TMPDIR':'tmp','TEMP':'tmp','TMP':'tmp'}.items():
        path=ROOT/'.cache'/value
        path.mkdir(parents=True,exist_ok=True)
        env[key]=str(path)
    env['CGO_ENABLED']='1'
    env['GOTELEMETRY']='off'
    env['GOWORK']='off'
    return env


def run(args, env):
    subprocess.run(list(map(str,args)),cwd=ROOT,env=env,check=True)


def native_target(env):
    return '-'.join(subprocess.check_output(['go','env','GOOS','GOARCH'],env=env,text=True).split())


def copy_runtime(source, destination):
    if source is None:
        return
    source = Path(source).resolve()
    for path in sorted(source.rglob('*')):
        if path.is_symlink():
            raise ValueError('runtime assets must be ordinary files: '+str(path))
        target=destination/path.relative_to(source)
        if path.is_dir():
            target.mkdir(parents=True,exist_ok=True)
        elif path.is_file():
            target.parent.mkdir(parents=True,exist_ok=True)
            shutil.copy2(path,target)


def notices(stage, env, binary_only):
    for name in ('README.md','LICENSE','THIRD_PARTY_NOTICES.md'):
        source=ROOT/name
        if not source.is_file():
            raise ValueError('required release notice is missing: '+name)
        shutil.copy2(source,stage/name)
    license_dir=stage/'licenses'
    license_dir.mkdir(exist_ok=True)
    for component in ('connect','talk'):
        for name in ('LICENSE','THIRD_PARTY_NOTICES.md'):
            shutil.copy2(ROOT/'components'/component/name, license_dir/(component+'-'+name))
    if binary_only:
        return
    # Inventory only the modules compiled into the released executable.
    data=subprocess.check_output(['go','list','-deps','-json','./cmd/wirectl'],cwd=ROOT,env=env,text=True)
    decoder=json.JSONDecoder()
    dependencies={}
    while data.strip():
        data=data.lstrip()
        item,end=decoder.raw_decode(data)
        data=data[end:]
        module=item.get('Module',{})
        if module.get('Main') or not module.get('Dir'):
            continue
        dependencies[module['Path']]=module
    inventory=[]
    for name,module in sorted(dependencies.items()):
        destination=license_dir/re.sub(r'[^A-Za-z0-9._-]','_',name)
        destination.mkdir(exist_ok=True)
        directory=Path(module['Dir'])
        found=[]
        for filename in ('LICENSE','LICENSE.txt','LICENSE.md','LICENCE','COPYING','COPYRIGHT','UNLICENSE'):
            if (directory/filename).is_file():
                shutil.copy2(directory/filename,destination/filename)
                found.append(filename)
        if not found:
            raise ValueError('dependency is missing a root license notice: '+name)
        if name=='github.com/gen2brain/malgo':
            header=(directory/'miniaudio.h').read_text()
            (destination/'miniaudio-LICENSE.txt').write_text(header[header.rfind('ALTERNATIVE 1 - Public Domain'):])
        inventory.append({'module':name,'version':module.get('Version',''),'notices':found})
    (license_dir/'dependencies.json').write_text(json.dumps(inventory,indent=2)+'\n')
    goroot=Path(subprocess.check_output(['go','env','GOROOT'],env=env,text=True).strip())
    go_license=goroot/'LICENSE'
    if not go_license.is_file() and goroot.name=='libexec':
        go_license=goroot.parent/'LICENSE'
    shutil.copy2(go_license,license_dir/'Go-LICENSE')


def bundle_full(stage, target, env):
    if target.startswith('windows'):
        wintun_stage=ROOT/'components/connect/.cache/package-wintun'
        wintun_stage.mkdir(parents=True,exist_ok=True)
        # Resolve the destination in the same MSYS shell as the fetch script;
        # a Windows-native C:\ path would fail its POSIX repository guard.
        run(['bash','-c','exec bash components/connect/scripts/fetch-wintun.sh "$(pwd -P)/components/connect/.cache/package-wintun"'],env)
        shutil.copy2(wintun_stage/'wintun.dll',stage/'bin/wintun.dll')
        shutil.copy2(wintun_stage/'WINTUN-LICENSE.txt',stage/'bin/WINTUN-LICENSE.txt')
        shutil.copy2(wintun_stage/'WINTUN-LICENSE.txt',stage/'licenses/WINTUN-LICENSE.txt')
        return
    engine_stage=stage/'libexec/wirectl-download'
    run(['sh','components/download/scripts/build-engines.sh'],env)
    run([sys.executable,'components/download/scripts/bundle-engines.py',engine_stage],env)
    run([sys.executable,'components/download/scripts/bundle-video.py',engine_stage],env)
    downloads=ROOT/'components/download/.cache'
    for source,name in (
        ('engines/aria2-1.37.0/COPYING','aria2-COPYING'),
        ('amule-build/src/aMule-2.3.3/docs/COPYING','amule-COPYING'),
        ('amule-build/src/wxWidgets-3.0.5.1/docs/licence.txt','wxWidgets-LICENSE'),
        ('amule-build/src/wxWidgets-3.0.5.1/docs/lgpl.txt','wxWidgets-LGPL'),
        ('amule-build/src/cryptopp-CRYPTOPP_8_9_0/License.txt','CryptoPP-LICENSE'),
        ('amule-build/src/boost_1_74_0/LICENSE_1_0.txt','boost-LICENSE')):
        shutil.copy2(downloads/source,stage/'licenses'/name)


def release_sources(env):
    status=subprocess.check_output(['git','status','--porcelain','--untracked-files=all'],cwd=ROOT,env=env,text=True)
    if status.strip():
        raise ValueError('full source packaging requires a clean committed checkout; commit public source changes first')
    names=subprocess.check_output(['git','ls-files','-z'],cwd=ROOT,env=env).decode().split('\0')
    return [name for name in names if name]


def source_archive(dist, version, stage, env):
    scratch=ROOT/'.cache/packages'/('wirectl-'+version+'-sources')
    shutil.rmtree(scratch,ignore_errors=True)
    scratch.mkdir(parents=True)
    source=scratch/'wire-tools'
    source.mkdir()
    # Source archives are public release assets. Only a clean committed tree
    # may be paired with a release executable; unrelated worktree files cannot
    # enter the source asset and untracked Go files cannot be silently omitted.
    tracked = release_sources(env)
    for relative in tracked:
        path=ROOT/relative
        if path.is_symlink() or not path.is_file():
            raise ValueError('release source must be an ordinary tracked file: '+relative)
        target=source/relative
        target.parent.mkdir(parents=True,exist_ok=True)
        shutil.copy2(path,target)
    upstream=source/'components/upstream'
    upstream.mkdir(parents=True,exist_ok=True)
    cache=ROOT/'components/download/.cache'
    files=list((cache/'amule-build/downloads').glob('*'))
    files+=list((cache/'engines').glob('aria2-*.tar.xz'))
    files+=list((cache/'video').glob('ffmpeg-*.tar.xz'))
    files+=list((cache/'video').glob('yt-dlp-*.tar.gz'))
    if not files:
        raise ValueError('corresponding source archives were not produced')
    for path in files:
        if path.is_file() and not path.name.endswith('.partial'):
            shutil.copy2(path,upstream/path.name)
    libraries=stage/'libexec/wirectl-download/runtime-libraries.json'
    if libraries.exists():
        run([sys.executable,'components/download/scripts/bundle-runtime-sources.py',libraries,upstream/'debian'],env)
    archive=dist/('wirectl-'+version+'-sources.tar.gz')
    with tarfile.open(archive,'w:gz') as package:
        package.add(source,arcname='wire-tools')
    (stage/'licenses/SOURCES.txt').write_text('Corresponding sources: '+archive.name+'\nBuild recipes and pinned patches: wire-tools/components/download/scripts/\n')
    return archive


def checksums(dist, version):
    assets=sorted(list(dist.glob('wirectl-'+version+'-*.tar.gz'))+list(dist.glob('wirectl-'+version+'-*.zip')))
    (dist/'SHA256SUMS').write_text(''.join(hashlib.sha256(p.read_bytes()).hexdigest()+'  '+p.name+'\n' for p in assets))


def main():
    parser=argparse.ArgumentParser(description=__doc__)
    parser.add_argument('--version',required=True)
    parser.add_argument('--binary',type=Path,help='package an already-built native executable')
    parser.add_argument('--runtime-dir',type=Path,help='merge an already-staged runtime tree')
    parser.add_argument('--output',type=Path,default=ROOT/'dist')
    parser.add_argument('--full',action='store_true',help='include pinned native engines and corresponding sources')
    args=parser.parse_args()
    version=args.version.removeprefix('v')
    if not VERSION.fullmatch(version):
        parser.error('version must be SemVer, for example 1.2.3')
    dist=local(args.output)
    env=environment()
    target=native_target(env)
    if target not in TARGETS:
        parser.error('unsupported release target: '+target)
    if args.full and not target.startswith('windows'):
        release_sources(env)
    name='wirectl-'+version+'-'+target
    stage=ROOT/'.cache/packages'/name
    shutil.rmtree(stage,ignore_errors=True)
    (stage/'bin').mkdir(parents=True)
    copy_runtime(args.runtime_dir,stage)
    suffix='.exe' if target.startswith('windows') else ''
    binary=stage/'bin'/('wirectl'+suffix)
    if args.binary:
        shutil.copy2(args.binary,binary)
    else:
        run(['go','build','-trimpath','-buildvcs=false','-ldflags','-s -w -X main.version='+version,
             '-o',binary,'./cmd/wirectl'],env)
    notices(stage,env,bool(args.binary))
    dist.mkdir(parents=True,exist_ok=True)
    if args.full:
        bundle_full(stage,target,env)
        if not target.startswith('windows'):
            source_archive(dist,version,stage,env)
    files=[p for p in sorted(stage.rglob('*')) if p.is_file()]
    metadata={'name':'wirectl','version':version,'platform':target,'full_runtime':args.full,
              'files':{p.relative_to(stage).as_posix():hashlib.sha256(p.read_bytes()).hexdigest() for p in files}}
    (stage/'wirectl-package.json').write_text(json.dumps(metadata,indent=2)+'\n')
    files=[p for p in sorted(stage.rglob('*')) if p.is_file()]
    archive=dist/(name+('.zip' if target.startswith('windows') else '.tar.gz'))
    if target.startswith('windows'):
        with zipfile.ZipFile(archive,'w',zipfile.ZIP_DEFLATED) as package:
            for path in files:
                package.write(path,path.relative_to(stage).as_posix())
    else:
        with tarfile.open(archive,'w:gz') as package:
            for path in files:
                package.add(path,arcname=path.relative_to(stage).as_posix())
    checksums(dist,version)
    print(str(archive.relative_to(ROOT)))

if __name__=='__main__':
    try:
        main()
    except (ValueError,OSError,subprocess.CalledProcessError) as error:
        raise SystemExit(str(error))
