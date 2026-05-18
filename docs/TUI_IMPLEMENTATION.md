# TUI Implementation Notes

This implementation follows `TUI_PLAN.md` with one repository-specific
adaptation: the source for the existing `gws` binary is not present here, so the
new binary delegates all non-`tui` commands to an installed upstream `gws` when
available. The TUI itself uses a hybrid API client:

- command-backed client first, using the installed `gws`
- fixture-backed client as fallback, so the UI remains runnable offline
- golden fixture JSON for compatibility tests

## Package Map

- `cmd/`: CLI entrypoint, `tui` flag parsing, upstream delegation, fixture CLI
- `internal/api/`: Workspace client interface, command adapter, fixture adapter
- `internal/tui/`: Bubble Tea model, layout, modal compose, config/state
- `internal/tui/theme/`: Lip Gloss styles and palette
- `internal/tui/notify/`: platform notification and sound helpers
- `testdata/cli_golden/`: byte-for-byte fixture snapshots for CLI JSON shapes

## Release Contract

Release builds should pass:

```sh
go test ./...
go vet ./...
go build ./...
```

Tags matching `v*` trigger `.github/workflows/release.yml`, cross-compile the
binary, and attach archives to the GitHub release.
