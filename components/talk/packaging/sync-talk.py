#!/usr/bin/env python3
"""Fetch a stable official talk definition, verifying both release digest sources."""
import argparse
import hashlib
import json
from pathlib import Path
import re
import subprocess
import urllib.request

p = argparse.ArgumentParser()
p.add_argument('kind', choices=['brew', 'scoop'])
a = p.parse_args()
release = json.loads(subprocess.check_output(['gh', 'api', 'repos/k0ngk0ng/wire-talk/releases/latest']))
tag = release['tag_name']
if release.get('draft') or release.get('prerelease') or not re.fullmatch(r'v\d+\.\d+\.\d+', tag):
    raise SystemExit('only stable releases are accepted')
assets = {v['name']: v for v in release['assets']}
if len(assets) != len(release['assets']):
    raise SystemExit('duplicate assets')
base = f'https://github.com/k0ngk0ng/wire-talk/releases/download/{tag}/'
def digest(name):
    asset = assets[name]
    if asset['browser_download_url'] != base + name:
        raise ValueError('unexpected download URL')
    d = asset.get('digest', '')
    if not re.fullmatch(r'sha256:[0-9a-f]{64}', d):
        raise ValueError('missing GitHub SHA-256')
    return d[7:]
def fetch(name):
    expected = digest(name)
    with urllib.request.urlopen(base + name, timeout=45) as response:
        data = response.read(2**20 + 1)
    if len(data) > 2**20 or hashlib.sha256(data).hexdigest() != expected:
        raise ValueError('definition/checksums differ from GitHub digest')
    return data
sums = {}
for line in fetch('SHA256SUMS').decode().splitlines():
    match = re.fullmatch(r'([0-9a-f]{64})\s+([^/\\]+)', line)
    if not match or match[2] in sums:
        raise ValueError('invalid checksum list')
    sums[match[2]] = match[1]
targets = ['windows-amd64'] if a.kind == 'scoop' else ['darwin-arm64', 'darwin-amd64', 'linux-arm64', 'linux-amd64']
for target in targets:
    filename = f'wire-talk-{tag[1:]}-{target}' + ('.zip' if a.kind == 'scoop' else '.tar.gz')
    if digest(filename) != sums[filename]:
        raise ValueError('archive checksum sources disagree')
name = 'wire-talk.json' if a.kind == 'scoop' else 'wire-talk.rb'
if name not in assets:
    name = 'talk.json' if a.kind == 'scoop' else 'talk.rb'
content = fetch(name)
if a.kind == 'scoop':
    item = json.loads(content)
    if item['version'] != tag[1:]:
        raise ValueError('manifest version mismatch')
    for target in targets:
        filename = f'wire-talk-{tag[1:]}-{target}.zip'
        if item['architecture']['64bit']['hash'] != sums[filename] or item['architecture']['64bit']['url'] != base + filename:
            raise ValueError('manifest does not match verified archive')
    destination = Path('bucket/wire-talk.json')
else:
    text = content.decode()
    if f'version "{tag[1:]}"' not in text:
        raise ValueError('formula version mismatch')
    for target in targets:
        filename = f'wire-talk-{tag[1:]}-{target}.tar.gz'
        if f'url "{base}{filename}"' not in text or f'sha256 "{sums[filename]}"' not in text:
            raise ValueError('formula does not match verified archives')
    # Older releases used the short package name; archives are unchanged.
    if name == 'talk.rb':
        text = text.replace('class Talk < Formula', 'class WireTalk < Formula', 1)
    if not text.startswith('class WireTalk < Formula\n'):
        raise ValueError('unexpected formula class')
    content = text.encode()
    destination = Path('Formula/wire-talk.rb')
previous = destination if destination.exists() else destination.with_name('talk' + destination.suffix)
if previous.exists():
    old = previous.read_text()
    old_version = json.loads(old)['version'] if a.kind == 'scoop' else re.search(r'version "([0-9.]+)"', old)[1]
    if tuple(map(int, old_version.split('.'))) > tuple(map(int, tag[1:].split('.'))):
        raise ValueError('refusing package downgrade')
destination.parent.mkdir(parents=True, exist_ok=True)
destination.write_bytes(content)
if a.kind == 'scoop':
    # Scoop has no formula aliases. The old name becomes a package containing
    # only a dependency, so it never installs a second competing executable.
    legacy = dict(version=tag[1:], description='Compatibility name for wire-talk',
                  homepage='https://github.com/k0ngk0ng/wire-talk', license='MIT',
                  depends='k0ngk0ng/wire-talk',
                  notes='The package is now wire-talk. Use scoop update wire-talk; the command remains wirectl talk.')
    Path('bucket/talk.json').write_text(json.dumps(legacy, indent=2) + '\n')
print(f'Verified {destination} for {tag}')
