# Feature Roadmap

What the upstream Google Workspace CLI (`gws`, currently `googleworkspace-cli`
v0.22.5) exposes, what the TUI already implements, and what is left to build —
organized end-to-end so each item can be picked up as a self-contained slice.

## Implementation status

Implemented in the TUI/API/daemon stack:

- Tier 0 stubs: mail send/archive/trash/star and calendar RSVP/delete.
- Tier 1 deepening: Chat edit/delete/reactions/create/setup/thread replies;
  Mail real labels/read-unread/drafts/attachment download; Calendar multiple
  calendars/edit/move; Meet conference history with participants, recordings,
  and transcripts.
- Tier 2 tabs: Tasks, Drive, and Docs, including Drive file download and Docs
  read-only document viewing.

Tier 3 remains intentionally deferred as the roadmap recommends.

## E2E anatomy of a feature

Every feature crosses these layers. The fewer layers an item touches, the
cheaper it is.

| # | Layer | File | Role |
|---|-------|------|------|
| 1 | Domain types + interface | `internal/api/types.go` | data types + method on `WorkspaceClient` |
| 2 | Upstream adapter | `internal/api/command.go` | `CommandClient` shells out to `gws ...` |
| 3 | Daemon protocol | `internal/api/protocol.go` | RPC param structs |
| 4 | Daemon client | `internal/api/remote.go` | `RemoteClient` over the socket |
| 5 | Daemon server | `internal/daemon/server.go` | `case "Method":` dispatch |
| 6 | Offline cache | `internal/api/snapshot.go` | field on `WorkspaceSnapshot` |
| 7 | TUI state + logic | `internal/tui/app.go` | `Feature`, state, `Update`, keybindings |
| 8 | Rendering | `render.go` / `card_render.go` / `detail_vim.go` | list/detail/action panes |
| 9 | CLI entry | `cmd/tui.go`, `cmd/root.go` | `--feature` value, usage text |
| 10 | Tests | `*_test.go` per layer | guards |

## What the upstream CLI offers

Services in `gws`: `drive`, `sheets`, `gmail`, `calendar`, `admin-reports`,
`docs`, `slides`, `tasks`, `people`, `chat`, `classroom`, `forms`, `keep`,
`meet`, `events`, `modelarmor`, `workflow`, `script`.

The TUI exposes four: `chat`, `mail`, `calendar`, `meet`.

---

## Tier 0 — Wire the existing stubs (touches layer 2 only)

The TUI UI, daemon protocol, `RemoteClient`, and daemon dispatch already exist
for all six items below. Each one currently has a `CommandClient` method that
returns `errors.New("... not wired")`. The only work is filling in the method
body in `internal/api/command.go`. **Highest ROI — do this first.**

### 0.1 `SendMail` — `internal/api/command.go:1014`

Current: `return MailThread{}, errors.New("mail compose through generic gws is not wired yet")`

- Build an RFC 2822 message from `MailDraft` (`To`, `Cc`, `Subject`, body). For
  replies, set `In-Reply-To` / `References` headers; `MailDraft.ThreadID` is
  already on the type.
- base64url-encode the message (`base64.URLEncoding`, already imported).
- Call: `gmail users messages send --json '{"raw":"<b64url>","threadId":"<id>"}'`
  with `--params '{"userId":"me"}'`.
- Parse the returned message id/threadId and return a `MailThread`.
- Effort: **S–M** (MIME assembly is the only fiddly part).

### 0.2 `ArchiveMail` — `internal/api/command.go:1018`

Current: `return errors.New("archive not wired")`

- `MailThread.ID` is a **threadId**, so use the `threads` resource.
- Call: `gmail users threads modify --params '{"userId":"me","id":"<threadId>"}' --json '{"removeLabelIds":["INBOX"]}'` via `runVoid`.
- Effort: **S**.

### 0.3 `TrashMail` — `internal/api/command.go:1021`

Current: `return errors.New("trash not wired")`

- Call: `gmail users threads trash --params '{"userId":"me","id":"<threadId>"}'` via `runVoid`.
- Effort: **S**.

### 0.4 `ToggleStar` — `internal/api/command.go:1024`

Current: `return MailThread{}, errors.New("star not wired")`

- The signature only gets a threadId, so fetch current state first
  (reuse `fetchMailMessage`, check the `STARRED` label).
- If starred → `removeLabelIds:["STARRED"]`, else `addLabelIds:["STARRED"]`,
  via `gmail users threads modify`.
- Return the updated `MailThread` (re-fetch or flip the `Starred` field locally).
- Effort: **S**.

### 0.5 `RSVPEvent` — `internal/api/command.go:1123`

Current: `return CalendarEvent{}, errors.New("rsvp not wired")`

- Google Calendar has no dedicated RSVP endpoint — the attendee patches the
  event with their own `responseStatus`.
- Steps: `calendar events get` the event → find the self attendee → set
  `responseStatus` (`accepted` / `declined` / `tentative`) → `calendar events patch --json '{"attendees":[...]}'`.
- Needs the user's own email. Source it from `calendar calendarList get`
  (`primary`) or cache it once at auth time.
- Effort: **M** (the self-email lookup is the wrinkle).

### 0.6 `DeleteEvent` — `internal/api/command.go:1126`

Current: `return errors.New("delete not wired")`

- Call: `calendar events delete --params '{"calendarId":"primary","eventId":"<id>"}'` via `runVoid` (schema confirmed).
- Effort: **S**.

### Tier 0 tests

Extend the `GWS_FAKE_COMMAND` fake-command harness in
`internal/api/command_test.go` (`fakeCommand()`) with cases for the new
subcommands, then add table tests per method. No other layer needs test
changes — protocol/remote/daemon paths are already covered.

**Tier 0 total: ~1–2 days, single file. Makes all four existing tabs fully
functional, which matters for the first public release.**

---

## Tier 1 — Deepen the existing four tabs (layers 1–5, 7–8)

No new `Feature` / `--feature` value needed; add methods to `WorkspaceClient`.

| Area | Item | Upstream command | Effort |
|------|------|------------------|--------|
| Chat | Delete / edit message | `chat spaces messages delete` / `patch` | M |
| Chat | Emoji reactions | `chat spaces messages reactions create` / `delete` | M |
| Chat | Create / manage space | `chat spaces create` / `setup` | M |
| Chat | Threaded-reply UI | (`ThreadID` already on the type) | M |
| Mail | **Real labels** (currently hardcoded `defaultMailLabels()`) | `gmail users labels list` | S |
| Mail | Mark read / unread | `gmail users threads modify` toggling `UNREAD` | S |
| Mail | Drafts | `gmail users drafts create` / `list` / `send` | M |
| Mail | Download mail attachments | `gmail users messages attachments get` | M |
| Calendar | Edit / move event | `calendar events patch` | M |
| Calendar | Multiple calendars | `calendar calendarList list` | M |
| Meet | Past meeting history | `meet conferenceRecords list` | M |
| Meet | Recordings & transcripts | `meet conferenceRecords recordings` / `transcripts list` | M |
| Meet | Participant list | `meet conferenceRecords participants list` | M |

`MailLabels()` returning a hardcoded list (`command.go:923`) is effectively a
bug — fixing it to call `gmail users labels list` is the cheapest Tier 1 win.

---

## Tier 2 — New tabs (full E2E, all 10 layers)

A new tab adds a `Feature` const, an entry in `featureOrder`, a snapshot field,
a `--feature` value, and rendering. Ordered by fit with the existing
three-pane (`list / detail / action`) layout.

| New tab | Upstream | Why it fits | Effort |
|---------|----------|-------------|--------|
| **Tasks** | `tasks tasklists` / `tasks tasks` | tasklist → tasks → detail maps cleanly onto three panes; small payloads, no heavy pagination | M |
| **Drive** | `drive files list` | file browser; download reuses the existing `--output` flag | M–L |
| **Docs** | `docs documents get` | read-only viewer, reuses the mail detail-pane pattern | M |

Tasks is the recommended first new tab — smallest data model, immediately
useful, lowest rendering risk.

---

## Tier 3 — Heavy / niche tabs (defer until post-release)

`sheets`, `slides`, `forms`, `keep`, `classroom`, `admin-reports`, `script`,
`modelarmor`. These need bespoke rendering (cell grids, form structures) and
should wait until after the first release.

`events` (Workspace Events API) is already used internally for the Chat
realtime stream and does not need its own tab.

---

## Recommended order

1. **Tier 0** — wire the six stubs. One file, ~1–2 days, unlocks the four
   existing tabs for the first release.
2. **Mail real labels + read/unread** (Tier 1) — fixes the hardcoded-labels bug.
3. **Tasks tab** (Tier 2) — first visible new feature.
4. Remaining Tier 1 / Tier 2 items as needed.
