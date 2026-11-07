#!/usr/bin/env python3
"""Generate SHA256SUMS, Homebrew formula and Scoop manifest from built assets."""
import argparse
import hashlib
import json
from pathlib import Path
import re

p = argparse.ArgumentParser()
p.add_argument('--version', required=True)
a = p.parse_args()
v = a.version.removeprefix('v')
if not re.fullmatch(r'\d+\.\d+\.\d+(?:-[0-9A-Za-z.-]+)?', v):
    p.error('invalid version')
root = Path(__file__).resolve().parents[1]
dist = root / 'dist'
base = f'https://github.com/k0ngk0ng/wire-talk/releases/download/v{v}'
targets = ['darwin-arm64', 'darwin-amd64', 'linux-amd64', 'linux-arm64', 'windows-amd64']
names = {t: f'wire-talk-{v}-{t}' + ('.zip' if t.startswith('windows') else '.tar.gz') for t in targets}
hashes = {t: hashlib.sha256((dist / n).read_bytes()).hexdigest() for t, n in names.items()}
(dist / 'SHA256SUMS').write_text(''.join(f'{hashes[t]}  {names[t]}\n' for t in sorted(targets)))
formula = f'''class WireTalk < Formula
  desc "Direct encrypted microphone and speaker conversations"
  homepage "https://github.com/k0ngk0ng/wire-talk"
  version "{v}"
  license "MIT"
  depends_on "k0ngk0ng/tap/wirectl"
'''
for system, goos in [('macos', 'darwin'), ('linux', 'linux')]:
    formula += f'  on_{system} do\n'
    for cpu, arch in [('arm', 'arm64'), ('intel', 'amd64')]:
        t = f'{goos}-{arch}'
        formula += f'    on_{cpu} do\n      url "{base}/{names[t]}"\n      sha256 "{hashes[t]}"\n    end\n'
    formula += '  end\n'
formula += '''  def install
    bin.install "bin/wirectl-talk"
    (bash_completion/"wirectl-talk").write Utils.safe_popen_read(bin/"wirectl-talk", "completion", "bash")
    (zsh_completion/"_wirectl-talk").write Utils.safe_popen_read(bin/"wirectl-talk", "completion", "zsh")
    (bin/".wire-talk-package-manager").write "homebrew\\n"
    doc.install "README.md", "THIRD_PARTY_NOTICES.md"
    doc.install "licenses"
  end
  test do
    assert_match version.to_s, shell_output("#{bin}/wirectl-talk version")
    assert_path_exists bash_completion/"wirectl-talk"
    assert_path_exists zsh_completion/"_wirectl-talk"
    system bin/"wirectl-talk", "init", "--state-dir", testpath/"state"
    assert_path_exists testpath/"state/config.json"
  end
end
'''
(dist / 'wire-talk.rb').write_text(formula)
t = 'windows-amd64'
scoop = dict(version=v, description='Direct encrypted microphone and speaker conversations', homepage='https://github.com/k0ngk0ng/wire-talk', license='MIT', depends='k0ngk0ng/wirectl', architecture={'64bit': dict(url=f'{base}/{names[t]}', hash=hashes[t])}, bin='bin/wirectl-talk.exe', post_install="Set-Content -Path \"$dir\\bin\\.wire-talk-package-manager\" -Value 'scoop' -Encoding Ascii", checkver='github', autoupdate={'architecture': {'64bit': {'url': 'https://github.com/k0ngk0ng/wire-talk/releases/download/v$version/wire-talk-$version-windows-amd64.zip'}}, 'hash': {'url': '$baseurl/SHA256SUMS'}})
(dist / 'wire-talk.json').write_text(json.dumps(scoop, indent=2) + '\n')
