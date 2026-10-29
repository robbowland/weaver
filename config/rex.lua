-- weaver's keys in Rex, as config/tmux.conf binds them in tmux. wvr writes
-- this file (~/.config/rex/weaver.lua) each time it starts on Rex; load it
-- from ~/.config/rex/init.lua with
--
--   require("weaver")
--
-- then `rex config reload`. What runs is wvr's, at its own path:
local wvr = "@WVR@"
-- weaver's record of the Rex sessions it made, one file each:
local sessions = "@SESSIONS@"

local terminal = "com.superlogical.terminal"

-- The directory the pane a key was pressed in last reported, for a new
-- thread's form to start from: a file:// URL's path, its %XX escapes
-- undone (a space is %20), else a plain path as it is.
local function pwd(ctx)
  local r = ctx.block_id and rex.block.call(terminal, "pwd", { block_id = ctx.block_id })
  local url = r and r.pwd or ""
  local path = url:match("^file://[^/]*(/.*)$")
  if path then
    return (path:gsub("%%(%x%x)", function(h) return string.char(tonumber(h, 16)) end))
  end
  if url:find("://", 1, true) then return "" end
  return url
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

-- weavers: is the key pressed in a Rex session weaver made? Rex's
-- configuration is one for every session; elsewhere the key is the
-- terminal's (a weaver on tmux in a Rex terminal reads its own Alt keys).
local function weavers(ctx)
  local id = (ctx.session_id or ""):gsub(":", "_")
  local f = id ~= "" and io.open(sessions .. "/" .. id)
  if f then
    f:close()
    return true
  end
  return false
end

-- key binds k to fn in weaver's sessions; fn returns false to let the key
-- through there too.
local function key(k, fn)
  rex.bind(k, function(ctx)
    if not weavers(ctx) then return false end
    return fn(ctx) ~= false
  end)
end

key("alt+y", function(ctx) run(ctx, { "go", "home" }, pwd(ctx)) end)
key("alt+shift+n", function(ctx)
  if inView(ctx) then return false end
  run(ctx, { "_popup", "new" }, pwd(ctx))
end)
key("alt+v", function(ctx) run(ctx, { "edit" }) end)
key("alt+shift+v", function(ctx) run(ctx, { "edit", "--full" }) end)
key("alt+1", function(ctx) run(ctx, { "next" }) end)
key("alt+j", function() rex.session.focus_next_window{} end)
key("alt+k", function() rex.session.focus_previous_window{} end)
key("alt+h", function() rex.session.focus_direction{ direction = "left" } end)
key("alt+l", function() rex.session.focus_direction{ direction = "right" } end)
