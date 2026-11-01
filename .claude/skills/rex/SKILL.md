---
name: rex
description: Use when changing, debugging or QA-ing weaver on Rex (the Superlogical terminal multiplexer): internal/rex, config/rex.lua (weaver's keys), anything that opens, shows, splits or focuses windows on Rex, test/rex.sh, or when a Rex behaviour "does nothing". Holds what Rex actually does (verified against a real app), the traps it sets, how to test against it, and references.
---

# weaver on Rex

Rex (Superlogical, beta; verified on Rex Beta 0.1.0 build 1696, API
revision 8) is server > session > window ("tab") > block (pane). weaver
drives it through the `rex` CLI: `rex api call METHOD JSON` (~20 ms each),
`rex do` (a Lua snapshot, ~25 ms; app actions), and Lua in the server's
config (weaver.lua, written from config/rex.lua). Read docs/design.md's
Multiplexers section with this.

## What Rex does that bit weaver (all verified; keep them true)

1. **The app shows its own tab, not the server's active window.**
   `session.focus_window` changes what the server calls active; the app
   neither follows it nor keeps it (it writes its own tab back when it next
   shows the session). Anything you should *see* goes through the app:
   `rex do session.select session_id=S window_id=W` (needs Remote Control
   on). `rex.Session.showInApp` does this; `Show`, `Focus`, and foreground
   `OpenWindow`/`BreakOut` use it, falling back to the server call when the
   app refuses. Pane focus *within* the shown window via
   `session.focus_block` does stick.
2. **The app evens out a window it shows when its panes change** (a split
   made, a block swapped in), within ~1.5 s, whatever ratio you asked for.
   A split made in a window it is *not* showing keeps its ratio, also once
   shown. So: build windows whole behind you, then show them (threadTab);
   after changing a shown window's panes, put widths back late
   (`settleWidths` → `wvr _width AGENT`, detached, twice). `resize_pane`
   after the rebalance sticks. `Workspace.balancesSplitsOnCreation=false`
   did not stop it.
3. **The server reads its config only at start or `rex config reload`.**
   `pcall(require, "weaver")` in init.lua fails silently if weaver.lua was
   not there yet. Check what is loaded with `client.keymap` (bindings whose
   `source` starts `weaver.lua:`); `rex.KeysLoaded` / `rex.ReloadConfig`.
   `rex config check` loads the file itself and says nothing about the
   server.
4. **Key-bound Lua runs in the server with an app's environment**: PATH
   `/usr/bin:/bin:/usr/sbin:/sbin:/usr/local/bin`, no `REX_BIN_DIR`, no
   Homebrew. A `wvr` it starts finds no `rex` and does nothing, silently.
   weaver.lua carries the writer's rex dir (as REX_BIN_DIR; not its PATH,
   which would change with every shell that starts wvr and reload Rex's
   config each time); `rex.Bin` falls back to
   `~/Library/Application Support/com.superlogical.rex/RexServer/Versions/Current/Rex Server.app/Contents/MacOS/rex`;
   key commands' stderr goes to `rex.log` (see `rex server logs`). Their
   `cwd` defaults to Rex's own directory: pass the pane's (its reported
   pwd, else its program's from the terminal's `process` method).
5. **Rex's bindings take Alt keys whatever Option sends**: with
   `Workspace.terminalOptionKeyBehavior` `automatic`, `composesText` or
   `actsAsAlt` alike, a pressed ⌥j fired weaver's `alt+j`. The setting
   (`automatic | composesText | actsAsAlt | leftActsAsAlt | rightActsAsAlt`)
   matters for what reaches programs (weaver's tmux in a Rex terminal, nvim,
   fzf): composed, ⌥ is a consumed modifier (`consumed_mods`).
6. **A floating layer is headed by its block's label**, over a one-row
   layer's only row: weaver's messages (Say) are labelled with their text.
   A popover's heading shows the terminal's title instead when the shell
   sets one.
7. **Every remote action shows a toast** ("rex: Go to Session"); there is no
   setting for it (`RemoteActionNotices`).
8. **An unviewed block is 80×24** until an app shows it; a client's size
   applies only when it owns the block or none does. `session.create`'s
   first block is owned by its (short-lived) caller.
9. **A server restart at the same endpoint restores sessions** with the same
   session/window/block ids but a fresh shell in every block (husks).
   weaver records the server pid per session (`sessions/<id>` line 3) and
   remakes a restored one (`Restored`, `Ensure`). Saved sessions are kept
   per endpoint: a server on another socket restores nothing.
10. `rex.client.queue` works only inside `rex.action`s, not in functions
    bound with `rex.bind`; `client.perform` from a key handler waits on the
    app that is waiting on it. Prefer running `wvr`.
11. `session.swap_blocks` refuses a detached block (`session.new_block`):
    "not found in any window".
12. The sidebar's colour is not configurable (a raised surface over the
    theme's background, rgb(19,20,19) on black); `interfaceStyle`
    (`system | useTheme | blendTheme`) does not change it.

## Testing against Rex

- Unit/integration: `rextest.Server(t, rex.Bin())` (a throwaway server),
  `rextest.ServerAt(t, bin, dir)` to restart one at the same endpoint (short
  /tmp dir: socket paths are ~100 bytes). `rextest.NoRex` for none.
- End to end: `test/rex.sh`. Its server runs with an app-like environment
  (no rex on PATH, no REX_BIN_DIR) and an init.lua that requires weaver
  from before wvr writes it, so the key path is the real one. Press a key as
  the app does with `client.key`:
  `{"session_id":S,"key":{"action":"press","key":"key_j","mods":"alt","text":"","unshifted_codepoint":106}}`
  → `{"handled":true}` when a Lua binding took it.
- There is no app in a throwaway server: app actions fail and the server
  fallback runs. Behaviour that depends on the app (what it shows, its
  rebalancing, toasts, headings) needs the real app.

## QA on the real app (when the user allows it)

- Use a separate weaver session (`WEAVER_SESSION=wqa`), a build of the
  working tree (`go build -o /tmp/wqa/bin/wvr ./cmd/wvr`), a fake agent in
  `WEAVER_CLAUDE`, and back up `~/.config/rex/weaver.lua` first (a QA start
  rewrites it); restore it and `rex config reload` after.
- See the app: `screencapture -x -o -l<windowid>` of the Rex window (window
  id via CGWindowListCopyWindowInfo, owner "Rex Beta"; needs Screen
  Recording). Without screenshots, a block's `size` method shows which
  blocks the app has drawn (`desired` from the app's client id).
- Press keys: `osascript` System Events `key code N using option down`
  (needs Accessibility). **Before every key, check the app is on the QA
  session**: `Workspace.lastFocusedSession` in com.superlogical.rex's prefs
  (`defaults export … | plutil -extract 'Workspace\.lastFocusedSession' raw
  -o - - | base64 -D`) — else keys land in whatever session the user has
  open (possibly the agent's own).
- A fake agent lives only in the QA shell's `WEAVER_CLAUDE`: a thread
  resumed from the board (a held or lost one) takes the board's agent
  command ("now's", by design), which in a Rex block is the real `claude`.
  Resume and restart fake threads from the QA shell (`wvr resume`, `wvr
  restart`), not the board; if a real agent starts, end it by the pid the
  block's `process` method gives, never by name.
- Clean up QA sessions (`session.destroy`) and put the app back where it was
  (`rex do session.select session_id=…`).

## Debugging

- `rex server logs -n 2000` (Lua `rex.log` output, Lua errors);
  `rex config check`; `rex api call client.keymap '{}'`;
  `rex api describe METHOD` for any method's schema; `rex api list`.
- `rex do client.…`/`session.select` fail with "Rex.app is not accepting
  actions" when Remote Control is off.

## References

- Docs index: https://www.superlogical.com/rex/docs/llms.txt (pages as .md:
  customize/config, keyboard-shortcuts, actions; automate/cli, lua-scripts,
  lua-events, server-api; reference/lua, reference/events;
  build/program-status).
- Lua availability table (what works at load / later / in scripts):
  https://www.superlogical.com/rex/docs/reference/lua.md
- Prior art: firstmate's Rex backend
  https://raw.githubusercontent.com/kunchenguid/firstmate/master/docs/rex-backend.md
  (CLI lookup order, background tabs, 80×24 unviewed blocks, a `rex do`
  event reader for program status).
