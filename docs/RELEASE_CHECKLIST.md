# Release Checklist

Use this checklist before tagging a public release.

## Repository

- [ ] `git status --short` contains only intentional changes.
- [ ] `go.mod` module path matches the public GitHub repository.
- [ ] `README.md` install command uses the same public module path.
- [ ] `LICENSE`, `SECURITY.md`, `CONTRIBUTING.md`, and `CHANGELOG.md` are
  current.
- [ ] `docs/PRIVACY.md` reflects current auth, cache, draft, image, and daemon
  paths.
- [ ] Supported platforms are documented accurately. The first release targets
  macOS and Linux only.
- [ ] No credentials, OAuth tokens, private Workspace data, local logs, daemon
  sockets, or personal screenshots are committed.

## Automated Checks

```sh
make check
go build ./...
```

CI must pass on GitHub before the release tag is cut.

## Manual Smoke

- [ ] Build the binary with `go build -o ./bin/gws ./cmd/gws`.
- [ ] Verify an authenticated upstream CLI is available with `gws auth status`.
- [ ] Set `GWS_TUI_UPSTREAM` if the upstream CLI is not discoverable as another
  `gws` on `PATH`.
- [ ] Run `gws tui` against a real account and verify Chat, Mail, Calendar, and
  Meet screens open.
- [ ] Verify `r` refreshes the active feature.
- [ ] Verify Chat message loading, selection, and send behavior.
- [ ] Verify Mail thread loading and detail rendering.
- [ ] Verify Calendar and Meet detail panes render useful data.
- [ ] In Kitty, verify image previews render; outside Kitty, verify text fallback
  remains usable.
- [ ] Run `gws daemon start --detach`.
- [ ] Run `gws tui --daemon`.
- [ ] Open a second daemon-backed TUI and verify both clients receive live Chat
  updates.
- [ ] Run `gws daemon status`, `gws daemon logs`, and `gws daemon stop`.
- [ ] Run `:GwsOpen` in Neovim if validating compatibility with the existing
  plugin workflow.

## Tagging

```sh
git tag v0.1.0
git push origin v0.1.0
```

The GitHub release workflow should publish macOS/Linux platform archives and
`checksums.txt` for the tag.

## After Release

- [ ] Install from the public command path:
  `go install github.com/fabhiansan/gws-tui/cmd/gws@latest`.
- [ ] Download one GitHub release archive and verify its checksum.
- [ ] Add release notes from `CHANGELOG.md`.
- [ ] Confirm the README screenshot renders on GitHub.
- [ ] Prepare a Reddit post using `docs/REDDIT_LAUNCH.md`.
