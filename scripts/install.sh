#!/usr/bin/env bash
#
# install.sh — one-shot installer for gws-tui.
#
# After `git clone`, run:   bash scripts/install.sh
#
# What it does:
#   Phase A  Installs the upstream Google Workspace CLI (@googleworkspace/cli)
#            and builds + installs this repo's `gws` TUI binary.
#   Phase B  Walks you through a bring-your-own Google Cloud project so the
#            TUI can authenticate, then runs the OAuth login.
#
# Options:
#   --no-auth    Skip Phase B (Google Cloud setup + login).
#   --no-color   Disable coloured output.
#   -h, --help   Show this help and exit.

set -euo pipefail

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
OPT_NO_AUTH=0
OPT_NO_COLOR=0

usage() {
	cat <<'EOF'
gws-tui installer

Usage: bash scripts/install.sh [options]

Options:
  --no-auth    Skip the Google Cloud project setup and login step.
  --no-color   Disable coloured output.
  -h, --help   Show this help and exit.
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--no-auth)  OPT_NO_AUTH=1 ;;
		--no-color) OPT_NO_COLOR=1 ;;
		-h|--help)  usage; exit 0 ;;
		*) echo "unknown option: $1" >&2; usage >&2; exit 2 ;;
	esac
	shift
done

# ---------------------------------------------------------------------------
# Output helpers
# ---------------------------------------------------------------------------
if [[ -t 1 && $OPT_NO_COLOR -eq 0 && -z "${NO_COLOR:-}" ]]; then
	C_RESET=$'\033[0m'; C_RED=$'\033[31m'; C_GREEN=$'\033[32m'
	C_YELLOW=$'\033[33m'; C_BLUE=$'\033[34m'; C_BOLD=$'\033[1m'
else
	C_RESET=''; C_RED=''; C_GREEN=''; C_YELLOW=''; C_BLUE=''; C_BOLD=''
fi

step() { printf '\n%s\n' "${C_BOLD}${C_BLUE}::${C_RESET} ${C_BOLD}$*${C_RESET}"; }
info() { printf '%s\n' "${C_BLUE}  ->${C_RESET} $*"; }
ok()   { printf '%s\n' "${C_GREEN}  ok${C_RESET} $*"; }
warn() { printf '%s\n' "${C_YELLOW}   !${C_RESET} $*" >&2; }
die()  { printf '%s\n' "${C_RED}error:${C_RESET} $*" >&2; exit 1; }

have() { command -v "$1" >/dev/null 2>&1; }

# Returns 0 only if the upstream CLI holds a usable Workspace credential.
# `gws auth status` exits 0 even when signed out, so inspect its JSON instead.
upstream_authenticated() {
	local json
	json="$("$UPSTREAM_GWS" auth status 2>/dev/null)" || return 1
	[[ "$json" == *'"auth_method"'* ]] || return 1
	case "$json" in
		*'"auth_method": "none"'* | *'"auth_method":"none"'*) return 1 ;;
	esac
	return 0
}

trap 'printf "%s\n" "${C_RED}install.sh failed at line ${LINENO}.${C_RESET}" >&2' ERR

# ---------------------------------------------------------------------------
# Phase 0 — locate the repository root
# ---------------------------------------------------------------------------
SCRIPT_DIR="$(cd "$(dirname "${BASH_SOURCE[0]}")" && pwd)"
REPO_ROOT="$(cd "$SCRIPT_DIR/.." && pwd)"
cd "$REPO_ROOT"
[[ -f go.mod ]] || die "go.mod not found in $REPO_ROOT — run this from a clone of gws-tui."

# ---------------------------------------------------------------------------
# Phase 1 — prerequisites
# ---------------------------------------------------------------------------
step "Checking prerequisites"
have go || die "Go toolchain not found. Install Go from https://go.dev/dl/ (or 'brew install go'), then re-run."
ok "go found: $(go version | awk '{print $3}')"

# ---------------------------------------------------------------------------
# Phase 2 — install the upstream Google Workspace CLI
# ---------------------------------------------------------------------------
step "Installing upstream Google Workspace CLI (gws)"

# Echoes the upstream binary path on stdout, returns non-zero if not installed.
detect_upstream() {
	local p
	if have brew && brew list --formula googleworkspace-cli >/dev/null 2>&1; then
		p="$(brew --prefix)/bin/gws"
		[[ -x "$p" ]] && { printf '%s' "$p"; return 0; }
	fi
	if have npm && npm ls -g --depth=0 @googleworkspace/cli >/dev/null 2>&1; then
		p="$(npm prefix -g)/bin/gws"
		[[ -x "$p" ]] && { printf '%s' "$p"; return 0; }
	fi
	return 1
}

UPSTREAM_GWS=""
if UPSTREAM_GWS="$(detect_upstream)"; then
	ok "upstream gws already installed: $UPSTREAM_GWS"
else
	if have brew; then
		info "installing via Homebrew: brew install googleworkspace-cli"
		brew install googleworkspace-cli
	elif have npm; then
		info "installing via npm: npm install -g @googleworkspace/cli"
		npm install -g @googleworkspace/cli
	else
		die "Need Homebrew or npm to install the upstream gws CLI.
  Homebrew: https://brew.sh    |    Node/npm: https://nodejs.org
  Install one, then re-run."
	fi
	UPSTREAM_GWS="$(detect_upstream)" || UPSTREAM_GWS="$(command -v gws || true)"
	[[ -n "$UPSTREAM_GWS" && -x "$UPSTREAM_GWS" ]] \
		|| die "upstream gws installed but its binary could not be located."
	ok "upstream gws installed: $UPSTREAM_GWS"
fi

# ---------------------------------------------------------------------------
# Phase 3 — build and install the TUI binary from this repo
# ---------------------------------------------------------------------------
step "Building and installing the gws TUI"

GOBIN_DIR="$(go env GOBIN)"
[[ -n "$GOBIN_DIR" ]] || GOBIN_DIR="$(go env GOPATH)/bin"
LOCAL_BIN="$HOME/.local/bin"
UPSTREAM_DIR="$(dirname "$UPSTREAM_GWS")"

# 1-based index of a directory within PATH; 99999 when it is absent.
path_pos() {
	local target="$1" i=1 d
	local IFS=:
	for d in $PATH; do
		[[ "$d" == "$target" ]] && { printf '%s' "$i"; return 0; }
		i=$((i + 1))
	done
	printf '%s' 99999
}
UPSTREAM_POS="$(path_pos "$UPSTREAM_DIR")"

# The TUI binary and the upstream CLI are both named `gws`. Install the TUI
# into a PATH directory that resolves *before* the upstream, otherwise typing
# `gws tui` would hit the upstream CLI (which has no `tui` command) and fail.
PATH_HINT=""
if [[ "$(path_pos "$GOBIN_DIR")" -lt "$UPSTREAM_POS" ]]; then
	info "go install ./cmd/gws  ->  $GOBIN_DIR"
	go install ./cmd/gws
	TUI_GWS="$GOBIN_DIR/gws"
else
	mkdir -p "$LOCAL_BIN"
	info "go build  ->  $LOCAL_BIN/gws"
	go build -o "$LOCAL_BIN/gws" ./cmd/gws
	TUI_GWS="$LOCAL_BIN/gws"
	# Flag a PATH change only when ~/.local/bin won't win over the upstream.
	[[ "$(path_pos "$LOCAL_BIN")" -lt "$UPSTREAM_POS" ]] || PATH_HINT="$LOCAL_BIN"
fi
ok "TUI installed: $TUI_GWS"

# ---------------------------------------------------------------------------
# Phase 4 — PATH sanity check
# ---------------------------------------------------------------------------
step "Checking PATH"

hash -r 2>/dev/null || true
RESOLVED_GWS="$(command -v gws || true)"
if [[ -z "$RESOLVED_GWS" ]]; then
	warn "no 'gws' found on PATH yet — see the PATH note below."
elif [[ "$RESOLVED_GWS" == "$TUI_GWS" ]]; then
	ok "'gws' resolves to the TUI: $RESOLVED_GWS"
elif [[ "$RESOLVED_GWS" == "$UPSTREAM_GWS" ]]; then
	warn "'gws' resolves to the UPSTREAM CLI, not the TUI ($RESOLVED_GWS)."
	warn "  'gws tui' will fail until the TUI directory comes first on PATH:"
	warn "  put '$(dirname "$TUI_GWS")' ahead of '$(dirname "$UPSTREAM_GWS")'."
else
	info "'gws' resolves to: $RESOLVED_GWS"
fi

if [[ -n "$PATH_HINT" ]]; then
	warn "$PATH_HINT must come before $UPSTREAM_DIR on PATH for 'gws tui' to work."
	warn "Add this to your shell rc (~/.zshrc), then restart your shell:"
	printf '\n    export PATH="%s:$PATH"\n' "$PATH_HINT"
fi

# Safety net: the TUI honours GWS_TUI_UPSTREAM before scanning PATH, so this
# guarantees it finds the upstream CLI regardless of PATH ordering.
info "Optional safety net — add to your shell rc so the TUI always finds upstream:"
printf '\n    export GWS_TUI_UPSTREAM=%q\n' "$UPSTREAM_GWS"

# ---------------------------------------------------------------------------
# Phase B — bring-your-own Google Cloud project + authentication
# ---------------------------------------------------------------------------
print_manual_setup() {
	cat <<'EOF'

  Manual Google Cloud setup
  -------------------------
  1. Create (or pick) a Google Cloud project:
       https://console.cloud.google.com/projectcreate

  2. Enable the APIs the TUI uses (open each link, click "Enable"):
       Gmail     https://console.cloud.google.com/apis/library/gmail.googleapis.com
       Chat      https://console.cloud.google.com/apis/library/chat.googleapis.com
       Calendar  https://console.cloud.google.com/apis/library/calendar-json.googleapis.com
       Meet      https://console.cloud.google.com/apis/library/meet.googleapis.com

  3. Configure the OAuth consent screen — User type "External", and add your
     own Google account under "Test users":
       https://console.cloud.google.com/apis/credentials/consent

  4. Create credentials -> "OAuth client ID", Application type "Desktop app".
     Download the JSON file it offers.
       https://console.cloud.google.com/apis/credentials

  (Tip: install the gcloud CLI to let this script create the project and
   OAuth client for you, skipping all the manual steps above.)

EOF
}

# Offer to install the gcloud CLI (macOS/Homebrew) so 'gws auth setup' can run.
offer_install_gcloud() {
	local reply
	if [[ "$(uname -s)" != "Darwin" ]] || ! have brew; then
		info "Install the gcloud CLI to enable automated setup:"
		info "  https://cloud.google.com/sdk/docs/install"
		return
	fi
	info "The gcloud CLI is not installed — 'gws auth setup' needs it."
	printf '%s' "  Install gcloud now via Homebrew? [y/N] "
	read -r reply || reply=""
	case "${reply:-N}" in
		[Yy]*)
			if brew install --cask google-cloud-sdk; then
				hash -r 2>/dev/null || true
				if have gcloud; then
					ok "gcloud installed."
				else
					warn "gcloud installed but not on PATH in this shell."
					warn "open a new terminal, then re-run:  bash scripts/install.sh"
				fi
			else
				warn "gcloud install failed — using manual path."
			fi
			;;
		*) info "skipping gcloud install; using manual path." ;;
	esac
}

if [[ $OPT_NO_AUTH -eq 1 ]]; then
	step "Skipping Google Cloud setup (--no-auth)"
	info "finish later with:  gws auth login"
else
	step "Google Workspace authentication"

	if upstream_authenticated; then
		ok "already authenticated with Google Workspace."
	elif [[ ! -t 0 ]]; then
		warn "not an interactive terminal — skipping auth."
		warn "finish later with:  gws auth login"
	else
		SETUP_DONE=0

		# Does this upstream build expose the automated 'auth setup' command?
		HAS_SETUP=0
		"$UPSTREAM_GWS" auth setup --help >/dev/null 2>&1 && HAS_SETUP=1

		if [[ $HAS_SETUP -eq 1 ]]; then
			# 'gws auth setup' automates the project + OAuth client but needs
			# the gcloud CLI; offer to install it when it is missing.
			have gcloud || offer_install_gcloud
			if have gcloud; then
				info "gcloud + 'gws auth setup' can create your Google Cloud"
				info "project and OAuth client automatically."
				printf '%s' "  Run 'gws auth setup' now? [Y/n] "
				read -r reply || reply=""
				case "${reply:-Y}" in
					[Nn]*) info "skipping automated setup; using manual path." ;;
					*)
						if "$UPSTREAM_GWS" auth setup; then
							SETUP_DONE=1
						else
							warn "'gws auth setup' did not complete — using manual path."
							warn "  (if gcloud is not logged in: run 'gcloud auth login', then re-run.)"
						fi
						;;
				esac
			else
				info "'gws auth setup' needs gcloud — using manual path."
			fi
		fi

		# Manual path: user creates the OAuth client, we install the JSON.
		if [[ $SETUP_DONE -eq 0 ]]; then
			print_manual_setup
			CRED_DEST="$HOME/.config/gws/client_secret.json"
			printf '%s' "  Path to the downloaded OAuth client JSON (Enter to skip): "
			read -r cred_src || cred_src=""
			cred_src="${cred_src/#\~/$HOME}"
			if [[ -z "$cred_src" ]]; then
				warn "no credentials provided. Finish later by placing the JSON at:"
				warn "  $CRED_DEST   then run:  gws auth login"
			elif [[ ! -f "$cred_src" ]]; then
				die "file not found: $cred_src"
			else
				mkdir -p "$(dirname "$CRED_DEST")"
				cp "$cred_src" "$CRED_DEST"
				chmod 600 "$CRED_DEST"
				ok "credentials installed at $CRED_DEST"
				SETUP_DONE=1
			fi
		fi

		# Run the OAuth login unless setup already produced a valid session.
		if [[ $SETUP_DONE -eq 1 ]]; then
			if upstream_authenticated; then
				ok "authenticated with Google Workspace."
			else
				info "starting OAuth login — a browser window will open..."
				if "$UPSTREAM_GWS" auth login; then
					ok "authenticated with Google Workspace."
				else
					warn "login did not complete — re-run:  gws auth login"
				fi
			fi
		fi
	fi
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
step "Done"
ok "upstream gws : $UPSTREAM_GWS"
ok "gws TUI      : $TUI_GWS"
printf '\n%s\n' "Next steps:"
echo "    gws tui                  # launch the TUI"
echo "    gws tui --feature mail   # jump straight to a feature"
[[ -n "$PATH_HINT" ]] && echo "    (apply the PATH export above first, then restart your shell)"
echo
