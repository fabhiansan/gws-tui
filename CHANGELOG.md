# Changelog

All notable changes to this project will be documented in this file.

This project follows semantic versioning once public releases begin.

## Unreleased

- Prepare the repository for public GitHub release.
- Document privacy, security, contribution, release, and Reddit launch
  workflows.
- Align the Go module path and install command with the public `cmd/gws`
  command path.
- Remove runtime dummy-data fallback; live data now requires an authenticated
  upstream `gws` CLI.

## v0.1.0

Initial public release target.

Planned scope:

- Standalone `gws tui` terminal UI for Google Workspace.
- Chat, Mail, Calendar, and Meet feature panes.
- Optional daemon mode for shared Workspace data loading and notifications.
- Kitty inline image preview support.
- Compatibility coverage for existing Neovim plugin command shapes.
