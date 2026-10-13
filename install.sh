#!/usr/bin/env bash
# Install weaver; with --configure, also wire it into zellij and Claude Code.
#
#   ./install.sh                  copy to ~/.local/share/weaver, link ~/.local/bin/weaver
#   ./install.sh --configure      also add the zellij keybindings and Claude Code hooks
#     --zellij-config FILE        zellij config to edit; repeatable (default: $ZELLIJ_CONFIG_FILE,
#                                 else ~/.config/zellij/config.kdl). FILE.in templates are
#                                 edited but not validated.
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
TS=$(date +%Y%m%d-%H%M%S)

say() { printf '%s\n' "$*"; }
warn() { printf 'warning: %s\n' "$*" >&2; }
die() {
	printf 'install.sh: %s\n' "$*" >&2
	exit 1
}

configure=0 dry=0 zconfs=""
while [ $# -gt 0 ]; do
	case $1 in
	--configure) configure=1 ;;
	--dry-run) dry=1 ;;
	--zellij-config)
		[ $# -ge 2 ] || die "--zellij-config needs a file"
		zconfs="$zconfs$2$NL"
		shift
		;;
	-h | --help)
		sed -n '2,12p' "$0"
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
		mkdir -p "$DEST/nvim" "$DEST/config"
		cp "$SRC/weaver" "$SRC/install.sh" "$DEST/"
		cp "$SRC/nvim/weaver.lua" "$DEST/nvim/"
		cp "$SRC/config/zellij-keybinds.kdl" "$SRC/config/claude-hooks.json" "$DEST/config/"
		chmod 755 "$DEST/weaver" "$DEST/install.sh"
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

# Drop the entries of agentnav (weaver's old name), then append each weaver hook
# entry unless the event already has one with the same matcher and commands;
# other entries are never touched.
JQ_HOOKS='
def sig: (.matcher // "") + "\u0001" + ([.hooks[]?.command? // empty] | join("\u0001"));
def agentnav: [.hooks[]?.command? // empty] | length > 0 and all(startswith("agentnav "));
(if .hooks then .hooks |= with_entries(
  if any(.value[]; agentnav) then (.value |= map(select(agentnav | not))) | select(.value | length > 0)
  else . end) else . end) |
reduce ($add[0].hooks | to_entries[]) as $e (.;
  reduce $e.value[] as $entry (.;
    if any((.hooks[$e.key] // [])[]; sig == ($entry | sig)) then .
    else .hooks[$e.key] = ((.hooks[$e.key] // []) + [$entry]) end))'

configure_claude() {
	local f=$CLAUDE_SETTINGS tmp
	tmp=$(mktemp "${TMPDIR:-/tmp}/weaver.XXXXXX")
	if [ -f "$f" ]; then cat "$f"; else echo '{}'; fi |
		jq --slurpfile add "$SRC/config/claude-hooks.json" "$JQ_HOOKS" >"$tmp" ||
		die "cannot parse $f"
	if [ -f "$f" ] && jq -e --slurpfile new "$tmp" '. == $new[0]' "$f" >/dev/null; then
		say "claude: $f already runs weaver on every event"
	elif show_or_apply claude "$f" "$tmp"; then
		jq empty "$f" || die "wrote invalid JSON to $f; the backup is $f.bak.$TS"
	fi
	rm -f "$tmp"
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
	[ $dry = 1 ] || cat <<'EOF'

Done. To pick it up:
  - running zellij sessions reload their config file by themselves; restart zellij
    only if Alt y / Alt v do nothing
  - restart any Claude Code sessions that are already running: they read hooks at start
EOF
fi
