# Reddit Launch Notes

Reddit launch goal: get useful feedback from terminal, Go, Neovim, and open
source users without posting like an ad.

## Before Posting

- [ ] The repository is public.
- [ ] The latest release tag exists and has downloadable artifacts.
- [ ] `go install github.com/fabhiansan/gws-tui/cmd/gws@latest` works.
- [ ] The README shows a screenshot, install command, supported features, and
  privacy notes.
- [ ] The post clearly says the first release targets macOS and Linux.
- [ ] You have read each target subreddit's rules.
- [ ] You are ready to answer comments, accept bug reports, and document known
  limitations.

## Possible Subreddits

Check rules before posting. Do not post to all of these at once.

- `r/commandline`
- `r/golang`
- `r/opensource`
- `r/neovim`, if framing around Neovim workflow compatibility
- `r/selfhosted`, only if the local-daemon angle is genuinely relevant

## Posting Rules

- Be explicit that you built it.
- Lead with the problem and technical tradeoffs, not hype.
- Include one link to the GitHub repository.
- Do not ask for upvotes.
- Do not use link shorteners.
- Do not cross-post aggressively.
- Do not hide known limitations.
- Reply to comments with useful technical detail, not canned promotion.

## Draft Post

Title options:

```text
I built a terminal UI for Google Workspace in Go
```

```text
gws-tui: a Bubble Tea terminal UI for Google Workspace
```

Body:

````text
I built `gws-tui`, a local terminal UI for Google Workspace.

The main reason was workflow friction: I already use terminal and Neovim heavily,
but Chat, Mail, Calendar, and Meet were still scattered across browser tabs. This
wraps the existing `gws` CLI with a Bubble Tea interface, keeps non-TUI commands
delegated to the upstream CLI, and keeps auth/API access local to the existing
CLI setup.

Current features:

- Chat, Mail, Calendar, and Meet panes
- Vim-style navigation
- Optional daemon mode for shared cache, polling, and notifications
- Kitty inline image previews
- Compatibility tests for the existing Neovim plugin command shapes

Current release targets are macOS and Linux.

Install:

```sh
go install github.com/fabhiansan/gws-tui/cmd/gws@latest
gws tui
```

Repo:
https://github.com/fabhiansan/gws-tui

I am looking for feedback from terminal users, especially around UX, auth/cache
expectations, daemon behavior, and whether the install/setup docs are clear.
````

## Known Limitations to Mention if Asked

- It depends on the upstream `gws` CLI for real Workspace auth and API access.
- If the upstream CLI is not discoverable as another `gws` on `PATH`, users need
  to set `GWS_TUI_UPSTREAM=/path/to/upstream/gws`.
- Daemon mode is optional and should be treated as advanced until more users
  exercise it across different OS environments.
- Inline image previews are best in Kitty; other terminals fall back to text.
- Windows is not part of the first release because daemon lifecycle management
  currently depends on Unix-style process and socket behavior.
