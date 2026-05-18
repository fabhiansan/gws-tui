# gws TUI — End-to-End Plan

> Build a standalone Charm-styled TUI for Google Workspace as a new
> subcommand of the existing `gws` binary. **The current `gws-chat.nvim`
> Lua plugin is not modified by this plan and keeps working as-is.**
>
> When the TUI is mature, we'll evaluate replacing the Lua plugin in a
> separate, future plan. Until then the two coexist: power users can keep
> the deep-in-nvim experience, and the TUI gives us a path to a
> nvim-independent, beautiful TUI.

## 1. Vision

A single new entrypoint, `gws tui`, that:

1. Runs in any terminal, no Neovim required.
2. Has feature parity with what the Lua plugin does today (chat, mail,
   calendar, meet, auth, realtime events).
3. Looks great — Charm aesthetic, polished defaults, vim keybinds.

### Goals
- Beautiful, polished TUI (rounded borders, gradient accents, consistent
  palette).
- Vim-style keybinds; mouse optional.
- Feature parity with the current plugin.
- One binary, runs anywhere. Same install story as lazygit.
- Realtime updates without polling.

### Non-goals (explicitly out of scope)
- **Modifying the current `gws-chat.nvim` Lua plugin.** Zero changes to
  any `.lua` file. Zero changes to its public API. Existing users see no
  difference.
- Tight Neovim integration for the TUI (no extmarks, no virtual text in
  code buffers, no autocmd reactions). If we want those later, that's a
  separate plan built on RPC.
- Replacing or deprecating the Lua plugin in this cycle.
- Plugin/theme system for end users in v1.
- Web/HTTP UI.
- Mobile.

## 2. Relationship to the existing plugin

```
         existing today                     this plan adds
┌─────────────────────────────┐       ┌──────────────────────────┐
│  Neovim                     │       │  Any terminal            │
│  ┌───────────────────────┐  │       │  ┌────────────────────┐  │
│  │ gws-chat.nvim (Lua)   │  │       │  │   gws tui          │  │
│  │   ui.lua, events.lua, │  │       │  │   (Bubble Tea)     │  │
│  │   features/*, …       │  │       │  └─────────┬──────────┘  │
│  └──────────┬────────────┘  │       │            │             │
└─────────────┼───────────────┘       └────────────┼─────────────┘
              │                                    │
              │ shells out to                      │ in-process call
              ▼                                    ▼
        ┌──────────────────────────────────────────────────┐
        │  gws binary (Go)                                 │
        │    existing subcommands: auth, chat, mail, …     │
        │    NEW subcommand:       tui                     │
        │    shared package:       internal/api/ (reused)  │
        └──────────────────────────────────────────────────┘
```

- The Lua plugin already shells out to `gws ...` for its JSON data.
  That contract does not change.
- The TUI is a brand-new subcommand on the same binary, reusing the same
  internal API client packages.
- Both can be open at the same time. They share the same auth/token store
  on disk.

### What this plan does NOT touch
- `lua/gws_chat/init.lua`
- `lua/gws_chat/ui.lua`
- `lua/gws_chat/events.lua`
- `lua/gws_chat/features.lua`
- `lua/gws_chat/features/*.lua`
- `lua/gws_chat/config.lua`
- `lua/gws_chat/gws.lua`
- `lua/gws_chat/state.lua`
- `lua/gws_chat/users.lua`
- `lua/gws_chat/util.lua`
- `lua/gws_chat/base64.lua`
- `lua/plugins/gws-chat.lua`

If a step would require changing any of these, it is out of scope.

### What this plan DOES touch
- The `gws` Go binary (adds `cmd/tui.go`, `internal/tui/...`).
- Possibly the API client packages (promote/refactor for reuse, but
  preserve all existing CLI behavior — the Lua plugin depends on the
  CLI's JSON output).

## 3. Architecture

```
┌──────────────────────────────────────────────────────────┐
│ gws binary (Go) — additions only, no breaking changes   │
│                                                          │
│  cmd/                                                    │
│    root.go        ── existing                            │
│    auth.go,       ── existing (unchanged)                │
│    chat.go, …     ── existing (unchanged)                │
│    tui.go         ── NEW: `gws tui` entrypoint           │
│                                                          │
│  internal/                                               │
│    api/           ── shared client (refactor if needed,  │
│                      preserve CLI JSON output)           │
│    auth/          ── existing (unchanged)                │
│    events/        ── existing realtime subscription      │
│    tui/           ── NEW                                 │
│      app.go      ── root Bubble Tea model               │
│      router.go   ── feature switcher                    │
│      theme/      ── lipgloss palette + styles           │
│      components/ ── statusbar, list, viewport, input    │
│      chat/                                              │
│      mail/                                              │
│      calendar/                                          │
│      meet/                                              │
│      compose/    ── modal multi-field composer          │
│      notify/     ── desktop notifications + sound       │
└──────────────────────────────────────────────────────────┘
```

### Key decisions

| Decision | Choice | Reason |
|---|---|---|
| TUI framework | Bubble Tea + Lip Gloss + Bubbles | Most polished aesthetic, biggest ecosystem, Elm-style fits chat state |
| Process model | Single binary, new `tui` subcommand | One install, shared internal packages |
| Realtime in-app | Goroutine drives `tea.Cmd` → message into model | Bubble Tea's native pattern |
| Notifications when TUI closed | Out of scope for v1 | Lua plugin's `events.lua` still covers this case for nvim users |
| State persistence | `~/.config/gws/tui-state.json` | Small, separate from CLI/Lua state |
| Config | `~/.config/gws/tui.toml` | TUI-specific. The Lua plugin's config is untouched. |
| Theme | Lip Gloss adaptive (auto light/dark) | Works in any terminal |
| Logging | `log/slog` to `~/.cache/gws/tui.log` | Never write to stdout — Bubble Tea owns the terminal |
| CLI JSON output | **Frozen.** No changes. | Lua plugin parses it; breakage is a regression. |

### Compatibility guardrails

To make sure we don't accidentally break the Lua plugin:

1. **Golden-file tests for existing CLI JSON output.** Snapshot
   representative outputs of `gws chat spaces`, `gws chat messages`,
   `gws mail list`, etc., into `testdata/cli_golden/`. CI fails if the
   shape changes.
2. **No edits to existing `cmd/*.go` flags or arg names.**
3. **If `internal/api/` is refactored for reuse, the CLI side keeps the
   exact same public output.**
4. **Manual smoke test of `:GwsOpen` after each milestone** — does the
   Lua plugin still work end to end?

## 4. UI Plan

### 4.1 Design language

- **Borders:** rounded (`lipgloss.RoundedBorder`), 1-cell padding inside panes.
- **Active pane:** accent border + bold title. Inactive: dim border + dim title.
- **Status bar:** bottom row, feature tabs left, hints right, live indicator
  inline when active subscriptions exist.
- **Palette (adaptive):**
  - Accent: `#7D56F4` (purple)
  - Live: `#10B981` (green)
  - Warn: `#F59E0B`
  - Error: `#EF4444`
  - Subtle: `#6B7280`
  - Background: terminal default (respects user's colorscheme)
- **Typography:** Unicode box-drawing + optional Nerd Font glyphs (gated by
  `--no-icons` flag).
- **Animation:** spinners during loads (Bubbles `spinner.Dot`), brief fade for
  newly arrived messages.

### 4.2 Root layout

```
┌─ gws ──────────────────────────────────────────────────────────────────┐
│ ┌─ Spaces ───────────┐ ┌─ #engineering ───────────────────────────────┐ │
│ │ ● #engineering  ▎  │ │ alice    10:42                               │ │
│ │   #design          │ │   anyone seen the latest design?             │ │
│ │   #random          │ │                                              │ │
│ │   @alice           │ │ bob      10:43                               │ │
│ │   @bob             │ │   yeah, looks great                          │ │
│ │                    │ │                                              │ │
│ │                    │ │ you      10:44                               │ │
│ │                    │ │   shipping today                             │ │
│ │                    │ └──────────────────────────────────────────────┘ │
│ │                    │ ┌─ message · ⏎ send · ⇧⏎ newline ──────────────┐ │
│ │                    │ │ ▎                                            │ │
│ │                    │ │                                              │ │
│ │                    │ └──────────────────────────────────────────────┘ │
│ └────────────────────┘                                                  │
│  Chat · Mail · Calendar · Meet              ● 2 live    j/k move ⏎ open │
└────────────────────────────────────────────────────────────────────────┘
```

- **List pane (left):** 30% width, min 28 cols.
- **Detail pane (top-right):** remaining width.
- **Action pane (bottom-right):** 3 lines tall by default, expands while typing.
- **Status bar:** 1 line, full width. Tabs left, hints right, live indicator inline.

### 4.3 Feature: Chat

```
┌─ Spaces ────────────┐ ┌─ #engineering ───────────────────────────────┐
│ ● #engineering   ▎  │ │ ─── Today ─────────────────────────────────  │
│   #design        ●  │ │                                              │
│   #random           │ │ alice    10:42                               │
│   @alice            │ │   anyone seen the latest design?             │
│   @bob              │ │   ↪ 2 replies                                │
│                     │ │                                              │
│                     │ │ bob      10:43                               │
│                     │ │   yeah, looks great 🔥                       │
│                     │ │                                              │
│                     │ │ you      10:44                               │
│                     │ │   shipping today                             │
└─────────────────────┘ └──────────────────────────────────────────────┘
                        ┌─ message · ⏎ send ───────────────────────────┐
                        │ Got it, will start the deploy▎               │
                        └──────────────────────────────────────────────┘
```

**Per-space markers:**
- `●` green = live subscription active.
- `●` accent = unread.
- `▎` accent bar = currently selected.

**Detail rendering:**
- Day separators (`─── Today ───`, `─── Yesterday ───`).
- Sender colors derived from a stable hash of senderID. Self always uses accent.
- Wrap to viewport width, breakindent for hanging indent.
- Threaded messages indented under parent with `↪`.
- Code blocks: lipgloss `Border` with subtle background.
- Links: underlined accent.

**Composer:**
- Single line by default, grows up to 6 with `Shift+Enter`.
- `@` triggers member-search popover (v2 if not in MVP).

**Keys (chat):**
| Key | Action |
|---|---|
| `j`/`k` | move in list/detail |
| `Enter`/`o` | open selected space (focus → detail) |
| `i` | focus composer |
| `s` | toggle subscription on selected space |
| `R` | reply to focused message |
| `gg`/`G` | top/bottom |
| `/` | search messages in current space (v2 if not in MVP) |

### 4.4 Feature: Mail

```
┌─ Inbox (32) ─────────────┐ ┌─ Re: Q4 planning ────────────────────────┐
│ ● Alice                  │ │ From: alice@example.com                  │
│   Re: Q4 planning   2h   │ │ To:   you@example.com, bob@…             │
│ ─────────────────────    │ │ Date: Mon, 17 May 2026 14:22             │
│   Bob                    │ │ ────────────────────────────────────     │
│   Lunch?            5h   │ │                                          │
│ ─────────────────────    │ │ Hi team,                                 │
│   GitHub                 │ │                                          │
│   PR review         1d   │ │ Sending the latest deck for Q4. Let me   │
│                          │ │ know your thoughts by Friday.            │
│                          │ │                                          │
│                          │ │ — Alice                                  │
└──────────────────────────┘ └──────────────────────────────────────────┘
 [Inbox] Unread Starred …    ┌─ quick reply · ⏎ send ───────────────────┐
                              │ Looks good!▎                            │
                              └─────────────────────────────────────────┘
```

- 2-line thread rows: `● Sender` + `Subject ... time`.
- Label tab strip at bottom of list pane.
- HTML → plaintext via `jaytaylor/html2text` or `k3a/html2text`.
- Quoted text (`> ...`) collapsed with `[+ 23 lines quoted]` toggle.

**Compose modal:**
```
┌─ Compose · ^s send · ⇥ next field · ^q cancel ──────────────────────┐
│ To:      bob@example.com                                            │
│ Cc:                                                                 │
│ Subject: Re: Q4 planning                                            │
│ ────────────────────────────────────────────────────────────────    │
│                                                                     │
│ Bob,                                                                │
│                                                                     │
│ Sounds good. I'll have the deck reviewed by Thursday.               │
│                                                                     │
│ — you▎                                                              │
└─────────────────────────────────────────────────────────────────────┘
```

- Field jumping with `Tab`/`Shift+Tab`.
- `Ctrl+s` or `Ctrl+Enter` to send.
- Draft autosave every 5s to `~/.cache/gws/drafts/<uuid>.json`.

**Keys (mail):**
| Key | Action |
|---|---|
| `j`/`k` | move thread |
| `Enter`/`o` | open thread |
| `c` | compose new |
| `R` | reply |
| `f` | forward |
| `e` | archive |
| `#` | trash |
| `s` | star/unstar |
| `/` | search (Gmail query syntax) |
| `m` | load more |
| `1..9` | jump to label N |

### 4.5 Feature: Calendar

```
┌─ This week ─────────────┐ ┌─ 1:1 with Alice ──────────────────────────┐
│ Today                   │ │ 🕐 Mon, 17 May 2026 · 14:00–14:30        │
│ ● 14:00  1:1 with Alice │ │ 📍 Google Meet (auto-generated)           │
│   16:30  Eng review     │ │ 👥 alice@…, you                           │
│ Tue 18 May              │ │                                           │
│   09:00  Standup        │ │ ─── Description ─────────────────────     │
│   13:00  Lunch w/ Bob   │ │                                           │
│ Wed 19 May              │ │ Weekly sync.                              │
│   10:00  Planning       │ │                                           │
│                         │ │ ─── Actions ──────────────────────────    │
│                         │ │   [Y]es  [N]o  [M]aybe                    │
└─────────────────────────┘ └───────────────────────────────────────────┘
                            ┌─ quick add · ⏎ create ────────────────────┐
                            │ Lunch Friday 12pm▎                       │
                            └───────────────────────────────────────────┘
```

- Day headers between groups.
- `●` accent = next upcoming event (single).
- Color-coded by type (1:1 / meeting / focus).
- Quick-add uses Google's natural-language parser.

**Compose modal (full event):**
```
┌─ New event · ^s save · ⇥ next ─────────────────────────────────────┐
│ Summary:    Design review                                          │
│ When:       Fri 21 May 2026  14:00 – 15:00                         │
│ Where:      Google Meet                                            │
│ Attendees:  alice@…, bob@…                                         │
│ ─────────────────────────────────────────────────────────────      │
│ Description:                                                       │
│ Reviewing the new dashboard mocks.                                 │
└────────────────────────────────────────────────────────────────────┘
```

**Keys (calendar):**
| Key | Action |
|---|---|
| `j`/`k` | move event |
| `Enter`/`o` | open event |
| `c` | new event (compose) |
| `q` | quick-add via action pane |
| `y`/`n`/`M` | RSVP yes/no/maybe |
| `d` | delete event |
| `/` | search |
| `m` | load more |
| `t` | jump to today |
| `]`/`[` | next/prev week |

### 4.6 Feature: Meet

```
┌─ Meet spaces ───────────┐ ┌─ standup-daily ───────────────────────────┐
│ ● standup-daily  active │ │ 🔗 https://meet.google.com/abc-defg-hij   │
│   demo-thursday         │ │ 👥 5 active                               │
│   q4-planning           │ │                                           │
│                         │ │ Created: 12 May 2026                      │
│                         │ │ Type:    open                             │
│                         │ │ Recording: off                            │
│                         │ │                                           │
│                         │ │ ─── Actions ──────────────────────────    │
│                         │ │   [J]oin  [C]opy link  [E]nd              │
└─────────────────────────┘ └───────────────────────────────────────────┘
                            ┌─ create space · ⏎ create ─────────────────┐
                            │ retro-may                                ▎│
                            └───────────────────────────────────────────┘
```

**Keys (meet):**
| Key | Action |
|---|---|
| `j`/`k` | move space |
| `Enter`/`o` | open space details |
| `J` | join in browser (`xdg-open`/`open`) |
| `C` | copy link to clipboard |
| `n` | new space (action pane) |
| `E` | end active conference |

### 4.7 Auth screen

Shown automatically when no valid token is found, or via `gws tui --auth`.

```
┌─ gws · sign in ────────────────────────────────────────────────────┐
│                                                                    │
│  Signing you into Google Workspace…                                │
│                                                                    │
│  ✓ Browser opened                                                  │
│  → Waiting for OAuth callback                                      │
│                                                                    │
│  If your browser didn't open, visit:                               │
│  https://accounts.google.com/…                                     │
│                                                                    │
│  Press q to cancel                                                 │
│                                                                    │
└────────────────────────────────────────────────────────────────────┘
```

After success: dismiss and route to last-used feature (or chat by default).

### 4.8 Notifications

- Desktop notify via `beeep` or platform-specific:
  - macOS: `osascript` (or `terminal-notifier`)
  - Linux: `notify-send`
  - Windows: `BurntToast`
- Sound: same fallback chain the Lua plugin uses (`afplay` / `paplay` / bell).
- In-TUI toast: bottom-right slide-in (3s) when message arrives in
  non-focused space.

### 4.9 Empty / error states

- **Empty list:** centered subtle text + hint (`No spaces yet. Press n to create.`).
- **Loading:** spinner + label centered in pane.
- **Error:** red bordered banner at top of pane, dismissible with `x`.
- **Disconnected (subscription dropped):** status bar shows `○ reconnecting…`
  in amber, retries with exponential backoff.

## 5. Phased milestones

Each milestone produces a runnable, releasable binary. **Don't combine.**
After every milestone: run `:GwsOpen` in nvim and confirm the Lua plugin still
works end to end.

### M0 — Scaffolding (1–2 days)

- [x] Add `cmd/tui.go` with a stub Bubble Tea program (renders "hello").
- [x] Add `internal/tui/app.go` with root model: header + body + status bar,
      no features yet.
- [x] Add `internal/tui/theme/` with palette + reusable styles.
- [x] Wire `--feature=<name>` flag (no-op for now).
- [x] Set up golden-file CI test for existing CLI JSON output (compatibility
      guardrail).
- [x] CI: `go build ./...` + `go vet` on push.

**Definition of done:**
- `gws tui` opens and renders the shell in any terminal. Pressing `q` quits.
- `gws chat spaces`, `gws mail list`, etc. produce **byte-identical** JSON
  to before the M0 changes (golden tests pass).
- `:GwsOpen` in nvim still works (manual smoke).

### M1 — Chat read-only (3–4 days)

- [x] Promote/refactor `internal/api/chat.go` for reuse (CLI output unchanged).
- [x] `internal/tui/chat/model.go`: spaces list + message viewport.
- [x] Sender color hashing, day separators, threaded reply indicator.
- [x] Load states: spinner while fetching spaces, then messages.
- [x] Pagination on scroll-to-top (load older).
- [x] Status bar shows current space.

**DoD:** `gws tui` → see spaces → select space → see messages, paginate.
Lua plugin smoke test passes.

### M2 — Chat send + realtime (2–3 days)

- [x] Composer pane with growing height, `Shift+Enter` newline.
- [x] Send message via existing API client.
- [x] Optimistic insert (gray) until server confirms (full color).
- [x] Realtime subscription: subscribe per-space, dedupe by message ID,
      push to model via `tea.Cmd`.
- [x] Live indicator + reconnect with backoff.
- [x] Desktop notifications + sound when message arrives in unfocused space.

**DoD:** Send works. Open two terminals, send from one, see it in the other
within 1s. Lua plugin smoke test passes.

### M3 — Mail (4–5 days)

- [x] Promote/refactor `internal/api/mail.go` for reuse (CLI output unchanged).
- [x] Labels list, thread list, thread view with HTML→text.
- [x] Label tab strip at bottom of list pane.
- [x] Compose modal with field jumping, draft autosave.
- [x] Reply / forward / archive / trash / star.
- [x] Search with Gmail query syntax.
- [x] Pagination.

**DoD:** Read inbox, open thread, reply, send, compose, search.
Lua plugin smoke test passes.

### M4 — Calendar (3 days)

- [x] Promote/refactor calendar API (CLI output unchanged).
- [x] List events grouped by day.
- [x] Event detail with RSVP buttons.
- [x] Quick-add via action pane (natural language).
- [x] Full compose modal for events.
- [x] Search + pagination + delete.

**DoD:** Browse week, see today's events, RSVP, create event.
Lua plugin smoke test passes.

### M5 — Meet (1–2 days)

- [x] List spaces, detail view, copy/join/end actions.
- [x] Create new space from action pane.

**DoD:** All four features functional.
Lua plugin smoke test passes.

### M6 — Polish & release (2–3 days)

- [x] First-run auth flow (auto-detect missing token, prompt).
- [x] Config file (`~/.config/gws/tui.toml`) read + reload.
- [x] State persistence (last feature, last space) — separate file from any
      Lua-side state.
- [x] `--no-icons` and `--no-color` flags.
- [x] `gws tui --version` shows build SHA.
- [x] Documentation: README section for the TUI, screenshots/casts.
- [x] GitHub Actions: cross-compile for darwin/linux/windows × amd64/arm64,
      attach binaries to release tag.

**DoD:** Tagged release. Binary installable. Lua plugin unchanged and working.

### Implementation status — 2026-05-18

Implemented in this repository:

- `cmd/tui.go`, `cmd/root.go`, and `cmd/delegate.go` add the `gws tui`
  entrypoint, `--feature`, `--auth`, `--no-icons`, `--no-color`,
  `--no-images`, and `--version`.
- `internal/api/` provides a reusable Workspace client boundary, command-backed
  adapter for an installed `gws`, fixture fallback, realtime fixture events, and
  golden JSON compatibility coverage.
- `internal/tui/` provides the Bubble Tea root model, feature router, Chat,
  Mail, Calendar, Meet views, composers, quick actions, config reload, state
  persistence, draft autosave, Kitty inline image previews using virtual
  placement with text fallback, media-resource keyed Chat image caching,
  authenticated Chat media download through the upstream CLI, desktop
  notification helpers, and logging.
- `internal/tui/theme/` contains the Lip Gloss palette and reusable styles.
- `testdata/cli_golden/` and `cmd/root_test.go` cover compatibility snapshots
  for the CLI JSON shapes used by the Lua plugin.
- `.github/workflows/ci.yml` runs `go test`, `go vet`, and `go build`.
- `.github/workflows/release.yml` cross-compiles darwin/linux/windows for
  amd64/arm64 and attaches binaries to `v*` tags.
- `README.md`, `docs/TUI_IMPLEMENTATION.md`, and
  `docs/screenshots/chat.svg` document usage, config, compatibility, and the
  current terminal UI.

Verified locally:

- `go test ./...`
- `go vet ./...`
- `go build ./...`
- `go build -o /private/tmp/gws-tui-gws .` produced a 6.1 MB binary.
- `/private/tmp/gws-tui-gws tui --version`
- `GWS_TUI_USE_FIXTURES=1 go run . chat spaces list`
- PTY smoke: `/private/tmp/gws-tui-gws tui --fixtures` rendered and exited
  cleanly with `q`.

External gates still required before an actual release announcement:

- Tag a release in git and let `.github/workflows/release.yml` publish binaries.
- Run a real Neovim `:GwsOpen` smoke against the built binary in an interactive
  editor session.
- Dogfood against valid Google Workspace credentials for at least one week.

### Out of this plan: future "M7 — replace Lua plugin"

Not part of this plan. To be considered as a separate effort *after* the TUI
has been dogfooded for at least a few weeks and we've confirmed it's a
net upgrade over the Lua plugin. That future plan would:
- Provide a Lua launcher that opens the TUI in a Neovim float (lazygit-style).
- Migrate users with a deprecation cycle.
- Eventually remove the Lua UI code.

We do not commit to that here.

## 6. Testing strategy

| Layer | Approach |
|---|---|
| API clients | Existing CLI tests; add table tests for new endpoints |
| CLI JSON compatibility | Golden files under `testdata/cli_golden/`, diffed in CI |
| TUI models | `teatest` (Charm) — script keystrokes, assert rendered output |
| Theme | Visual regression: golden-file snapshots of rendered views |
| Realtime | Inject fake event source; verify dedupe + ordering |
| Cross-platform | GH Actions matrix builds. Manual smoke on macOS + Linux |
| Lua plugin regression | Manual `:GwsOpen` smoke test after every milestone |

No mocking the Google APIs in unit tests — use recorded fixtures
(`go-vcr` or hand-rolled JSON in `testdata/`).

## 7. Risks & mitigations

| Risk | Mitigation |
|---|---|
| **Refactoring `internal/api/` breaks the CLI's JSON output and silently breaks the Lua plugin** | Golden-file tests on every CLI subcommand (M0 task). CI fails on any drift. |
| **Bubble Tea performance with 10k messages** | Viewport windowing; only render visible lines. Charm's `viewport.Model` handles this. |
| **HTML email rendering is ugly** | Start with `html2text`, accept imperfection. v2: `--html-viewer=lynx` escape hatch. |
| **OAuth in a sealed TUI** | Release alternate screen, open browser, poll for callback, restore TUI. Pattern used by `gh` CLI. |
| **Notifications when TUI closed** | The Lua plugin already covers nvim users. Standalone-TUI users opt-in to a future `gws daemon` (v2). |
| **Color in plain terminals** | Lip Gloss auto-degrades. `--no-color` flag. Test in `TERM=dumb`. |
| **Two processes competing for the same auth token / refresh race** | Token store already supports concurrent reads. Confirm exclusive-write locking on refresh — add a file lock if missing. |
| **Two processes both subscribing to the same chat space** | The Pub/Sub backend dedupes server-side, but local notifications could double-fire. Acceptable for v1; document. |

## 8. Open questions

Defaults in parens; flag if you want to change them.

1. **Bubble Tea or tview?** (Bubble Tea)
2. **Config format: TOML or YAML?** (TOML, via koanf)
3. **Where do drafts live?** (`~/.cache/gws/drafts/`)
4. **Single binary or split `gws-tui`?** (Single, new subcommand)
5. **Notification library: beeep or per-OS exec?** (beeep)
6. **State persistence format: JSON or BoltDB?** (JSON file)
7. **Do we let two `gws tui` instances run at once?** (Yes, with a warning
   if both subscribe to the same space)

## 9. Out of scope (deferred to v2+)

- Slash commands in chat composer.
- File uploads and full non-image attachment management.
- @-mention completion popover.
- Threaded reply UI (collapse/expand).
- Drive / Docs features.
- Per-user themes / config hot-reload.
- Background daemon for notifications.
- Multi-account switching inside TUI.
- Web UI / remote-rendering.
- **Any changes to the Lua plugin.** (Tracked separately, future plan.)

## 10. Success criteria

We ship the TUI when:

1. All four features (chat, mail, calendar, meet) work in `gws tui`.
2. Realtime chat delivers messages in <1s on a healthy connection.
3. Binary <20MB, starts in <100ms cold.
4. Works on macOS arm64/amd64 and Linux amd64 (Windows best-effort).
5. Internal dogfooding for ≥1 week with no P0 bugs.
6. README + GIF/cast in place.
7. **The Lua plugin is unchanged and demonstrably still working** —
   `:GwsOpen` opens it, all existing commands behave identically, the
   golden CLI JSON tests pass.

Bonus signals: screenshots on social from users in Alacritty, WezTerm,
Ghostty, iTerm, gnome-terminal without bug reports.
