This directory contains a local replacement for `gvisor.dev/gvisor`.

Source snapshot:
- module version: `v0.0.0-20251209211007-f417d9b6ea17`
- copied from the local Go module cache

Why this exists:
- the pinned upstream snapshot fails to build with Go 1.26 because
  `pkg/sync/runtime_constants_go125.go` and
  `pkg/sync/runtime_constants_go126.go` are both selected
- newer upstream revisions include the Go 1.26 fix, but also introduce a
  separate `pkg/tcpip/stack/bridge_test.go` packaging issue for this repo

Local patch:
- `pkg/sync/runtime_constants_go125.go` now uses `go1.25 && !go1.26`
- this matches the upstream fix from commit `ecdf8459ce8b`
  (`sync: fix go1.26 build constraints`)
