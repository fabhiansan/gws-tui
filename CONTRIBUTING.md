# Contributing

Thanks for helping improve `gws-tui`.

## Development

```sh
go build -o ./bin/gws .
./bin/gws tui --fixtures
```

Fixture mode is the safest way to work on UI behavior without touching a real
Google Workspace account:

```sh
GWS_TUI_USE_FIXTURES=1 ./bin/gws tui
```

## Checks

Run the same checks used by CI before opening a pull request:

```sh
make check
go build ./...
```

For changes that touch daemon mode, caching, auth delegation, or live Workspace
data, also run the manual smoke checklist in `docs/RELEASE_CHECKLIST.md`.

## Pull Requests

- Keep pull requests focused on one behavior or docs change.
- Include tests for behavior changes when the surface is practical to test.
- Do not commit OAuth credentials, token caches, local daemon sockets, logs, or
  screenshots that expose private Workspace data.
- Update `README.md` or `docs/` when a user-facing command, flag, config key,
  cache path, or release process changes.

## Compatibility

This repository intentionally keeps non-`tui` commands delegated to an existing
`gws` binary when available. Changes that affect command delegation should keep
the Neovim plugin compatibility contract covered by the fixture and golden
tests.
