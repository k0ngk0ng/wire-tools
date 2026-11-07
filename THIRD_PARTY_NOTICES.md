# Third-party software

wirectl consolidates wire-connect, wire-download and wire-talk. Their source
snapshots and provenance are recorded in docs/imports.json. The MIT license
applies to this repository's own code unless a file states otherwise.

- Connect notices: components/connect/THIRD_PARTY_NOTICES.md
- Talk notices: components/talk/THIRD_PARTY_NOTICES.md
- Download engine build recipes and patches: components/download/scripts/

Go dependencies retain their upstream licenses. Binary distributions include
license texts for bundled dependencies. aria2, aMule, FFmpeg and supporting
libraries have their own licenses; full distributions include corresponding
source archives and the exact build recipes. Downloaded tools and runtime
state are not part of the source import.
