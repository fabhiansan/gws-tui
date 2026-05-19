# Privacy and Local Data

`gws-tui` is a local terminal application. It does not run a hosted backend and
does not intentionally transmit data anywhere except through the local upstream
`gws` command and the Google Workspace APIs that command already uses.

## Authentication

The TUI delegates Google Workspace authentication and API access to an installed
`gws` binary when one is available. OAuth tokens and account credentials are
managed by that upstream CLI, not by a hosted `gws-tui` service.

Run this to inspect the current upstream auth state:

```sh
gws auth status
```

## Local Files

By default, config is loaded from the first existing path:

```text
$GOOGLE_WORKSPACE_CLI_CONFIG_DIR/tui.toml
$XDG_CONFIG_HOME/gws/tui.toml
~/.config/gws/tui.toml
```

Typical local data paths are:

```text
~/.config/gws/tui-state.json
~/.cache/gws/tui-cache.json
~/.cache/gws/images
~/.cache/gws/drafts
~/.cache/gws/tui.log
~/.cache/gws/daemon.log
```

If `$XDG_RUNTIME_DIR` is unavailable, daemon socket and PID files may also fall
back under `~/.cache/gws`.

## Cached Data

Depending on enabled features and actions, local cache files may include:

- Workspace list and detail metadata.
- Message snippets and conversation metadata.
- Draft text snapshots.
- Downloaded image attachments or inline image previews.
- Daemon or TUI diagnostic logs.

Avoid sharing cache, state, draft, image, or log files unless you have reviewed
and redacted private Workspace data.

## Deleting Local Data

Stop the daemon first if it is running:

```sh
gws daemon stop
```

Then remove local TUI data:

```sh
rm -rf ~/.cache/gws/tui-cache.json ~/.cache/gws/images ~/.cache/gws/drafts ~/.cache/gws/tui.log ~/.cache/gws/daemon.log ~/.config/gws/tui-state.json
```

If you configured custom paths in `tui.toml`, delete those custom paths instead.
