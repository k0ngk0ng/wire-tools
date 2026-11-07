#!/usr/bin/env bash
# Run only in a disposable CI runner: this installs and migrates Homebrew apps.
set -euo pipefail
test "${GITHUB_ACTIONS:-}" = true
brew install k0ngk0ng/tap/wire-talk
brew test k0ngk0ng/tap/wire-talk
brew install k0ngk0ng/tap/talk
test "$(brew --prefix talk)" = "$(brew --prefix wire-talk)"
test "$(wirectl talk version)" = "$(wirectl-talk version)"
brew uninstall wire-talk

# Recreate an installation under the original name, then restore the rename
# map and verify Homebrew moves it to the canonical name without reinstalling.
TALK_TAP_PATH="$(brew --repository k0ngk0ng/tap)"
export TALK_TAP_PATH
python3 - <<'PY'
import json, os
from pathlib import Path
tap = Path(os.environ['TALK_TAP_PATH'])
cache = Path('.cache/talk-migration')
cache.mkdir(parents=True, exist_ok=True)
renames = tap / 'formula_renames.json'
(cache / 'formula_renames.json').write_bytes(renames.read_bytes())
mapping = json.loads(renames.read_text())
mapping.pop('talk', None)
renames.write_text(json.dumps(mapping))
(tap / 'Aliases/talk').unlink()
formula = (tap / 'Formula/wire-talk.rb').read_text()
(tap / 'Formula/talk.rb').write_text(formula.replace('class WireTalk < Formula', 'class Talk < Formula', 1))
PY
brew install k0ngk0ng/tap/talk
python3 - <<'PY'
import os
from pathlib import Path
tap = Path(os.environ['TALK_TAP_PATH'])
(tap / 'Formula/talk.rb').unlink()
(tap / 'Aliases/talk').symlink_to('../Formula/wire-talk.rb')
(tap / 'formula_renames.json').write_bytes(Path('.cache/talk-migration/formula_renames.json').read_bytes())
PY
brew migrate talk
brew test k0ngk0ng/tap/wire-talk
test "$(brew --prefix talk)" = "$(brew --prefix wire-talk)"
test "$(wirectl talk version)" = "$(wirectl-talk version)"
