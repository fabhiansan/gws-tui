# Changelog

All notable changes to this project will be documented in this file.

This project follows semantic versioning once public releases begin.

## Unreleased

_Nothing yet._

## v0.1.0 (2026-05-23)

First public release. Distributed via the personal Homebrew tap
`fabhiansan/tap` and `go install github.com/fabhiansan/gws-tui/cmd/gws-tui`.
Supported platforms: macOS and Linux on amd64 and arm64.

- Standalone `gws-tui` terminal UI for Google Workspace.
- Chat, Mail, Calendar, and Meet feature panes.
- Optional daemon mode for shared Workspace data loading and notifications.
- Kitty inline image preview support.
- Rename the TUI binary from `gws` to `gws-tui`. The TUI no longer shadows or
  delegates to the upstream `gws` CLI; users invoke `gws-tui` directly for the
  TUI and `gws` directly for the upstream CLI. `GWS_TUI_UPSTREAM` still
  overrides upstream binary discovery when needed.
- Make the detail pane URL-aware: focus detail, move the text cursor onto a
  URL (or any line with a single URL), and press `Enter`/`o` to open it in the
  default browser.
- Make the Mail folder sidebar interactive: focus it with `H`, navigate with
  `j`/`k`, and press `Enter` (or click) to load Inbox, Starred, Important,
  Sent, Drafts, Spam, Trash, All Mail, or any custom Gmail label. The active
  folder persists across restarts.
- Remove runtime dummy-data fallback; live data requires an authenticated
  upstream `gws` CLI.
- Document privacy, security, contribution, release, and Reddit launch
  workflows.
