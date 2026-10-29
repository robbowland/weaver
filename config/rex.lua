-- weaver's keys in Rex, as config/tmux.conf binds them in tmux. wvr writes
-- this file (~/.config/rex/weaver.lua) each time it starts on Rex; load it
-- from ~/.config/rex/init.lua with
--
--   require("weaver")
--
-- then `rex config reload`. What runs is wvr's, at its own path:
local wvr = "@WVR@"

local terminal = "com.superlogical.terminal"

-- The directory the pane a key was pressed in last reported (a file://
-- URL), for a new thread's form to start from.
local function pwd(ctx)
  local r = ctx.block_id and rex.block.call(terminal, "pwd", { block_id = ctx.block_id })
  local url = r and r.pwd or ""
  return (url:gsub("^file://[^/]*", ""))
end

-- run runs wvr with args for the session the key was pressed in, as tmux's
-- run-shell does: in no pane of its own, so weaver acts on the focused one.
local function run(ctx, args, cwd)
  local argv = { wvr }
  for _, a in ipairs(args) do argv[#argv + 1] = a end
  rex.exec.start(argv, {
    cwd = cwd,
    env = { REX_SESSION = ctx.session_id, REX_BLOCK = false, WEAVER_MUX = "rex" },
    stdout = false, stderr = false, detach = true,
  })
end

-- inView: is the key pressed in one of weaver's views (GITHUB, LINEAR),
-- whose lists take Alt N themselves?
local function inView(ctx)
  local v = rex.session.view{}
  for _, w in ipairs(v and v.windows or {}) do
    if w.window_id == v.active_window_id then
      return w.label == "GITHUB" or w.label == "LINEAR"
    end
  end
  return false
end

rex.bind("alt+y", function(ctx) run(ctx, { "go", "home" }, pwd(ctx)); return true end)
rex.bind("alt+shift+n", function(ctx)
  if inView(ctx) then return false end
  run(ctx, { "_popup", "new" }, pwd(ctx))
  return true
end)
rex.bind("alt+v", function(ctx) run(ctx, { "edit" }); return true end)
rex.bind("alt+shift+v", function(ctx) run(ctx, { "edit", "--full" }); return true end)
rex.bind("alt+1", function(ctx) run(ctx, { "next" }); return true end)
rex.bind("alt+j", "client.tab.next")
rex.bind("alt+k", "client.tab.previous")
rex.bind("alt+h", "pane.focus.left")
rex.bind("alt+l", "pane.focus.right")
