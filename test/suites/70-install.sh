#!/bin/bash
# install.sh in a sandbox home: a fresh install (one binary, its files
# unpacked, its Neovim plugin where Neovim configs load it), the old install's
# copies removed, Claude Code hooks upgraded from older weaver (and agentnav)
# entries without leaving stale ones or touching anyone else's, Codex's, and
# idempotent reruns.
. "$(dirname "$0")/../lib.sh"

ROOT=$(cd "$(dirname "$0")/../.." && pwd -P)
H=$AT/install-home
rm -rf "$H"
mkdir -p "$H/.claude"
# What the install before the binary left: its script, itself and config/.
mkdir -p "$H/.local/share/weaver/config"
printf '#!/usr/bin/env bash\n' >"$H/.local/share/weaver/weaver"
printf '#!/usr/bin/env bash\n' >"$H/.local/share/weaver/install.sh"
echo '{}' >"$H/.local/share/weaver/config/claude-hooks.json"
inst() {
	env -u ZELLIJ_CONFIG_FILE HOME=$H XDG_DATA_HOME=$H/.local/share XDG_CONFIG_HOME=$H/.config CLAUDE_CONFIG_DIR=$H/.claude CODEX_HOME=$H/.codex \
		PATH="$H/.local/bin:$PATH" "$ROOT/install.sh" "$@" 2>&1
}

out=$(inst)
ok "install builds weaver, one binary, into ~/.local/bin" \
	"$([ -f "$H/.local/bin/weaver" ] && [ ! -L "$H/.local/bin/weaver" ] && go version -m "$H/.local/bin/weaver" | grep -c 'path	github.com/robbowland/weaver/cmd/weaver')" "1"
ok "...unpacks its files" "$(ls "$H/.local/share/weaver/runtime"/*/config/tmux.conf 2>/dev/null | wc -l | tr -d ' ')" "1"
ok "...keeps its Neovim plugin where Neovim configs load it" \
	"$(cd "$ROOT/nvim" && find . -type f | sort | tr '\n' ' ')" \
	"$(cd "$H/.local/share/weaver/nvim" && find . -type f ! -name .weaver-runtime | sort | tr '\n' ' ')"
ok "...and removes the old install's copies" "$(ls "$H/.local/share/weaver" | tr '\n' ' ')" "nvim runtime "

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
out=$(inst --configure)
S=$H/.claude/settings.json
ok "no stale weaver or agentnav hook is left" \
	"$(jq '[.. | objects | select(has("command")) | .command | select(test("^(weaver|agentnav) "))] | length' "$S")" "0"
ok "each event (each matcher of it) has exactly one weaver hook" \
	"$(jq '[.hooks[][] | [.hooks[] | select(.command | endswith("/weaver"))] | length | select(. > 0)] | unique' -c "$S")|$(jq '[.hooks | to_entries[] | select(any(.value[].hooks[]; .command | endswith("/weaver")))] | length' "$S")|$(jq -c '[.hooks.PreToolUse[] | select(any(.hooks[]; .command | endswith("/weaver"))) | .matcher]' "$S")" \
	"[1]|$(jq '.hooks | length' "$ROOT/config/claude-hooks.json")|[\"AskUserQuestion\",\"Bash\"]"
ok "...run by absolute path, without a shell" \
	"$(jq -r '[.hooks[][].hooks[] | select(.command | endswith("/weaver")) | .command] | unique | .[]' "$S")|$(jq -r '.hooks.Stop[] | .hooks[] | select(.command | endswith("/weaver")) | .args | join(" ")' "$S")" \
	"$H/.local/bin/weaver|hook"
ok "...async where nobody waits" "$(jq -r '.hooks.PostToolUse[] | .hooks[] | select(.command | endswith("/weaver")) | "\(.args | join(" ")) \(.async)"' "$S")" "hook --async true"
ok "someone else's hooks survive, even beside an old weaver one" \
	"$(jq -r '[.. | objects | select(has("command")) | .command | select(test("say|lint"))] | join(",")' "$S")|$(jq -r .model "$S")" \
	"say done,my-linter|opus"
ok "a backup of the settings is kept" "$(ls "$H/.claude" | grep -c '^settings.json.bak\.')" "1"

ok "no ~/.codex: nothing created for Codex" "$([ -e "$H/.codex" ] && echo made || echo none)" "none"

# Codex, set up with someone else's hooks.
mkdir -p "$H/.codex"
cat >"$H/.codex/hooks.json" <<'EOF'
{ "hooks": { "SessionStart": [{ "matcher": "startup|resume", "hooks": [{ "type": "command", "command": "headroom init", "timeout": 15 }] }] } }
EOF
out=$(inst --configure)
C=$H/.codex/hooks.json
ok "codex: weaver adapts every Codex hook event" \
	"$(jq -r '[.hooks | to_entries[] | select(any(.value[].hooks[]; .command | test("/weaver adapt codex"))) | .key] | sort | join(",")' "$C")" \
	"Interrupt,PermissionRequest,PostToolUse,SessionEnd,SessionStart,Stop,SubagentStart,SubagentStop,UserPromptSubmit"
ok "...by absolute path" "$(jq -r '.hooks.Stop[0].hooks[0].command' "$C")" "$H/.local/bin/weaver adapt codex --bell"
ok "...leaving its other hooks" "$(jq -r '.hooks.SessionStart[] | .hooks[].command' "$C" | grep -c headroom)" "1"
ok "...and says to trust them" "$(printf '%s\n' "$out" | grep -c 'trust weaver.s hooks in /hooks')" "1"

sleep 1.1 # a new backup would get a new name
before=$(cat "$S")
out=$(inst --configure)
ok "rerun: settings unchanged" "$(cat "$S")" "$before"
ok "...no new backup" "$(ls "$H/.claude" | grep -c '^settings.json.bak\.')" "1"
ok "...and says so" "$(printf '%s\n' "$out" | grep -c 'already runs weaver')" "2"
before=$(cat "$C")
rm -f "$H/.codex/hooks.json"
out=$(inst --configure --no-codex)
ok "--no-codex leaves Codex alone" "$([ -e "$C" ] && echo touched || echo alone)" "alone"
mkdir -p "$H/.config/zellij"
printf 'keybinds {\n    // weaver: its keys\n    bind "Alt y" { Run "weaver" "ls"; }\n}\n' >"$H/.config/zellij/config.kdl"
out=$(inst --configure --no-codex 2>&1)
ok "a zellij config with weaver's old keybindings: said, left alone" \
	"$(printf '%s\n' "$out" | grep -c "still has weaver's keybindings")|$(grep -c 'Run "weaver"' "$H/.config/zellij/config.kdl")" "1|1"
done_suite
