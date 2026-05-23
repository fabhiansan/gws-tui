#!/usr/bin/env bash
#
# uninstall.sh — remove what scripts/install.sh set up.
#
# Run:   bash scripts/uninstall.sh [options]
#
# By default it stops the daemon, logs out of Google Workspace (clearing the
# token cache and OS keyring entry), removes stored credentials and the TUI
# binary, and uninstalls the upstream Google Workspace CLI.
#
# Options:
#   --purge          Also delete ~/.config/gws and ~/.cache/gws entirely
#                    (TUI config, UI state, caches, image data).
#   --keep-upstream  Do not uninstall the upstream @googleworkspace/cli.
#   --no-color       Disable coloured output.
#   -h, --help       Show this help and exit.

set -euo pipefail

# ---------------------------------------------------------------------------
# Argument parsing
# ---------------------------------------------------------------------------
OPT_PURGE=0
OPT_KEEP_UPSTREAM=0
OPT_NO_COLOR=0

usage() {
	cat <<'EOF'
gws-tui uninstaller

Usage: bash scripts/uninstall.sh [options]

Options:
  --purge          Also delete ~/.config/gws and ~/.cache/gws entirely.
  --keep-upstream  Do not uninstall the upstream @googleworkspace/cli.
  --no-color       Disable coloured output.
  -h, --help       Show this help and exit.
EOF
}

while [[ $# -gt 0 ]]; do
	case "$1" in
		--purge)         OPT_PURGE=1 ;;
		--keep-upstream) OPT_KEEP_UPSTREAM=1 ;;
		--no-color)      OPT_NO_COLOR=1 ;;
		-h|--help)       usage; exit 0 ;;
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

trap 'printf "%s\n" "${C_RED}uninstall.sh failed at line ${LINENO}.${C_RESET}" >&2' ERR

[[ -n "${HOME:-}" ]] || die "\$HOME is not set — refusing to run."
CFG_DIR="$HOME/.config/gws"
CACHE_DIR="$HOME/.cache/gws"

# ---------------------------------------------------------------------------
# Phase 1 — stop the daemon and clear the Workspace session
# ---------------------------------------------------------------------------
step "Clearing Google Workspace session"
if have gws-tui; then
	if gws-tui daemon stop >/dev/null 2>&1; then
		ok "daemon stopped"
	else
		info "daemon not running"
	fi
else
	info "no 'gws-tui' on PATH — skipping daemon stop"
fi
if have gws; then
	if gws auth logout >/dev/null 2>&1; then
		ok "logged out (token cache + OS keyring cleared)"
	else
		info "nothing to log out of"
	fi
else
	info "no upstream 'gws' on PATH — skipping logout"
fi

# ---------------------------------------------------------------------------
# Phase 2 — remove stored credential files
# ---------------------------------------------------------------------------
step "Removing stored credentials"
removed=0
for f in client_secret.json credentials.enc token_cache.json; do
	if [[ -e "$CFG_DIR/$f" ]]; then
		rm -f "$CFG_DIR/$f"
		ok "removed $CFG_DIR/$f"
		removed=1
	fi
done
[[ $removed -eq 1 ]] || info "no credential files found"

# ---------------------------------------------------------------------------
# Phase 3 — remove the gws-tui binary
# ---------------------------------------------------------------------------
step "Removing the gws-tui binary"
if have go; then
	GOBIN_DIR="$(go env GOBIN 2>/dev/null || true)"
	[[ -n "$GOBIN_DIR" ]] || GOBIN_DIR="$(go env GOPATH 2>/dev/null || echo "$HOME/go")/bin"
else
	GOBIN_DIR="$HOME/go/bin"
fi
removed=0
for p in "$GOBIN_DIR/gws-tui" "$HOME/.local/bin/gws-tui"; do
	if [[ -e "$p" || -L "$p" ]]; then
		rm -f "$p"
		ok "removed $p"
		removed=1
	fi
done
[[ $removed -eq 1 ]] || info "no gws-tui binary found"

# ---------------------------------------------------------------------------
# Phase 4 — uninstall the upstream Google Workspace CLI
# ---------------------------------------------------------------------------
if [[ $OPT_KEEP_UPSTREAM -eq 1 ]]; then
	step "Keeping upstream gws (--keep-upstream)"
else
	step "Uninstalling upstream Google Workspace CLI"
	if have brew && brew list --formula googleworkspace-cli >/dev/null 2>&1; then
		brew uninstall googleworkspace-cli
		ok "removed Homebrew formula: googleworkspace-cli"
	elif have npm && npm ls -g --depth=0 @googleworkspace/cli >/dev/null 2>&1; then
		npm uninstall -g @googleworkspace/cli
		ok "removed npm package: @googleworkspace/cli"
	else
		info "upstream gws not installed via brew or npm — nothing to remove"
	fi
fi

# ---------------------------------------------------------------------------
# Phase 5 — optionally purge config, state, and caches
# ---------------------------------------------------------------------------
if [[ $OPT_PURGE -eq 1 ]]; then
	step "Purging config, state, and caches (--purge)"
	for d in "$CFG_DIR" "$CACHE_DIR"; do
		if [[ -d "$d" ]]; then
			rm -rf "$d"
			ok "removed $d"
		fi
	done
else
	step "Keeping config and caches"
	info "left in place (use --purge to remove):"
	info "  $CFG_DIR"
	info "  $CACHE_DIR"
fi

# ---------------------------------------------------------------------------
# Summary
# ---------------------------------------------------------------------------
step "Done"
info "gws-tui and its credentials have been removed."
[[ $OPT_PURGE -eq 0 ]] && info "Re-run with --purge to also wipe config/state/caches."
info "Reinstall any time with:  bash scripts/install.sh"
echo
