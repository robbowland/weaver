#!/usr/bin/env bash
# For working on weaver: this checkout's build, as ~/.local/bin/wvr (ahead
# of Homebrew's on PATH), unreleased. To install weaver, use Homebrew:
# `brew install robbowland/kegs/weaver` (or --HEAD, for what is on main).
#
#   ./install.sh                  build wvr into ~/.local/bin, and say what is left (wvr doctor)
#     --configure                 also add the Claude Code (and Codex) hooks now
#                                 (`wvr setup`; wvr does it itself as it starts)
#     --no-codex                  leave Codex alone (default: add its hooks when ~/.codex exists)
#     --dry-run                   print what would change without writing anything
#
# Idempotent: rerunning refreshes the installed copy and skips what is there.
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
		sed -n '2,11p' "$0"
		exit 0
		;;
	*) die "unknown option: $1 (see --help)" ;;
	esac
	shift
done

# check_deps: Go, to build weaver. What weaver works with is wvr's to say
# (and `wvr doctor --fix`'s to install).
check_deps() {
	command -v go >/dev/null 2>&1 || die "Go is needed to build weaver: brew install go"
}

# ours FILE: is FILE a weaver binary?
ours() { go version -m "$1" 2>/dev/null | grep -q 'github.com/robbowland/weaver'; }

install_files() {
	if [ $dry = 1 ]; then
		say "would build $SRC into $BIN/wvr"
		return
	fi
	mkdir -p "$BIN"
	# Only ever replace weaver: a weaver binary, or (as weaver) a link an
	# older install made.
	if [ -e "$BIN/wvr" ] && ! ours "$BIN/wvr"; then
		die "$BIN/wvr exists and is not weaver; not replacing it"
	fi
	# Built beside it, then renamed over it: a hook running meanwhile runs the
	# old binary or the new one, never half of either.
	(cd "$SRC" && go build -trimpath -o "$BIN/.wvr.new" ./cmd/wvr) || die "cannot build weaver"
	# The command was weaver before it was wvr: hooks of Claude Code sessions
	# started then, and a home board open since, still run that path, so it
	# is kept the same build until you remove it.
	if [ -f "$BIN/weaver" ] && [ ! -L "$BIN/weaver" ] && ours "$BIN/weaver"; then
		cp -f "$BIN/.wvr.new" "$BIN/.weaver.new" && mv -f "$BIN/.weaver.new" "$BIN/weaver"
		say "kept $BIN/weaver, the old name, as this build: remove it once its sessions have restarted"
	fi
	mv -f "$BIN/.wvr.new" "$BIN/wvr"
	# Its files unpacked now (its Neovim plugin kept at $DEST/nvim for Neovim
	# configs), not when it first needs them.
	"$BIN/wvr" _runtime || warn "could not unpack weaver's files under $DEST"
	# The binary carries the Neovim plugin and the configs (it unpacks them
	# under $DEST/runtime). An older install's copies beside it
	# stay while what it started may still use them (an nvim loaded its plugin
	# from there).
	say "installed $BIN/wvr"
	case ":$PATH:" in
	*":$BIN:"*) ;;
	*) warn "$BIN is not on your PATH; weaver's tmux runs 'wvr' from PATH" ;;
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
	# The hooks: `wvr setup` (with --dry-run, by a build of this checkout
	# that is not installed).
	set -- --bin "$BIN/wvr"
	[ $codex = 1 ] || set -- "$@" --no-codex
	if [ $dry = 1 ]; then
		tmp=$(mktemp -d "${TMPDIR:-/tmp}/weaver.XXXXXX")
		(cd "$SRC" && go build -o "$tmp/wvr" ./cmd/wvr) || die "cannot build weaver"
		"$tmp/wvr" setup --dry-run "$@"
		rm -rf "$tmp"
	else
		"$BIN/wvr" setup "$@"
	fi
fi
# What is left (wvr doctor --fix does what it can of it).
if [ $dry = 0 ]; then
	echo
	"$BIN/wvr" doctor || true
fi
