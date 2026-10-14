#!/usr/bin/env bash
# Install weaver; with --configure, also wire it into zellij and Claude Code.
#
#   ./install.sh                  copy to ~/.local/share/weaver, link ~/.local/bin/weaver
#   ./install.sh --configure      also add the zellij keybindings and the Claude Code (and Codex) hooks
#     --zellij-config FILE        zellij config to edit; repeatable (default: $ZELLIJ_CONFIG_FILE,
#                                 else ~/.config/zellij/config.kdl). FILE.in templates are
#                                 edited but not validated.
#     --no-codex                  leave Codex alone (default: add its hooks when ~/.codex exists)
#     --dry-run                   print what would change without writing anything
#
# Idempotent: rerunning refreshes the installed copy and skips config already present.
set -eu

NL='
'
SRC=$(cd "$(dirname "$0")" && pwd -P)
DEST="${XDG_DATA_HOME:-$HOME/.local/share}/weaver"
BIN="$HOME/.local/bin"
CLAUDE_SETTINGS="${CLAUDE_CONFIG_DIR:-$HOME/.claude}/settings.json"
CODEX_DIR="${CODEX_HOME:-$HOME/.codex}"
CODEX_DONE=0
TS=$(date +%Y%m%d-%H%M%S)

say() { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

configure=0 dry=0 zconfs="" codex=1
while [ $# -gt 0 ]; do
	case $1 in
	--configure) configure=1 ;;
	--dry-run) dry=1 ;;
	--no-codex) codex=0 ;;
	--zellij-config)
		[ $# -ge 2 ] || die "--zellij-config needs a file"
		zconfs="$zconfs$2$NL"
		shift
		;;
	-h | --help)
		sed -n '2,13p' "$0"
		exit 0
		;;
	*) die "unknown option: $1 (see --help)" ;;
	esac
	shift
done

check_deps() {
	local c missing="" v major minor rest
	for c in zellij nvim jq git; do
		command -v "$c" >/dev/null 2>&1 || missing="$missing $c"
	done
	[ -z "$missing" ] || die "missing dependencies:$missing"
	command -v fzf >/dev/null 2>&1 ||
		warn "fzf not found: 'weaver ls' will print a table instead of the interactive dashboard"
	v=$(zellij --version 2>/dev/null)
	v=${v##* }
	major=${v%%.*} rest=${v#*.}
	minor=${rest%%.*}
	case "$major.$minor" in [0-9]*.[0-9]*) ;; *) die "cannot parse the zellij version '$v'" ;; esac
	if [ "$major" -eq 0 ] && [ "$minor" -lt 45 ]; then
		die "zellij $v is too old: weaver needs 0.45 or newer"
	fi
}

install_files() {
	if [ $dry = 1 ]; then
		say "would install $SRC into $DEST and link $BIN/weaver"
		return
	fi
	if [ "$SRC" != "$(cd "$DEST" 2>/dev/null && pwd -P)" ]; then
		# Into a fresh copy, then swapped in: a hook running meanwhile sees the
		# old install or the new one, never half of each.
		rm -rf "$DEST.new"
		mkdir -p "$DEST.new/config"
		cp "$SRC/weaver" "$SRC/install.sh" "$DEST.new/"
		cp -R "$SRC/nvim" "$DEST.new/"
		cp "$SRC"/config/* "$DEST.new/config/"
		chmod 755 "$DEST.new/weaver" "$DEST.new/install.sh"
		rm -rf "$DEST.old"
		mkdir -p "$(dirname "$DEST")"
		if [ -e "$DEST" ]; then mv "$DEST" "$DEST.old"; fi
		mv "$DEST.new" "$DEST"
		rm -rf "$DEST.old"
	fi
	mkdir -p "$BIN"
	if [ -e "$BIN/weaver" ] && [ ! -L "$BIN/weaver" ]; then
		die "$BIN/weaver exists and is not a symlink; not replacing it"
	fi
	ln -sfn "$DEST/weaver" "$BIN/weaver"
	say "installed $DEST/weaver, linked from $BIN/weaver"
	case ":$PATH:" in
	*":$BIN:"*) ;;
	*) warn "$BIN is not on your PATH; zellij and Claude Code run 'weaver' from PATH" ;;
	esac
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

# show_or_apply LABEL FILE NEW: print the diff; unless --dry-run, back FILE up
# and write NEW into it (through the file, so a symlinked config stays a link).
show_or_apply() {
	local label=$1 f=$2 new=$3
	say "--- $label: $f"
	diff -u "$([ -f "$f" ] && echo "$f" || echo /dev/null)" "$new" || true
	[ $dry = 1 ] && return 1
	# Inside `if`, set -e is off: check each step, never overwrite without a backup.
	if [ -f "$f" ]; then
		cp -p "$f" "$f.bak.$TS" || die "cannot back up $f; not changing it"
		say "backed up to $f.bak.$TS"
	else
		mkdir -p "$(dirname "$f")" || die "cannot create $(dirname "$f")"
	fi
	cat "$new" >"$f" || die "cannot write $f (backup: $f.bak.$TS)"
}

# The weaver block in a zellij config: from its "// weaver:" comment (or
# "// agentnav:", its old name) through the brace closing the block after it,
# leading whitespace stripped.
AWK_BLOCK='
	/^[[:space:]]*\/\/ (weaver|agentnav):/ { inblock = 1 }
	inblock {
		line = $0
		sub(/^[[:space:]]+/, "", line)
		print line
		if (line !~ /^\/\//) {
			depth += gsub(/[{]/, "{") - gsub(/[}]/, "}")
			started = 1
		}
		if (started && depth <= 0) exit
	}'

configure_zellij() {
	local f=$1 tmp
	tmp=$(mktemp "${TMPDIR:-/tmp}/weaver.XXXXXX")
	if [ -f "$f" ] && grep -Eq 'Run "(weaver|agentnav)"' "$f"; then
		if [ "$(awk "$AWK_BLOCK" "$f")" = "$(sed 's/^[[:space:]]*//' "$SRC/config/zellij-keybinds.kdl")" ]; then
			say "zellij: $f already has the current weaver keybindings"
			rm -f "$tmp"
			return
		fi
		if ! grep -Eq '^[[:space:]]*// (weaver|agentnav):' "$f"; then
			warn "zellij: $f has weaver keybindings without the '// weaver:' comment; update them by hand from $SRC/config/zellij-keybinds.kdl"
			rm -f "$tmp"
			return
		fi
		# Replace the old block with the current one, at the same indentation.
		awk -v snip="$SRC/config/zellij-keybinds.kdl" '
			!done && /^[[:space:]]*\/\/ (weaver|agentnav):/ {
				ind = $0
				sub(/[^[:space:]].*/, "", ind)
				while ((getline l < snip) > 0) print (l == "" ? "" : ind l)
				skip = 1
				done = 1
				next
			}
			skip {
				depth += gsub(/[{]/, "{") - gsub(/[}]/, "}")
				if ($0 ~ /[{}]/) started = 1
				if (started && depth <= 0) skip = 0
				next
			}
			{ print }' "$f" >"$tmp"
	elif [ -f "$f" ] && grep -Eq '^[[:space:]]*keybinds([[:space:]]+clear-defaults=true)?[[:space:]]*[{]' "$f"; then
		# Insert just inside the first top-level keybinds block.
		awk -v snip="$SRC/config/zellij-keybinds.kdl" '
			!done && /^[[:space:]]*keybinds([[:space:]]+clear-defaults=true)?[[:space:]]*[{]/ {
				print
				while ((getline l < snip) > 0) print (l == "" ? "" : "    " l)
				done = 1
				next
			}
			{ print }' "$f" >"$tmp"
	else
		{
			if [ -f "$f" ]; then cat "$f" && echo; fi
			echo "keybinds {"
			sed 's/^/    /' "$SRC/config/zellij-keybinds.kdl"
			echo "}"
		} >"$tmp"
	fi
	if show_or_apply zellij "$f" "$tmp"; then
		case $f in
		*.in) say "zellij: $f is a template; not validated" ;;
		*)
			if zellij --config "$f" setup --check 2>&1 | grep -q 'Well defined'; then
				say "zellij: $f is well defined"
			else
				[ -f "$f.bak.$TS" ] && cat "$f.bak.$TS" >"$f"
				rm -f "$tmp"
				die "zellij rejected $f; restored the backup. Check with: zellij --config $f setup --check"
			fi
			;;
		esac
	fi
	rm -f "$tmp"
}

# Remove every weaver hook (whatever its form: "weaver hook", an exec-form
# "weaver" with args, an absolute path to it, or agentnav, weaver's old name)
# and add the current ones, so a changed matcher or event never leaves a stale
# entry behind. Hooks of anything else are never touched; a matcher group left
# empty is dropped.
JQ_HOOKS='
def ours: (.command? // "" | tostring) | test("^(\\S*/)?(weaver|agentnav)( |$)");
($add[0] | walk(if type == "object" and (.command? | type) == "string" then
    .command |= (if . == "weaver" then $bin elif startswith("weaver ") then $bin + .[6:] else . end)
  else . end)) as $new
| .hooks = ((.hooks // {})
    | with_entries(.value |= (map(.hooks |= map(select(ours | not))) | map(select((.hooks | length) > 0))))
    | with_entries(select(.value | length > 0)))
| reduce ($new.hooks | to_entries[]) as $e (.; .hooks[$e.key] = ((.hooks[$e.key] // []) + $e.value))'

# configure_hooks LABEL FILE HOOKS-JSON: weaver's hooks into an agent's JSON
# hooks file (Claude Code's settings.json, Codex's hooks.json).
configure_hooks() {
	local label=$1 f=$2 hooks=$3 tmp
	tmp=$(mktemp "${TMPDIR:-/tmp}/weaver.XXXXXX")
	if [ -f "$f" ]; then cat "$f"; else echo '{}'; fi |
		jq --slurpfile add "$hooks" --arg bin "$BIN/weaver" "$JQ_HOOKS" >"$tmp" ||
		die "cannot parse $f"
	if [ -f "$f" ] && jq -e --slurpfile new "$tmp" '. == $new[0]' "$f" >/dev/null; then
		say "$label: $f already runs weaver on every event"
	elif show_or_apply "$label" "$f" "$tmp"; then
		jq empty "$f" || die "wrote invalid JSON to $f; the backup is $f.bak.$TS"
	fi
	rm -f "$tmp"
}

configure_claude() { configure_hooks claude "$CLAUDE_SETTINGS" "$SRC/config/claude-hooks.json"; }

# Codex, when it is set up here: its hooks run `weaver adapt codex`. Codex
# asks you to trust new hooks (in /hooks) before it runs them.
configure_codex() {
	[ -d "$CODEX_DIR" ] || return 0
	configure_hooks codex "$CODEX_DIR/hooks.json" "$SRC/config/codex-hooks.json"
	CODEX_DONE=1
}

check_deps
install_files
remove_agentnav
if [ $configure = 1 ]; then
	[ -n "$zconfs" ] || zconfs="${ZELLIJ_CONFIG_FILE:-${XDG_CONFIG_HOME:-$HOME/.config}/zellij/config.kdl}$NL"
	printf '%s' "$zconfs" | while IFS= read -r z; do
		[ -n "$z" ] && configure_zellij "$z"
	done
	configure_claude
	[ $codex = 1 ] && configure_codex
	[ $dry = 1 ] || cat <<'EOF'

Done. To pick it up:
  - running zellij sessions reload their config file by themselves; restart zellij
    only if Alt y / Alt v do nothing
  - restart any Claude Code sessions that are already running: they read hooks at start
EOF
	[ $dry = 1 ] || [ $CODEX_DONE = 0 ] || cat <<'EOF'
  - Codex: trust weaver's hooks in /hooks (Codex asks before running new hooks), then
    restart running Codex sessions
EOF
fi
