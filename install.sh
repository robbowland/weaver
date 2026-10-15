#!/usr/bin/env bash
# Install weaver; with --configure, also wire it into Claude Code (and Codex).
#
#   ./install.sh                  build weaver (one binary) into ~/.local/bin/weaver
#   ./install.sh --configure      also add the Claude Code (and Codex) hooks (`weaver setup`)
#     --no-codex                  leave Codex alone (default: add its hooks when ~/.codex exists)
#     --dry-run                   print what would change without writing anything
#
# Idempotent: rerunning refreshes the installed copy and skips config already present.
set -eu

SRC=$(cd "$(dirname "$0")" && pwd -P)
DEST="${XDG_DATA_HOME:-$HOME/.local/share}/weaver"
BIN="$HOME/.local/bin"

say() { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

configure=0 dry=0 codex=1
while [ $# -gt 0 ]; do
	case $1 in
	--configure) configure=1 ;;
	--dry-run) dry=1 ;;
	--no-codex) codex=0 ;;
	--zellij-config) die "weaver runs on tmux only now: --zellij-config is gone (remove weaver's keybindings from your zellij config)" ;;
	-h | --help)
		sed -n '2,9p' "$0"
		exit 0
		;;
	*) die "unknown option: $1 (see --help)" ;;
	esac
	shift
done

check_deps() {
	local c missing="" v major minor rest
	for c in go tmux nvim git; do
		command -v "$c" >/dev/null 2>&1 || missing="$missing $c"
	done
	[ -z "$missing" ] || die "missing dependencies:$missing"
	command -v fzf >/dev/null 2>&1 ||
		warn "fzf not found: 'weaver ls' will print a table instead of the interactive dashboard"
	v=$(tmux -V 2>/dev/null)
	v=${v#tmux }
	major=${v%%.*} rest=${v#*.}
	minor=${rest%%[!0-9]*}
	case "$major.$minor" in [0-9]*.[0-9]*) ;; *) die "cannot parse the tmux version '$v'" ;; esac
	if [ "$major" -lt 3 ] || { [ "$major" -eq 3 ] && [ "$minor" -lt 3 ]; }; then
		die "tmux $v is too old: weaver needs 3.3 or newer"
	fi
}

install_files() {
	if [ $dry = 1 ]; then
		say "would build $SRC into $BIN/weaver"
		return
	fi
	mkdir -p "$BIN"
	# Only ever replace weaver: a link an older install made, or a weaver binary.
	if [ -e "$BIN/weaver" ] && [ ! -L "$BIN/weaver" ] &&
		! go version -m "$BIN/weaver" 2>/dev/null | grep -q 'github.com/robbowland/weaver'; then
		die "$BIN/weaver exists and is not weaver; not replacing it"
	fi
	# Built beside it, then renamed over it: a hook running meanwhile runs the
	# old binary or the new one, never half of either.
	(cd "$SRC" && go build -trimpath -o "$BIN/.weaver.new" ./cmd/weaver) || die "cannot build weaver"
	mv -f "$BIN/.weaver.new" "$BIN/weaver"
	# Its files unpacked now (its Neovim plugin kept at $DEST/nvim for Neovim
	# configs), not when it first needs them.
	"$BIN/weaver" _runtime || warn "could not unpack weaver's files under $DEST"
	# The binary carries the script, the Neovim plugin and the configs (it
	# unpacks them under $DEST/runtime). An older install's copies beside it
	# stay while what it started may still use them (an nvim loaded its plugin
	# from there).
	say "installed $BIN/weaver"
	case ":$PATH:" in
	*":$BIN:"*) ;;
	*) warn "$BIN is not on your PATH; tmux and Claude Code run 'weaver' from PATH" ;;
	esac
}

# Before weaver was one binary, install.sh copied its script, itself and
# config/ into $DEST and linked the script into $BIN. The binary carries
# them now ($DEST/nvim stays: the binary keeps it, for Neovim configs).
# Remove those copies, but only ones that look like what it wrote.
remove_old_copies() {
	local f gone=()
	for f in "$DEST/weaver" "$DEST/install.sh"; do
		if [ -f "$f" ] && [ ! -L "$f" ] && head -n 1 "$f" 2>/dev/null | grep -q '^#!.*bash'; then
			gone+=("$f")
		fi
	done
	if [ -d "$DEST/config" ] && [ -f "$DEST/config/claude-hooks.json" ]; then
		gone+=("$DEST/config")
	fi
	[ ${#gone[@]} -gt 0 ] || return 0
	if [ $dry = 1 ]; then
		say "would remove the old install's copies: ${gone[*]}"
		return
	fi
	rm -rf "${gone[@]}"
	say "removed the old install's copies: ${gone[*]}"
}

# weaver was called agentnav: remove that install, but only the copy and the
# link install.sh made, never a file it did not create.
remove_agentnav() {
	local old="${XDG_DATA_HOME:-$HOME/.local/share}/agentnav"
	if [ -L "$BIN/agentnav" ] && [ "$(readlink "$BIN/agentnav")" = "$old/agentnav" ]; then
		if [ $dry = 1 ]; then
			say "would remove the old agentnav install: $BIN/agentnav and $old"
		else
			rm -f "$BIN/agentnav"
			rm -rf "$old"
			say "removed the old agentnav install: $BIN/agentnav and $old"
		fi
	fi
}

check_deps
install_files
remove_old_copies
remove_agentnav
if [ $configure = 1 ]; then
	# The hooks: `weaver setup` (with --dry-run, by a build of this checkout
	# that is not installed).
	set -- --bin "$BIN/weaver"
	[ $codex = 1 ] || set -- "$@" --no-codex
	if [ $dry = 1 ]; then
		tmp=$(mktemp -d "${TMPDIR:-/tmp}/weaver.XXXXXX")
		(cd "$SRC" && go build -o "$tmp/weaver" ./cmd/weaver) || die "cannot build weaver"
		"$tmp/weaver" setup --dry-run "$@"
		rm -rf "$tmp"
	else
		"$BIN/weaver" setup "$@"
	fi
fi
