-- Helpers for the headless specs in test/nvim/*_spec.lua (run by
-- test/nvim/run.sh, each in its own nvim).
local T = {}

local api, fn = vim.api, vim.fn

T.pass, T.fail = 0, 0
T.root = fn.fnamemodify(debug.getinfo(1, 'S').source:sub(2), ':p:h:h:h')

function T.ok(name, got, want)
  if vim.deep_equal(got, want) then
    T.pass = T.pass + 1
    io.stdout:write('PASS ' .. name .. '\n')
  else
    T.fail = T.fail + 1
    io.stdout:write(('FAIL %s: got [%s] want [%s]\n'):format(name, vim.inspect(got), vim.inspect(want)))
  end
end

function T.skip(name, why)
  io.stdout:write(('SKIP %s: %s\n'):format(name, why))
end

function T.done()
  io.stdout:write(('-- %d passed, %d failed\n'):format(T.pass, T.fail))
  io.stdout:flush()
  vim.cmd((T.fail > 0 and 'cquit ' .. T.fail) or 'qall!')
end

-- Run the spec body, reporting an error as a failure rather than hanging.
function T.run(body)
  local ok, err = xpcall(body, debug.traceback)
  if not ok then
    T.fail = T.fail + 1
    io.stdout:write('FAIL (error) ' .. tostring(err) .. '\n')
  end
  T.done()
end

function T.wait(ms, cond)
  return vim.wait(ms, cond, 10) and true or false
end

-- Let scheduled work run.
function T.settle(ms)
  vim.wait(ms or 50, function() return false end, 10)
end

function T.sh(cmd, opts)
  local res = vim.system(cmd, vim.tbl_extend('force', { text = true }, opts or {})):wait()
  assert(res.code == 0, table.concat(cmd, ' ') .. ': ' .. (res.stderr or ''))
  return res.stdout
end

function T.git(root, args, opts)
  local cmd = { 'git', '-C', root }
  vim.list_extend(cmd, args)
  return T.sh(cmd, opts)
end

function T.write(path, lines)
  fn.mkdir(fn.fnamemodify(path, ':h'), 'p')
  fn.writefile(type(lines) == 'table' and lines or { lines }, path)
end

-- A scratch repo with a main checkout and two worktrees; real paths.
function T.repo()
  local base = fn.resolve(fn.tempname())
  fn.mkdir(base, 'p')
  base = vim.uv.fs_realpath(base)
  local r = base .. '/repo'
  fn.mkdir(r, 'p')
  T.git(r, { 'init', '-q', '-b', 'main' })
  T.git(r, { 'config', 'user.email', 'test@example.com' })
  T.git(r, { 'config', 'user.name', 'test' })
  T.write(r .. '/src/app.txt', { 'line one', 'line two', 'line three' })
  T.write(r .. '/README.md', { 'readme' })
  T.git(r, { 'add', '.' })
  T.git(r, { 'commit', '-qm', 'init' })
  T.git(r, { 'worktree', 'add', '-q', '-b', 'feat-a', base .. '/wt-a' })
  T.git(r, { 'worktree', 'add', '-q', '-b', 'feat-b', base .. '/wt-b' })
  return { base = base, main = r, a = base .. '/wt-a', b = base .. '/wt-b' }
end

-- A snapshot as the engine takes it (docs/design.md, Snapshots): a private
-- index seeded from the real one, add --all, write-tree, commit-tree onto the
-- tip, update-ref. Returns the commit, or the tip when nothing changed.
function T.snapshot(root, sid, turn, point, prompt, ref)
  ref = ref or ('refs/worktree/weaver/sessions/' .. sid)
  local gitdir = vim.trim(T.git(root, { 'rev-parse', '--path-format=absolute', '--git-path', 'weaver' }))
  fn.mkdir(gitdir, 'p')
  local idx = gitdir .. '/index-test-' .. sid
  local real = vim.trim(T.git(root, { 'rev-parse', '--path-format=absolute', '--git-path', 'index' }))
  if fn.filereadable(idx) == 0 and fn.filereadable(real) == 1 then
    vim.uv.fs_copyfile(real, idx)
  end
  local env = { GIT_INDEX_FILE = idx }
  T.git(root, { 'add', '--all' }, { env = env })
  local tree = vim.trim(T.git(root, { 'write-tree' }, { env = env }))
  local tip = vim.system({ 'git', '-C', root, 'rev-parse', '--verify', '--quiet', ref }, { text = true }):wait()
  tip = tip.code == 0 and vim.trim(tip.stdout) or nil
  if tip and vim.trim(T.git(root, { 'rev-parse', tip .. '^{tree}' })) == tree then
    return tip
  end
  local msg = ('weaver: turn %d %s: %s\n\n%s\n\nWeaver-Session: %s\nWeaver-Turn: %d\nWeaver-Point: %s\n'):format(
    turn,
    point,
    prompt:match('^[^\n]*'),
    prompt,
    sid,
    turn,
    point
  )
  local args = { 'commit-tree', tree }
  if tip then
    vim.list_extend(args, { '-p', tip })
  end
  local commit = vim.trim(T.git(root, args, {
    stdin = msg,
    env = {
      GIT_AUTHOR_NAME = 'weaver',
      GIT_AUTHOR_EMAIL = 'weaver@localhost',
      GIT_COMMITTER_NAME = 'weaver',
      GIT_COMMITTER_EMAIL = 'weaver@localhost',
    },
  }))
  T.git(root, { 'update-ref', ref, commit })
  return commit
end

-- Load weaver's Neovim side the way `weaver nvim` does.
function T.load_weaver(env)
  for k, v in pairs(env or {}) do
    vim.env[k] = v
  end
  vim.cmd('luafile ' .. fn.fnameescape(T.root .. '/nvim/weaver.lua'))
end

-- Deliver a hook event the way the engine does, through --remote-expr's
-- function, and let it run.
function T.event(ev)
  ev.seq = ev.seq or math.floor(vim.uv.hrtime() / 1e6)
  _G.WeaverEvent(vim.json.encode(ev))
  T.settle(30)
end

function T.bufname(win)
  return fn.fnamemodify(api.nvim_buf_get_name(api.nvim_win_get_buf(win or 0)), ':t')
end

function T.winbar(win)
  win = win or api.nvim_get_current_win()
  local wb = vim.wo[win].winbar
  if wb == '' then
    return ''
  end
  return api.nvim_eval_statusline(wb, { winid = win, use_winbar = true }).str
end

-- A child nvim (hermetic config, weaver loaded) driven over RPC, so typed
-- keys and modes are real: C.input('ihello') is you typing.
function T.child(env)
  local cmd = { 'nvim', '--embed', '--headless', '-i', 'NONE', '-n', '-u', T.root .. '/test/nvim/init.lua' }
  local chan = fn.jobstart(cmd, { rpc = true, env = env })
  assert(chan > 0, 'cannot start a child nvim')
  local C = { chan = chan }
  function C.request(method, ...)
    return vim.rpcrequest(chan, method, ...)
  end
  -- Lua in the child: C.lua('return vim.fn.getcwd()').
  function C.lua(code, ...)
    return vim.rpcrequest(chan, 'nvim_exec_lua', code, { ... })
  end
  function C.cmd(c)
    return vim.rpcrequest(chan, 'nvim_command', c)
  end
  -- Type KEYS (<Esc> and friends allowed), then let the child process them.
  function C.input(keys)
    vim.rpcrequest(chan, 'nvim_input', keys)
    C.poke()
  end
  function C.poke()
    for _ = 1, 3 do
      vim.rpcrequest(chan, 'nvim_eval', '1')
      T.settle(10)
    end
  end
  function C.mode()
    return vim.rpcrequest(chan, 'nvim_get_mode').mode
  end
  function C.bufname()
    return C.lua("return vim.fn.fnamemodify(vim.api.nvim_buf_get_name(0), ':t')")
  end
  function C.wait(ms, code)
    return T.wait(ms, function() return C.lua(code) end)
  end
  function C.event(ev)
    ev.seq = ev.seq or math.floor(vim.uv.hrtime() / 1e6)
    C.lua('return WeaverEvent(...)', vim.json.encode(ev))
    C.poke()
  end
  -- The winbars on screen in the current tab, joined.
  function C.winbar()
    return C.lua([[
      local out = {}
      for _, w in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
        if vim.wo[w].winbar ~= '' then
          out[#out + 1] = vim.api.nvim_eval_statusline(vim.wo[w].winbar, { winid = w, use_winbar = true }).str
        end
      end
      return table.concat(out, ' | ')
    ]])
  end
  -- Messages so far (:messages), and only those after the last call.
  local seen_messages = 0
  function C.messages()
    local all = vim.split(C.request('nvim_exec2', 'messages', { output = true }).output, '\n', { plain = true })
    local new = vim.list_slice(all, seen_messages + 1)
    seen_messages = #all
    return table.concat(new, '\n')
  end
  function C.wait_for(ms, code, want)
    local got
    T.wait(ms, function()
      got = C.lua(code)
      return vim.deep_equal(got, want)
    end)
    return got
  end
  -- The lines of diffview's file panel in the current tab.
  function C.panel()
    return C.lua([[
      for _, w in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
        local b = vim.api.nvim_win_get_buf(w)
        if vim.bo[b].filetype == 'DiffviewFiles' then
          return table.concat(vim.api.nvim_buf_get_lines(b, 0, -1, false), '\n')
        end
      end
      return ''
    ]])
  end
  function C.close()
    pcall(fn.jobstop, chan)
  end
  C.lua('vim.cmd.luafile(...)', T.root .. '/nvim/weaver.lua')
  return C
end

function T.has_diffview()
  local p = vim.g.weaver_test_plugins
  return p and p.diffview and p.diffview:match('diffview%-plus') ~= nil
end

return T
