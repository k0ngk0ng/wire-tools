#!/usr/bin/env bash
# Run only in a disposable GitHub-hosted runner; installs a staged release with Homebrew.
set -euo pipefail
test "${GITHUB_ACTIONS:-}" = true
root="$(pwd)"
export HOMEBREW_NO_AUTO_UPDATE=1 HOMEBREW_NO_ANALYTICS=1
export HOMEBREW_CACHE="$root/.cache/homebrew" HOMEBREW_LOGS="$root/.cache/homebrew-logs" HOMEBREW_TEMP="$root/.cache/tmp"
mkdir -p "$HOMEBREW_CACHE" "$HOMEBREW_LOGS" "$HOMEBREW_TEMP"
brew tap k0ngk0ng/wire-tools "${GITHUB_SERVER_URL}/${GITHUB_REPOSITORY}"
tap="$(brew --repository k0ngk0ng/wire-tools)"
python3 - "$root/.cache/release/wirectl.rb" "$tap/Formula/wirectl.rb" "$root/.cache/release" <<'PY'
from pathlib import Path
import re,sys
source,target,assets=map(Path,sys.argv[1:])
text=source.read_text()
text=re.sub(r'https://github\.com/k0ngk0ng/wire-tools/releases/download/v[^/]+/',assets.as_uri()+'/',text)
target.write_text(text)
PY
brew install k0ngk0ng/wire-tools/wirectl
brew test k0ngk0ng/wire-tools/wirectl
prefix="$(brew --prefix k0ngk0ng/wire-tools/wirectl)"
test "$("$prefix/bin/wirectl" version)" = "$VERSION"
test "$(cat "$prefix/bin/.wirectl-package-manager")" = homebrew
test -x "$prefix/libexec/wirectl-download/bin/aria2c"
test -x "$prefix/libexec/wirectl-download/bin/amuled"
brew reinstall k0ngk0ng/wire-tools/wirectl
brew test k0ngk0ng/wire-tools/wirectl
brew uninstall k0ngk0ng/wire-tools/wirectl
