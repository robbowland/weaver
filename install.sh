#!/usr/bin/env bash
# For working on weaver: this checkout's build, as ~/.local/bin/wvr (ahead
# of Homebrew's on PATH), unreleased. To install weaver, use Homebrew:
# `brew install robbowland/kegs/weaver` (or --HEAD, for what is on main).
#
#   ./install.sh                  build wvr into ~/.local/bin, and say what is left (wvr doctor)
#     --configure                 also add the Claude Code hooks now
#                                 (`wvr setup`; wvr does it itself as it starts)
#     --dry-run                   print what would change without writing anything
#
# Idempotent: rerunning refreshes the installed copy and skips what is there.
set -eu

SRC=$(cd "$(dirname "$0")" && pwd -P)
BIN="$HOME/.local/bin"

say() { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

configure=0 dry=0
while [ $# -gt 0 ]; do
	case $1 in
	--configure) configure=1 ;;
	--dry-run) dry=1 ;;
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
	if [ -e "$BIN/wvr" ] && ! ours "$BIN/wvr"; then
		die "$BIN/wvr exists and is not weaver; not replacing it"
	fi
	# Built beside it, then renamed over it: a hook running meanwhile runs the
	# old binary or the new one, never half of either.
	(cd "$SRC" && go build -trimpath -o "$BIN/.wvr.new" ./cmd/wvr) || die "cannot build weaver"
	mv -f "$BIN/.wvr.new" "$BIN/wvr"
	# The binary carries its configs, and unpacks them under
	# ~/.local/share/weaver/runtime when it first needs them.
	say "installed $BIN/wvr"
	case ":$PATH:" in
	*":$BIN:"*) ;;
	*) warn "$BIN is not on your PATH; the agents' hooks run 'wvr' from PATH" ;;
	esac
}

check_deps
install_files
if [ $configure = 1 ]; then
	# The hooks: `wvr setup` (with --dry-run, by a build of this checkout
	# that is not installed).
	set -- --bin "$BIN/wvr"
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
