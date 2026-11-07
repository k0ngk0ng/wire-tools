#!/usr/bin/env python3
"""Build a native release archive; all build scratch stays in this checkout."""
import argparse
import hashlib
import os
from pathlib import Path
import re
import shutil
import subprocess
import tarfile
import zipfile

root = Path(__file__).resolve().parents[1]
p = argparse.ArgumentParser()
p.add_argument('--version', required=True)
a = p.parse_args()
version = a.version.removeprefix('v')
if not re.fullmatch(r'(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)\.(0|[1-9][0-9]*)(-[0-9A-Za-z.-]+)?', version):
    p.error('version must be SemVer')
env = dict(os.environ)
for var, folder in [('GOCACHE', 'go-build'), ('GOMODCACHE', 'go-mod'), ('GOTMPDIR', 'tmp'), ('TMPDIR', 'tmp')]:
    dest = root / '.cache' / folder
    dest.mkdir(parents=True, exist_ok=True)
    env[var] = str(dest)
goos, arch = subprocess.check_output(['go', 'env', 'GOOS', 'GOARCH'], env=env, text=True).split()
name = f'wire-talk-{version}-{goos}-{arch}'
stage = root / '.cache' / 'packages' / name
stage.mkdir(parents=True, exist_ok=True)
(stage / 'bin').mkdir(exist_ok=True)
suffix = '.exe' if goos == 'windows' else ''
for command in ['wirectl', 'wirectl-talk']:
    subprocess.run(['go', 'build', '-trimpath', '-ldflags', f'-s -w -X main.version={version}', '-o', str(stage / 'bin' / (command + suffix)), './cmd/' + command], cwd=root, env=env, check=True)
for file in ['README.md', 'LICENSE', 'THIRD_PARTY_NOTICES.md']:
    shutil.copyfile(root / file, stage / file)
licenses = stage / 'licenses'
licenses.mkdir(exist_ok=True)
malgo = root / '.cache' / 'go-mod' / 'github.com' / 'gen2brain' / 'malgo@v0.11.24'
shutil.copyfile(malgo / 'LICENSE', licenses / 'malgo-LICENSE')
sys_license = root / '.cache' / 'go-mod' / 'golang.org' / 'x' / 'sys@v0.31.0' / 'LICENSE'
if sys_license.exists():
    shutil.copyfile(sys_license, licenses / 'x-sys-LICENSE')
for module, label in [
    ('github.com/gopxl/beep@v1.4.1', 'beep'),
    ('github.com/hajimehoshi/go-mp3@v0.3.4', 'go-mp3'),
    ('github.com/mewkiz/flac@v1.0.8', 'flac'),
    ('github.com/icza/bitio@v1.1.0', 'bitio'),
    ('github.com/pkg/errors@v0.9.1', 'pkg-errors'),
    ('github.com/schollz/pake/v3@v3.2.0', 'pake'),
    ('filippo.io/edwards25519@v1.2.0', 'edwards25519'),
    ('github.com/tscholl2/siec@v0.0.0-20240310163802-c2c6f6198406', 'siec'),
]:
    shutil.copyfile(root / '.cache' / 'go-mod' / module / 'LICENSE', licenses / (label + '-LICENSE'))
shutil.copyfile(root / '.cache/go-mod/github.com/mewkiz/pkg@v0.0.0-20230226050401-4010bf0fec14/UNLICENSE', licenses / 'mewkiz-pkg-UNLICENSE')
goroot = Path(subprocess.check_output(['go', 'env', 'GOROOT'], env=env, text=True).strip())
# Homebrew keeps the Go license above its libexec GOROOT.
go_license = goroot / 'LICENSE'
if not go_license.exists() and goroot.name == 'libexec':
    go_license = goroot.parent / 'LICENSE'
shutil.copyfile(go_license, licenses / 'Go-LICENSE')
# miniaudio is embedded in malgo and carries its own dual public-domain/MIT notice.
header = (malgo / 'miniaudio.h').read_text()
(licenses / 'miniaudio-LICENSE.txt').write_text(header[header.rfind('ALTERNATIVE 1 - Public Domain'):])
(root / 'dist').mkdir(exist_ok=True)
files = sorted(f for f in stage.rglob('*') if f.is_file())
if goos == 'windows':
    archive = root / 'dist' / (name + '.zip')
    with zipfile.ZipFile(archive, 'w', zipfile.ZIP_DEFLATED) as z:
        for file in files:
            z.write(file, file.relative_to(stage).as_posix())
else:
    archive = root / 'dist' / (name + '.tar.gz')
    with tarfile.open(archive, 'w:gz') as t:
        for file in files:
            t.add(file, arcname=file.relative_to(stage).as_posix())
print(f'{hashlib.sha256(archive.read_bytes()).hexdigest()}  {archive.name}')
