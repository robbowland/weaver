-- weaver's keys in Rex. wvr writes
-- this file (weaver.lua, beside the init.lua Rex reads) each time it starts
-- on Rex, and has Rex read it again when it is new to Rex; init.lua loads
-- it with
--
--   require("weaver")
--
-- What runs is wvr's, at its own path:
local wvr = "@WVR@"
-- weaver's record of the Rex sessions it made, one file each:
local sessions = "@SESSIONS@"
-- Where rex is, as the wvr that wrote this (from a terminal) found it.
-- Rex's server runs these keys' commands with an app's environment, with
-- no rex on its PATH and no REX_BIN_DIR: without it, wvr would find no rex
-- (but the Rex app's own), and every key would do nothing.
local rexbin = "@REXBIN@"
-- The settings a key's wvr reads (where weaver keeps its state, its
-- widths, its views) that the wvr writing this had set: Rex's server runs
-- these keys' commands with an app's environment, which has none of a
-- shell's.
local settings = { --[[@SETTINGS@]] }

local terminal = "com.superlogical.terminal"

-- home is $HOME, where Rex's Lua says it ("" where it does not).
local function home()
  return (os and os.getenv and os.getenv("HOME")) or ""
end

-- The directory of the pane a key was pressed in, for a new thread's form
-- to start from: the one it last reported (a file:// URL's path, its %XX
-- escapes undone, a space being %20; else a plain path as it is), else its
-- program's (nvim reports none), else home: never Rex's own.
local function pwd(ctx)
  if ctx.block_id then
    local r = rex.block.call(terminal, "pwd", { block_id = ctx.block_id })
    local url = r and r.pwd or ""
    local path = url:match("^file://[^/]*(/.*)$")
    if path then
      return (path:gsub("%%(%x%x)", function(h) return string.char(tonumber(h, 16)) end))
    end
    if url ~= "" and not url:find("://", 1, true) then return url end
    local p = rex.block.call(terminal, "process", { block_id = ctx.block_id })
    local proc = p and (p.foreground or p.child)
    if proc and proc.cwd and proc.cwd ~= "" then return proc.cwd end
  end
  return home()
end

-- run runs wvr with args for the session the key was pressed in, in no
-- pane of its own, so weaver acts on the focused one.
-- From cwd, else home (as well when cwd has gone: a removed worktree's).
-- What it says when it fails goes to Rex's log (rex server logs). False
-- when it could not run at all (no wvr where it was): the key is the
-- terminal's then, not swallowed.
local function run(ctx, args, cwd)
  local argv = { wvr }
  for _, a in ipairs(args) do argv[#argv + 1] = a end
  local env = {}
  for k, v in pairs(settings) do env[k] = v end
  env.REX_SESSION, env.REX_BLOCK = ctx.session_id, false
  env.REX_BIN_DIR = rexbin ~= "" and rexbin or false
  local opts = {
    cwd = (cwd ~= nil and cwd ~= "") and cwd or nil,
    env = env,
    stdout = false,
    stderr = function(s) rex.log("warn", "weaver: " .. s) end,
    detach = true,
  }
  local h, err = nil, nil
  if opts.cwd then h, err = rex.exec.start(argv, opts) end
  if not h then
    opts.cwd = home() ~= "" and home() or nil
    h, err = rex.exec.start(argv, opts)
  end
  if not h then
    rex.log("warn", "weaver: could not run " .. wvr .. ": " .. tostring(err))
    return false
  end
  return true
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
-- terminal's.
local function weavers(ctx)
  local id = (ctx.session_id or ""):gsub(":", "_")
  local f = id ~= "" and io.open(sessions .. "/" .. id)
  if f then
    f:close()
    return true
  end
  return false
end

-- inPopup: is the key pressed in a floating layer, one of weaver's popups
-- (a new thread's form, a card, Alt y's board)? A popup takes every key:
-- the popup's program reads it.
local function inPopup(ctx)
  if not ctx.block_id then return false end
  local v = rex.session.view{}
  for _, w in ipairs(v and v.windows or {}) do
    for _, l in ipairs(w.layers or {}) do
      if l.kind ~= "tiled" then
        for _, b in ipairs(l.blocks or {}) do
          if b.block_id == ctx.block_id then return true end
        end
      end
    end
  end
  return false
end

-- key binds k to fn in weaver's sessions, outside its popups; fn returns
-- false to let the key through there too.
local function key(k, fn)
  rex.bind(k, function(ctx)
    if not weavers(ctx) or inPopup(ctx) then return false end
    return fn(ctx) ~= false
  end)
end

key("alt+y", function(ctx) return run(ctx, { "go", "home" }, pwd(ctx)) end)
key("alt+shift+n", function(ctx)
  if inView(ctx) then return false end
  return run(ctx, { "_popup", "new" }, pwd(ctx))
end)
key("alt+v", function(ctx) return run(ctx, { "review" }) end)
key("alt+shift+v", function(ctx) return run(ctx, { "review", "--full" }) end)
key("alt+1", function(ctx) return run(ctx, { "next" }) end)
-- Quit the session, every agent and review in it, after asking.
key("ctrl+q", function(ctx) return run(ctx, { "_popup", "quit" }) end)
-- The next and previous window that shows no view: through wvr,
-- since the app shows only a window it is asked to (the server's active
-- window is not what it shows).
key("alt+j", function(ctx) return run(ctx, { "go", "window", "next" }) end)
key("alt+k", function(ctx) return run(ctx, { "go", "window", "prev" }) end)
key("alt+h", function() rex.session.focus_direction{ direction = "left" } end)
key("alt+l", function() rex.session.focus_direction{ direction = "right" } end)
