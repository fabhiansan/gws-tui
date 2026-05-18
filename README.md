# gws TUI

Standalone terminal UI for Google Workspace, exposed as:

```sh
gws tui
```

![Chat screen](docs/screenshots/chat.svg)

The TUI is built with Bubble Tea, Bubbles, and Lip Gloss. It keeps the existing
Lua Neovim plugin contract intact: non-`tui` commands are delegated to an
installed `gws` binary when one is available, and fixture mode exists for
deterministic compatibility tests.

## Install

```sh
go install github.com/fabhiantomaoludyo/gws-tui@latest
```

For local development:

```sh
go build -o ./bin/gws .
./bin/gws tui
```

## Commands

```sh
gws tui
gws tui --feature chat
gws tui --feature mail
gws tui --feature calendar
gws tui --feature meet
gws tui --auth
gws tui --no-icons
gws tui --no-color
gws tui --version
```

Set `GWS_TUI_USE_FIXTURES=1` to force deterministic fixture data. Without that
flag, the TUI tries the installed Google Workspace CLI first and falls back to
fixtures if auth/API calls fail.

## Keys

Global:

- `1`/`2`/`3`/`4`: switch Chat, Mail, Calendar, Meet
- `Tab` / `Shift+Tab`: cycle features
- `j`/`k`: move
- `Enter`/`o`: open selected item
- `/`: search
- `m`: load more
- `r`: refresh
- `q`: quit

Chat:

- `i`: focus composer
- `Enter`: send
- `Shift+Enter`: newline
- `s`: toggle live subscription marker
- `R`: reply prefix

Mail:

- `c`: compose
- `R`: reply
- `f`: forward
- `e`: archive
- `#`: trash
- `s`: star/unstar

Calendar:

- `c`: full event composer
- `i`: quick add from action pane
- `y`/`n`/`M`: RSVP yes/no/maybe
- `d`: delete
- `t`: jump to today
- `[`/`]`: previous/next week marker

Meet:

- `n`: create new space from action pane
- `J`: join in browser
- `C`: copy link
- `E`: end active conference

## Config

Config is read from:

```text
~/.config/gws/tui.toml
```

Supported keys:

```toml
initial_feature = "chat"
no_icons = false
no_color = false
notify_desktop = true
notify_sound = true
notify_sound_file = "/System/Library/Sounds/Glass.aiff"
state_path = "~/.config/gws/tui-state.json"
draft_dir = "~/.cache/gws/drafts"
log_path = "~/.cache/gws/tui.log"
```

State is written to `~/.config/gws/tui-state.json`. Draft compose snapshots are
autosaved every five seconds under `~/.cache/gws/drafts`.

## Compatibility

The Lua plugin is not modified by this repository. Golden tests cover fixture
JSON for the CLI shapes used by the plugin:

- `gws auth status`
- `gws chat spaces list`
- `gws chat spaces messages list`
- `gws gmail users messages list`
- `gws calendar events list`
- `gws meet spaces list`

Run:

```sh
go test ./...
go vet ./...
go build ./...
```

Manual smoke remains required before a release:

1. Build the binary.
2. Put it on `PATH` ahead of the old `gws`, or set the plugin to use it.
3. Run `:GwsOpen` in Neovim and verify existing plugin flows still work.
4. Run `gws tui` and verify Chat, Mail, Calendar, and Meet screens open.
