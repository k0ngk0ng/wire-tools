# Third-party components

- `third_party/wirectl`: CLI source snapshot from `../wirectl` (k0ngk0ng),
  with Windows `.exe` discovery fixes. The snapshot makes builds independent
  of sibling checkouts. The host/plugin executable contract is unchanged.
- `internal/installpath`: MIT-licensed Windows junction resolution and tests
  adapted from k0ngk0ng/wire-connect; copyright 2026 k0ngk0ng.
- `github.com/gen2brain/malgo v0.11.24`: native audio bindings, Unlicense.
- miniaudio (embedded by malgo): public domain or MIT No Attribution,
  selected by the consumer. Its license is included in release archives.
- `golang.org/x/sys v0.31.0`: Windows security APIs, BSD 3-Clause.
- `github.com/schollz/pake/v3 v3.2.0`: password-authenticated pairing, MIT.
  This application selects P-256 and adds mutual key confirmation.
- `filippo.io/edwards25519 v1.2.0`: indirect PAKE dependency, BSD 3-Clause.
- `github.com/tscholl2/siec v0.0.0-20240310163802-c2c6f6198406`: indirect
  PAKE dependency, MIT. Its curve is not selected by wire-talk.
- `github.com/gopxl/beep v1.4.1`: audio streaming, WAV decoding and resampling, MIT.
- `github.com/hajimehoshi/go-mp3 v0.3.4`: MP3 decoding, Apache-2.0.
- `github.com/mewkiz/flac v1.0.8`: FLAC decoding, Unlicense.
- `github.com/mewkiz/pkg v0.0.0-20230226050401-4010bf0fec14`: FLAC helper code, Unlicense.
- `github.com/icza/bitio v1.1.0`: bitstream decoding, Apache-2.0.
- `github.com/pkg/errors v0.9.1`: decoder error context, BSD 2-Clause.
- Go standard library/runtime: Go BSD-style license.

The application source is MIT licensed; see LICENSE. Module versions and
checksums are recorded in go.mod and go.sum. No speech recognition or cloud
speech service is used.
