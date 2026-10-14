#!/bin/bash
# install.sh in a sandbox home: a fresh install, upgrading Claude Code hooks
# from older weaver (and agentnav) entries without leaving stale ones or
# touching anyone else's, idempotent reruns, and the zellij keybindings.
. "$(dirname "$0")/../lib.sh"

ROOT=$(cd "$(dirname "$0")/../.." && pwd -P)
H=$AT/install-home
rm -rf "$H"
mkdir -p "$H/.claude" "$H/.config/zellij"
inst() {
	HOME=$H XDG_DATA_HOME=$H/.local/share XDG_CONFIG_HOME=$H/.config CLAUDE_CONFIG_DIR=$H/.claude CODEX_HOME=$H/.codex \
		PATH="$H/.local/bin:$PATH" "$ROOT/install.sh" "$@" 2>&1
}

out=$(inst)
ok "install copies weaver and links it" \
	"$([ -x "$H/.local/share/weaver/weaver" ] && echo y)|$(readlink "$H/.local/bin/weaver")" \
	"y|$H/.local/share/weaver/weaver"
ok "...with all of nvim/ and config/" \
	"$(cd "$ROOT/nvim" && find . -type f | sort | tr '\n' ' ')|$(ls "$ROOT/config" | tr '\n' ' ')" \
	"$(cd "$H/.local/share/weaver/nvim" && find . -type f | sort | tr '\n' ' ')|$(ls "$H/.local/share/weaver/config" | tr '\n' ' ')"
ok "...leaving no temporary copy" "$(ls "$H/.local/share" | tr '\n' ' ')" "weaver "

# Claude Code settings from an older weaver, agentnav, and someone else.
cat >"$H/.claude/settings.json" <<'EOF'
{
  "model": "opus",
  "hooks": {
    "Stop": [
      { "hooks": [{ "type": "command", "command": "weaver hook" }, { "type": "command", "command": "say done" }] }
    ],
    "PostToolUse": [
      { "matcher": "Edit|Write|MultiEdit|NotebookEdit|EnterWorktree|ExitWorktree", "hooks": [{ "type": "command", "command": "weaver hook" }] },
      { "matcher": "", "hooks": [{ "type": "command", "command": "weaver hook --async", "async": true }] },
      { "matcher": "Bash", "hooks": [{ "type": "command", "command": "my-linter" }] }
    ],
    "SessionStart": [{ "hooks": [{ "type": "command", "command": "agentnav hook" }] }]
  }
}
EOF
printf 'keybinds {\n    normal {\n        bind "Alt q" { Quit; }\n    }\n}\n' >"$H/.config/zellij/config.kdl"
out=$(inst --configure --zellij-config "$H/.config/zellij/config.kdl")
S=$H/.claude/settings.json
ok "no stale weaver or agentnav hook is left" \
	"$(jq '[.. | objects | select(has("command")) | .command | select(test("^(weaver|agentnav) "))] | length' "$S")" "0"
ok "each event has exactly one weaver hook" \
	"$(jq '[.hooks[] | [.[].hooks[] | select(.command | endswith("/weaver"))] | length] | unique' -c "$S")" "[1]"
ok "...run by absolute path, without a shell" \
	"$(jq -r '[.hooks[][].hooks[] | select(.command | endswith("/weaver")) | .command] | unique | .[]' "$S")|$(jq -r '.hooks.Stop[] | .hooks[] | select(.command | endswith("/weaver")) | .args | join(" ")' "$S")" \
	"$H/.local/bin/weaver|hook"
ok "...async where nobody waits" "$(jq -r '.hooks.PostToolUse[] | .hooks[] | select(.command | endswith("/weaver")) | "\(.args | join(" ")) \(.async)"' "$S")" "hook --async true"
ok "someone else's hooks survive, even beside an old weaver one" \
	"$(jq -r '[.. | objects | select(has("command")) | .command | select(test("say|lint"))] | join(",")' "$S")|$(jq -r .model "$S")" \
	"say done,my-linter|opus"
ok "a backup of the settings is kept" "$(ls "$H/.claude" | grep -c '^settings.json.bak\.')" "1"
ok "zellij keybindings added" "$(grep -c 'Run "weaver" "next"' "$H/.config/zellij/config.kdl")" "1"

ok "no ~/.codex: nothing created for Codex" "$([ -e "$H/.codex" ] && echo made || echo none)" "none"

# Codex, set up with someone else's hooks.
mkdir -p "$H/.codex"
cat >"$H/.codex/hooks.json" <<'EOF'
{ "hooks": { "SessionStart": [{ "matcher": "startup|resume", "hooks": [{ "type": "command", "command": "headroom init", "timeout": 15 }] }] } }
EOF
out=$(inst --configure --zellij-config "$H/.config/zellij/config.kdl")
C=$H/.codex/hooks.json
ok "codex: weaver adapts every Codex hook event" \
	"$(jq -r '[.hooks | to_entries[] | select(any(.value[].hooks[]; .command | test("/weaver adapt codex"))) | .key] | sort | join(",")' "$C")" \
	"Interrupt,PermissionRequest,PostToolUse,SessionEnd,SessionStart,Stop,SubagentStart,SubagentStop,UserPromptSubmit"
ok "...by absolute path" "$(jq -r '.hooks.Stop[0].hooks[0].command' "$C")" "$H/.local/bin/weaver adapt codex --bell"
ok "...leaving its other hooks" "$(jq -r '.hooks.SessionStart[] | .hooks[].command' "$C" | grep -c headroom)" "1"
ok "...and says to trust them" "$(printf '%s\n' "$out" | grep -c 'trust weaver.s hooks in /hooks')" "1"

sleep 1.1 # a new backup would get a new name
before=$(cat "$S")
out=$(inst --configure --zellij-config "$H/.config/zellij/config.kdl")
ok "rerun: settings unchanged" "$(cat "$S")" "$before"
ok "...no new backup" "$(ls "$H/.claude" | grep -c '^settings.json.bak\.')" "1"
ok "...and says so" "$(printf '%s\n' "$out" | grep -c -E 'already runs weaver|already has the current weaver keybindings')" "3"
before=$(cat "$C")
rm -f "$H/.codex/hooks.json"
out=$(inst --configure --no-codex --zellij-config "$H/.config/zellij/config.kdl")
ok "--no-codex leaves Codex alone" "$([ -e "$C" ] && echo touched || echo alone)" "alone"
done_suite
