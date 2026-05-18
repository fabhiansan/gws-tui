# Daemon Mode — E2E Plan

Mode opsional `gws daemond`: proses background yang memegang workspace state
(polling chat, cache, image download, draft autosave, notifikasi). TUI berubah
jadi thin client yang attach/detach ke daemon via Unix socket. Mode default
tetap **standalone** (single-process, seperti sekarang) — daemon harus dinyalakan
secara eksplisit.

## Tujuan & non-tujuan

**Goal**
- Polling chat & cache tetap hidup walau TUI ditutup.
- Notifikasi desktop tetap masuk tanpa TUI terbuka.
- TUI bisa dibuka/tutup berkali-kali, restart instan (no re-fetch).
- Multi-client (opsional): beberapa TUI attach ke daemon yang sama.

**Non-goal (untuk MVP)**
- Remote daemon (lintas mesin). Daemon strictly per-user, per-host.
- Windows support (tunda sampai Unix socket flow stabil).
- Web UI / HTTP API.
- Multi-user / sharing socket lintas user.

## Prinsip arsitektur

1. **Seam-nya sudah ada.** `internal/api.WorkspaceClient` adalah interface
   tunggal yang dipakai TUI untuk semua I/O workspace. Daemon mode = tambah
   implementasi ke-4: `RemoteClient` yang ngomong ke daemon via socket.
   TUI tidak peduli `client` itu `CommandClient`, `HybridClient`, atau
   `RemoteClient`.
2. **Daemon = headless TUI backend.** Daemon membungkus `HybridClient` +
   `workspaceCache` + subscription loop + notify dispatch. Tidak ada Bubble
   Tea di daemon, tidak ada rendering.
3. **State split jelas.** Data workspace (spaces, threads, events, cache,
   auth, image files) → **daemon**. UI state (selection, focused pane, scroll,
   vim mode, modal, search input) → **client**. Draft compose: hybrid (autosave
   ke daemon agar survive restart TUI, tapi composer state in-memory tetap di
   client).
4. **Protokol push + request/response.** Client kirim RPC (`ChatMessages`,
   `SendChatMessage`, dst.) dan terima event stream (`ChatMessageEvent`,
   `WorkspaceRefreshEvent`, `AuthChangedEvent`).

## CLI surface

```sh
# Default — standalone, persis seperti sekarang
gws tui

# Connect ke daemon; auto-spawn kalau belum jalan
gws tui --daemon

# Standalone explicit (override config)
gws tui --no-daemon

# Daemon lifecycle
gws daemon start         # foreground (untuk launchd/systemd)
gws daemon start --detach # fork ke background, return immediately
gws daemon stop          # SIGTERM via PID file
gws daemon status        # running? socket path? uptime? clients connected?
gws daemon logs          # tail $XDG_CACHE_HOME/gws/daemon.log
gws daemon restart
```

`tui.toml` baru:
```toml
daemon = false                              # auto-connect ke daemon kalau true
daemon_socket = "$XDG_RUNTIME_DIR/gws/daemon.sock"
daemon_autospawn = true                     # spawn daemon kalau belum jalan saat --daemon
daemon_log = "~/.cache/gws/daemon.log"
daemon_pid_file = "$XDG_RUNTIME_DIR/gws/daemon.pid"
```

Discovery order untuk socket: `$XDG_RUNTIME_DIR/gws/daemon.sock` →
`~/.cache/gws/daemon.sock` (macOS biasanya tidak set XDG_RUNTIME_DIR).

## Komponen baru

```
cmd/
  daemon.go              # `gws daemon` subcommand parsing
  tui.go                 # tambah --daemon / --no-daemon, RemoteClient wiring
internal/
  daemon/
    server.go            # net.Listener Unix socket, per-client goroutine
    session.go           # state per-client (subscriptions, snapshot version)
    hub.go               # fan-out events ke semua client, dedup subscriptions
    lifecycle.go         # PID file, signal handling, graceful shutdown
    rpc.go               # encode/decode + dispatch ke WorkspaceClient
  api/
    remote.go            # RemoteClient — implements WorkspaceClient via socket
    protocol.go          # tipe Request/Response/Event (shared daemon+client)
  tui/
    config.go            # tambah Daemon* fields
```

## Protokol

**Transport:** Unix domain socket, file mode `0600`, per-user. Frame:
length-prefixed JSON (`uint32 BE length` + `payload`). Alternatif: net/rpc
atau gRPC — JSON manual lebih ringan, no codegen, kompatibel dengan tooling
debug (`nc -U`, `socat`).

**Envelope:**
```json
{ "id": 42, "kind": "request",  "method": "ChatMessages", "params": {...} }
{ "id": 42, "kind": "response", "result": {...}, "error": null }
{           "kind": "event",    "topic": "chat.message", "payload": {...} }
```

**Methods (1:1 dengan `WorkspaceClient`):**
- `AuthStatus`, `ChatSpaces`, `ChatMessages`, `SendChatMessage`,
  `ChatMembers`, `PeopleGet`, `DownloadAttachment`
- `MailLabels`, `MailThreads`, `SendMail`, `ArchiveMail`, `TrashMail`,
  `ToggleStar`
- `CalendarEvents`, `QuickAddEvent`, `CreateEvent`, `RSVPEvent`, `DeleteEvent`
- `MeetSpaces`, `CreateMeetSpace`, `EndMeetSpace`

**Methods khusus daemon (baru, tidak ada di `WorkspaceClient`):**
- `Snapshot` — full workspace state untuk hydrate client saat connect
  (menggantikan call awal `loadAllCmd`). Berisi versi cache + payload.
- `SubscribeTopics` — client declare topik yang dia mau (`chat.message:<space>`,
  `mail.changed`, `auth.changed`, `cache.updated`).
- `Ping` — heartbeat, 15s interval. Client disconnect kalau 3× miss.
- `DraftSave` / `DraftLoad` — pindahkan autosave ke daemon (per-client key).

**Events:**
- `chat.message` — new message arrived (multiplex dari `SubscribeChat`).
- `chat.refreshed` — refetch selesai (trigger oleh client lain atau timer).
- `mail.changed`, `calendar.changed`, `meet.changed`.
- `auth.changed` — relogin / token expired.
- `image.cached` — file path siap dipakai untuk Kitty render.
- `notify` — daemon sudah fire notifikasi; client tampilkan toast saja.

## Pembagian tanggung jawab

| Concern                 | Standalone (sekarang) | Daemon mode |
|-------------------------|-----------------------|-------------|
| `WorkspaceClient` calls | TUI process           | Daemon      |
| `workspaceCache` r/w    | TUI                   | Daemon (file lock) |
| Polling `SubscribeChat` | TUI tick 5s           | Daemon, **satu** loop per space, fan-out |
| Image download          | TUI cmds              | Daemon → emit `image.cached` |
| Image **render**        | TUI                   | TUI (Kitty escape ke TTY pemanggil) |
| Desktop notify          | TUI                   | Daemon (selalu fire, tidak peduli client connected) |
| Toast in-UI             | TUI                   | TUI (dari event `notify`) |
| Draft autosave (5s)     | TUI → disk            | Daemon → disk, key per-client |
| Selections (per-feature)| TUI persisted state   | TUI (per-session) — `persistedState` tetap di client |
| `LastSpace` / `LastFeature` | TUI                | Client (per-host) — bukan urusan daemon |

**Catatan image render:** image *bytes* di-cache di filesystem oleh daemon
(`~/.cache/gws/images`). TUI baca path dari event + emit Kitty escape sequence
ke TTY-nya sendiri. File system jadi shared transport — tidak perlu kirim
binary lewat socket.

## Refactor seam (Phase 0 — wajib sebelum semua)

Audit 71 call site `m.client.*` / `m.cache.*` di `internal/tui/`. Pastikan:

1. **Tidak ada bypass.** Semua I/O workspace harus lewat `m.client`. Sekarang
   sudah hampir 100% — verify dengan grep.
2. **Cache write hanya satu titik.** `persistWorkspaceCache` adalah satu-satunya
   penulis ke `tui-cache.json`. Di daemon mode, fungsi ini jadi no-op di client;
   daemon punya `cache.Save()` sendiri.
3. **`Snapshot` shape disepakati.** Buat tipe `WorkspaceSnapshot` di
   `internal/api` yang isinya = field-field `workspaceCache` saat ini. Pakai
   tipe yang sama untuk hydrate standalone dan daemon mode.
4. **`Model` tidak menyentuh `os` / `exec` langsung.** Saat ini bersih
   (notify lewat package), pertahankan.

Ini bukan refactor besar — lebih ke "tighten existing boundaries" + tambah
tipe `WorkspaceSnapshot`.

## Phase plan

### Phase 1 — Protokol & RemoteClient (no daemon yet)
- Define `internal/api/protocol.go` (Request, Response, Event, envelope).
- Implement `RemoteClient` di `internal/api/remote.go`. Methods:
  - sequential request/response via socket (mutex + reply map by `id`)
  - `SubscribeChat` returns channel yang di-feed dari event stream
  - `Close()` cleanup goroutine + socket
- Test dengan **fake daemon** in-process (net.Pipe) — TUI Model jalan unchanged.
- Acceptance: `go test ./internal/api/...` hijau, RemoteClient round-trip semua
  method `WorkspaceClient`.

### Phase 2 — Daemon binary
- `cmd daemon start` (foreground only, no detach yet).
- `internal/daemon/server.go`: listen Unix socket, satu goroutine per koneksi.
- Daemon membungkus `HybridClient` + `workspaceCache` + satu `SubscribeChat`
  loop per space (lazy, on-demand).
- Hub: fan-out `ChatMessage` ke semua client yang subscribe space tersebut.
- `gws tui --daemon` connect ke socket, gagal kalau daemon belum ada.
- Acceptance: jalankan `gws daemon start` di satu shell, `gws tui --daemon` di
  shell lain → chat realtime jalan, tutup TUI, buka lagi, state masih ada.

### Phase 3 — Lifecycle
- `--detach` (double-fork pattern atau `setsid` + exec).
- PID file + flock.
- `gws daemon stop` (kirim SIGTERM, tunggu, fallback SIGKILL setelah 5s).
- `gws daemon status` (baca PID file, ping socket, tampilkan uptime &
  client count).
- Autospawn: kalau `--daemon` + socket tidak ada + `daemon_autospawn=true`,
  fork daemon, retry connect dengan backoff (max 3s).
- Signal handling: SIGINT/SIGTERM = graceful shutdown (close clients dengan
  reason, flush cache, release PID file).

### Phase 4 — Move notify + image download
- Notify dipindah ke daemon: setiap `chat.message` event → daemon call
  `notify.Send` *sebelum* fan-out. Client cuma terima event untuk toast.
- Image download di-trigger oleh daemon ketika observe attachment/URL baru;
  emit `image.cached` saat selesai. Client subscribe → trigger render.
- Acceptance: tutup semua TUI, kirim chat dari device lain → notifikasi
  desktop tetap muncul, image tetap ke-cache.

### Phase 5 — Draft + multi-client polish
- Pindah autosave ke daemon (`DraftSave`/`DraftLoad`), key = `client_id +
  feature + thread_id`. Daemon write ke `~/.cache/gws/drafts/`.
- Multi-client: dua TUI attach barengan → kedua-duanya terima realtime,
  draft per-client tidak konflik.
- `gws daemon status` tampilkan list client (PID, attached_at, TTY).

### Phase 6 — Distribusi
- `launchd` plist contoh (macOS) di `docs/launchd/`.
- `systemd --user` unit contoh di `docs/systemd/`.
- README section: "Running gws as a daemon".

## Skenario edge case yang harus dipikirkan

1. **Daemon crash saat TUI connected.** RemoteClient deteksi EOF, set
   `m.err = "daemon disconnected"`, tampilkan banner, retry connect dengan
   exponential backoff (1s → 30s). TUI tetap responsive (cache in-memory
   masih ada, cuma read-only).
2. **TUI crash.** Daemon detect EPIPE → cleanup session, hentikan subscription
   yang tidak ada subscriber lain. Tidak boleh leak goroutine.
3. **Socket sudah ada tapi daemon mati.** Stale socket file. `daemon start`
   harus deteksi (try connect → fail → unlink → bind ulang). PID file
   stale handling sama.
4. **Auth token refresh.** Daemon yang trigger via primary `CommandClient`.
   Emit `auth.changed` kalau status berubah; client refresh banner.
5. **Cache file lock.** Daemon owner cache. Standalone TUI juga write ke
   file yang sama. **Solusi:** kalau `--daemon`, client *tidak boleh* write
   cache. Daemon punya advisory flock; standalone TUI juga ambil flock,
   gagal acquire = warning "another writer active, cache disabled".
6. **Version skew.** Client & daemon build berbeda. Tambah field `protocol_version`
   di `Snapshot` response; client refuse connect kalau mismatch (suggest
   restart daemon).
7. **Sensitive data di socket.** Socket `0600` di per-user dir = cukup.
   Tidak ada auth lain (single-user trust model).
8. **`gws tui --daemon` di SSH session tanpa $XDG_RUNTIME_DIR.** Fallback
   ke `~/.cache/gws/daemon.sock`.

## Testing strategy

- **Unit:** RemoteClient (mock conn dengan net.Pipe), daemon hub
  (in-memory clients), protocol encode/decode round-trip, snapshot apply.
- **Integration:** `internal/daemon/integration_test.go` — spawn daemon dengan
  `FixtureClient` sebagai backend, connect 2 RemoteClient, verify event
  fan-out, lifecycle (start/stop/restart).
- **Existing tests:** `internal/tui/...` harus tetap hijau tanpa modifikasi
  signifikan — `WorkspaceClient` interface tidak berubah.
- **Manual smoke matrix:**
  | Mode               | Test                                              |
  |--------------------|---------------------------------------------------|
  | standalone         | `gws tui`, semua fitur jalan (regression)         |
  | daemon, 1 client   | `gws daemon start --detach && gws tui --daemon`   |
  | daemon, 2 client   | dua tmux pane, kedua TUI realtime sync            |
  | daemon, no client  | notify masuk tanpa TUI terbuka                    |
  | daemon crash       | `kill -9 $(cat daemon.pid)` → TUI menampilkan banner, auto-reconnect setelah restart |

## Risiko & open question

- **Selection state per-client vs shared?** Plan ini: per-client. Kalau user
  attach dari 2 device dia mungkin malah mau independent navigation. Bisa
  dijadikan setting nanti.
- **Subscription cost.** Sekarang polling 5s per space yang dibuka. Di
  daemon mode, daemon bisa poll *semua* space terus-menerus (untuk notify),
  atau lazy (hanya space yang ada subscriber). MVP: lazy, sama seperti
  sekarang. Notify-tanpa-TUI butuh upgrade ke eager — keputusan setelah
  Phase 4.
- **Image cache concurrency.** Banyak client minta image yang sama → daemon
  dedup via `imageLoading` map + single-flight pattern.
- **Composer state.** Kalau TUI mati saat sedang ngetik, draft ke-recover
  dari daemon. Tapi cursor position hilang — acceptable.
- **Resource ceiling.** Daemon idle harus < 20MB RSS. Audit sebelum ship.

## Estimasi effort (rough)

| Phase                       | Effort       |
|-----------------------------|--------------|
| 0 — Seam tightening         | 0.5 hari     |
| 1 — Protokol + RemoteClient | 2 hari       |
| 2 — Daemon binary (minimal) | 2 hari       |
| 3 — Lifecycle (detach, signals, autospawn) | 1.5 hari |
| 4 — Notify + image to daemon | 1 hari      |
| 5 — Multi-client + draft    | 1 hari       |
| 6 — Distribusi (launchd/systemd, docs) | 0.5 hari |
| **Total**                   | **~8.5 hari** |

## Quick win sebelum daemon (worth mentioning)

Sebagian besar value daemon (proses tetap hidup, restart instan, terminal
sebagai viewport) **sudah bisa dicapai dengan tmux/zellij hari ini** tanpa
satu baris kode. Daemon mode worth dikerjakan kalau:
- Multi-client (real time sync antar pane) jadi requirement, atau
- Notify-tanpa-TUI jadi requirement, atau
- User base mau experience "buka-tutup instan" tanpa tahu tentang tmux.

Kalau ketiganya tidak penting, dokumentasikan resep tmux dulu — tunda
daemon work.
