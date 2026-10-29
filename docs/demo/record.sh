#!/bin/bash
# The README's demo: weaver recorded as an asciicast (weaver.cast), then
# rendered as a GIF (weaver.gif, with agg). It runs in a throwaway HOME with
# a made-up repository, a scripted agent (agent.sh), a fake gh and weaver's
# own tmux server, so nothing of yours (repositories, accounts, agents,
# conversations, config) can show in it.
#   docs/demo/record.sh        (needs agg: brew install agg)
set -eu
HERE=$(cd "$(dirname "$0")" && pwd -P)
ROOT=${HERE%/docs/demo}
COLS=120 ROWS=32
command -v agg >/dev/null || { echo "record.sh: needs agg (brew install agg)" >&2; exit 1; }
REAL_HOME=$HOME
AT=$(cd "$(mktemp -d /tmp/weaver-demo.XXXXXX)" && pwd -P)
mkdir -p "$AT/bin"
(cd "$ROOT" && go build -o "$AT/bin/wvr" ./cmd/wvr)
S=demo
SOCKS=/tmp/weaver-$(id -u)
cleanup() {
	tmux -S "$AT/tmux.sock" kill-server >/dev/null 2>&1 || true
	[ -n "${rec:-}" ] && kill "$rec" 2>/dev/null || true
	pkill -f -- "--listen $SOCKS/tmux-${S}_" 2>/dev/null || true
	rm -f "$SOCKS/tmux-$S".* "$SOCKS/tmux-${S}"_*
	rm -rf "$AT"
}
trap cleanup EXIT
trap 'exit 130' INT TERM HUP

unset TMUX TMUX_PANE ZELLIJ ZELLIJ_SESSION_NAME ZELLIJ_PANE_ID WEAVER_TMUX_SESSION WEAVER_RESTORE LINEAR_API_KEY GH_TOKEN GITHUB_TOKEN
export HOME=$AT/home
export XDG_CONFIG_HOME=$HOME/.config XDG_STATE_HOME=$HOME/.local/state XDG_DATA_HOME=$HOME/.local/share XDG_CACHE_HOME=$HOME/.cache
export WEAVER_SESSION=$S WEAVER_TMUX_SOCKET=$AT/tmux.sock CLAUDE_CONFIG_DIR=$HOME/.claude _ZO_DATA_DIR=$AT/zoxide
export WEAVER_VIEWS=github WEAVER_SELF_SETUP=0 WEAVER_WORKTRUNK=0 WEAVER_BELL=0
export EDITOR=nvim VISUAL=nvim TERM=xterm-256color GIT_CONFIG_NOSYSTEM=1 BASH_SILENCE_DEPRECATION_WARNING=1
# Neovim: the tests' config (none of yours), with the plugins weaver uses.
export VIMINIT="lua dofile('$HERE/init.lua')" WEAVER_TEST_INIT=$ROOT/test/nvim/init.lua WEAVER_TEST_PLUGINS=$REAL_HOME/.local/share/nvim/lazy
export PATH="$AT/bin:$PATH" WEAVER_CLAUDE=$HERE/agent.sh
SD=$XDG_STATE_HOME/weaver
mkdir -p "$AT/bin" "$SD" "$CLAUDE_CONFIG_DIR/sessions" "$HOME/code"
ln -s "$HERE/agent.sh" "$AT/bin/claude"
# No Keychain.
printf '#!/bin/sh\nexit 44\n' >"$AT/bin/security"
printf '[user]\n\tname = Demo\n\temail = demo@example.com\n[init]\n\tdefaultBranch = main\n' >"$HOME/.gitconfig"

# greet: a repository whose origin reads as github.com/demo/greet, with a
# pull request (#12, add-quiet-flag) on it.
R=$HOME/code/greet
mkdir -p "$R"
cat >"$R/greet.py" <<'PY'
import argparse


def main():
    p = argparse.ArgumentParser()
    p.add_argument("name")
    args = p.parse_args()
    print(f"Hello, {args.name}!")


if __name__ == "__main__":
    main()
PY
g() { git -C "$R" "$@"; }
g init -q && g add . && g commit -qm "Greet someone by name"
g checkout -qb add-quiet-flag
cat >"$R/greet.py" <<'PY'
import argparse


def main():
    p = argparse.ArgumentParser()
    p.add_argument("name")
    p.add_argument("--quiet", action="store_true", help="say nothing")
    args = p.parse_args()
    if args.quiet:
        return
    print(f"Hello, {args.name}!")


if __name__ == "__main__":
    main()
PY
g commit -qam "Add a --quiet flag" && g checkout -q main
git clone -q --bare "$R" "$AT/greet.git" && g branch -q -D add-quiet-flag
g remote add origin https://github.com/demo/greet.git && g config url."$AT/greet.git".insteadOf https://github.com/demo/greet.git
g fetch -q origin && g branch -q -u origin/main
printf '%s\n' "$R" >"$SD/repos"

# gh: #12 asks for your review, #9 is yours.
cat >"$AT/bin/gh" <<'GH'
#!/bin/bash
PR='"number":12,"title":"Add a --quiet flag","body":"Lets greet.py say nothing.","url":"https://github.com/demo/greet/pull/12","author":{"login":"alice"},"state":"OPEN","headRefName":"add-quiet-flag","baseRefName":"main","isCrossRepository":false'
case "$1 $2" in
"pr view") echo "{$PR,\"comments\":[]}" ;;
"issue view") echo '{"number":9,"title":"Greet in French","body":"Bonjour.","url":"https://github.com/demo/greet/issues/9","author":{"login":"dan"},"state":"OPEN","comments":[]}' ;;
"api graphql")
	case "$*" in
	*'-user-review-requested:@me'*) echo '{"data":{"s":{"issueCount":0,"nodes":[]}}}' ;;
	*'review-requested:@me'*) echo "{\"data\":{\"s\":{\"issueCount\":1,\"nodes\":[{$PR,\"state\":\"OPEN\",\"isDraft\":false,\"updatedAt\":\"2026-10-20T09:00:00Z\",\"repository\":{\"nameWithOwner\":\"demo/greet\"},\"reviewDecision\":\"REVIEW_REQUIRED\",\"mergeable\":\"MERGEABLE\",\"additions\":4,\"deletions\":1,\"changedFiles\":1,\"commits\":{\"totalCount\":1},\"comments\":{\"totalCount\":0},\"labels\":{\"nodes\":[]},\"assignees\":{\"nodes\":[]},\"reviewRequests\":{\"nodes\":[]},\"last\":{\"nodes\":[]}}]}}}" ;;
	*'is:issue archived:false is:open assignee:@me'*) echo '{"data":{"s":{"issueCount":1,"nodes":[{"number":9,"title":"Greet in French","body":"Bonjour.","url":"https://github.com/demo/greet/issues/9","state":"OPEN","author":{"login":"dan"},"updatedAt":"2026-10-19T16:00:00Z","repository":{"nameWithOwner":"demo/greet"},"comments":{"totalCount":0},"labels":{"nodes":[]},"assignees":{"nodes":[]}}]}}}' ;;
	*'search(type: ISSUE'*) echo '{"data":{"s":{"issueCount":0,"nodes":[]}}}' ;;
	*) echo '{"data":{"repository":{"pullRequest":{"reviewThreads":{"nodes":[]}}}}}' ;;
	esac ;;
"auth token") echo gho_demo ;;
*) exit 1 ;;
esac
GH
chmod +x "$AT/bin/gh" "$AT/bin/security"

# The terminal: a shell in greet, recorded; keys go to it through a FIFO.
mkfifo "$AT/keys"
exec 3<>"$AT/keys"
key() { printf '%s\n' "$1" >&3; }
typ() {
	local i
	for ((i = 0; i < ${#1}; i++)); do
		key "$(printf '%s' "${1:i:1}" | xxd -p)"
		sleep 0.06
	done
}
tx() { tmux -S "$WEAVER_TMUX_SOCKET" "$@"; }
waitfor() {
	local i
	for ((i = 0; i < ${2:-100}; i++)); do
		eval "$1" && return 0
		sleep 0.1
	done
	echo "record.sh: gave up waiting for: $1" >&2
	exit 1
}
form() { ls "$SD" | grep -E '^form\.[^.]+$' | head -n 1; }
agent() { tx list-panes -t "=$S:$1" -F '#{pane_index} #{pane_id}' 2>/dev/null | awk '$1 == 1 { print substr($2, 2) }'; }
state() { jq -r .state "$SD/tmux-${S}_$1.json" 2>/dev/null; }
(cd "$R" && exec python3 -I "$HERE/rec.py" "$AT/keys" "$HERE/weaver.cast" "$COLS" "$ROWS" \
	env PS1='\[\e[90m\]\w\[\e[0m\] $ ' bash --noprofile --norc -i) &
rec=$!
sleep 1.5

# weaver: home, the board.
typ wvr
key 0d
waitfor 'ls $SOCKS/tmux-$S.fzf.*.sock >/dev/null 2>&1'
sleep 2.5
# alt+N: the form. The task, then (alt+n) a name of its own.
key 1b4e
waitfor '[ -n "$(form)" ]'
sleep 1
typ "Add a --loud flag that shouts"
sleep 1
key 1b6e
sleep 1.2
typ "Loud greeting"
sleep 0.6
key 0d
waitfor '[ "$(jq -r .name "$SD/$(form)" 2>/dev/null)" = "Loud greeting" ]'
sleep 2
key 0d # on start still: alt+n leaves the cursor where it was
# Its thread: Neovim follows the agent's edit; the agent asks to run it.
waitfor '[ -n "$(agent 1)" ]'
A1=$(agent 1)
waitfor '[ "$(state "$A1")" = needs-you ]' 200
sleep 2.5
# alt+y: the board over the thread; tab, GITHUB; alt+m, a review of #12,
# named for it.
key 1b79
sleep 3
key 09
sleep 2.5
key 1b6d
waitfor '[ "$(jq -r .mode "$SD/$(form)" 2>/dev/null)" = review ]'
sleep 1.2
typ "Check it does what it says"
sleep 2.5
key 0d
waitfor '[ -n "$(agent 2)" ]'
A2=$(agent 2)
# Its Neovim opens on the review, as its agent reads.
sleep 5
# alt+1: to the agent that needs you; allow it. The review finishes
# meanwhile, unseen: done.
key 1b31
sleep 2
key 31
waitfor '[ "$(state "$A1")" = done ]' 200
waitfor '[ "$(state "$A2")" = done ]' 200
sleep 2.5
# The board: the review done, the flag looked at; their cards.
key 1b79
sleep 3.5
key 1b5b42 # down
sleep 3.5
kill "$rec"
wait "$rec" 2>/dev/null || true
rec=""

# Nothing of yours: no path, name or account from outside the sandbox.
for w in "$REAL_HOME" "$(id -un)" "$(hostname -s)" "$AT"; do
	if grep -qF -- "$w" "$HERE/weaver.cast"; then
		echo "record.sh: the recording shows $w: not kept" >&2
		rm -f "$HERE/weaver.cast"
		exit 1
	fi
done
agg --theme github-dark --text-font-family "JetBrainsMonoNL Nerd Font Mono,JetBrains Mono NL,Menlo" --font-size 14 --idle-time-limit 3 --last-frame-duration 4 "$HERE/weaver.cast" "$HERE/weaver.gif"
echo "record.sh: $HERE/weaver.cast, $HERE/weaver.gif"
