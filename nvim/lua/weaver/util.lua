-- Shared helpers: messages that never raise a hit-enter prompt, paths, git.
local M = {}

local api, fn = vim.api, vim.fn
local uv = vim.uv or vim.loop

M.WARN = vim.log.levels.WARN
M.INFO = vim.log.levels.INFO

M.group = api.nvim_create_augroup('weaver', { clear = true })

-- nvim-notify, noice and snacks replace vim.notify with a non-blocking UI
-- (nvim-notify's is a callable table).
local function notify_replaced()
  if type(vim.notify) ~= 'function' then
    return true
  end
  local info = debug.getinfo(vim.notify, 'S')
  return not (info and info.source:match('vim/_[%w_/]*editor'))
end

-- One message, through the notifier when there is one, else as an echo cut
-- to fit the command line so it can never raise a hit-enter prompt.
function M.msg(text, level)
  text = 'weaver: ' .. text
  if notify_replaced() then
    vim.notify(text, level or M.INFO, { title = 'weaver' })
    return
  end
  text = text:gsub('\n', ' ')
  local width = vim.v.echospace
  if width < 2 then
    return
  end
  if fn.strdisplaywidth(text) > width then
    local n = fn.strchars(text)
    while n > 0 and fn.strdisplaywidth(fn.strcharpart(text, 0, n) .. '…') > width do
      n = n - 1
    end
    text = fn.strcharpart(text, 0, n) .. '…'
  end
  api.nvim_echo({ { text, level == M.WARN and 'WarningMsg' or 'Normal' } }, true, {})
end

-- Real path, so /tmp and /private/tmp (or a symlinked checkout) compare equal.
function M.real(path)
  if not path or path == '' then
    return nil
  end
  path = vim.fs.normalize(fn.fnamemodify(path, ':p'))
  if #path > 1 then
    path = path:gsub('/$', '')
  end
  return uv.fs_realpath(path) or path
end

function M.same_path(a, b)
  local ra, rb = M.real(a), M.real(b)
  return ra ~= nil and ra == rb
end

-- Whether PATH is DIR or inside it.
function M.under(path, dir)
  local p, d = M.real(path), M.real(dir)
  if not p or not d then
    return false
  end
  return p == d or p:sub(1, #d + 1) == d .. '/'
end

function M.is_dir(path)
  local st = path and path ~= '' and uv.fs_stat(path)
  return st ~= nil and st ~= false and st.type == 'directory'
end

-- A path for messages: relative to the cwd when it can be, and never long.
function M.short(path)
  local p = fn.fnamemodify(path, ':~:.')
  if #p > 40 then
    p = '…/' .. fn.fnamemodify(path, ':h:t') .. '/' .. fn.fnamemodify(path, ':t')
  end
  return p
end

-- git in ROOT, synchronously (user-triggered actions only), bounded at 3s.
-- Returns stdout, or nil and stderr.
function M.git(root, args, opts)
  opts = opts or {}
  local cmd = { 'git', '-C', root }
  vim.list_extend(cmd, args)
  local ok, res = pcall(function()
    return vim.system(cmd, { text = true, stdin = opts.stdin, env = opts.env }):wait(opts.timeout or 3000)
  end)
  if not ok or not res or res.code ~= 0 then
    return nil, ok and res and res.stderr or tostring(res)
  end
  return res.stdout
end

-- git in ROOT, asynchronously; CB(stdout or nil, stderr) runs scheduled.
function M.git_async(root, args, cb, opts)
  opts = opts or {}
  local cmd = { 'git', '-C', root }
  vim.list_extend(cmd, args)
  local ok, err = pcall(vim.system, cmd, { text = true, stdin = opts.stdin, env = opts.env }, function(res)
    vim.schedule(function()
      if res.code == 0 then
        cb(res.stdout)
      else
        cb(nil, res.stderr)
      end
    end)
  end)
  if not ok then
    vim.schedule(function() cb(nil, tostring(err)) end)
  end
end

function M.first_line(out)
  local line = out and out:match('^[^\n]*')
  return line ~= '' and line or nil
end

-- Paths from `git … -z`, which (unlike plain output) never C-quotes them.
function M.nul_list(out)
  local list = {}
  for p in (out or ''):gmatch('[^%z]+') do
    list[#list + 1] = p
  end
  return list
end

-- The repository top level of DIR, or nil.
function M.toplevel(dir)
  if not M.is_dir(dir) then
    return nil
  end
  local out = M.git(dir, { 'rev-parse', '--show-toplevel' })
  local top = M.first_line(out)
  return top and M.real(top) or nil
end

-- The tab-local (or global) working directory of TAB.
function M.tab_cwd(tab)
  return fn.getcwd(-1, api.nvim_tabpage_get_number(tab))
end

-- The repository the current tab works in: its cwd's, else the current file's.
function M.current_root()
  local root = M.toplevel(fn.getcwd())
  if root then
    return root
  end
  local name = api.nvim_buf_get_name(0)
  if name ~= '' and vim.bo.buftype == '' then
    return M.toplevel(fn.fnamemodify(name, ':p:h'))
  end
end

-- A diffview view on TAB, if diffview is loaded and TAB holds one.
function M.diffview_view(tab)
  if not package.loaded['diffview.lib'] then
    return nil
  end
  local ok, view = pcall(require('diffview.lib').tabpage_to_view, tab or api.nvim_get_current_tabpage())
  return ok and view or nil
end

-- Windows the agent's files may be shown in: tiled, a normal file buffer,
-- not a diff, not pinned with 'winfixbuf'.
function M.plain_window(win)
  if not api.nvim_win_is_valid(win) or api.nvim_win_get_config(win).relative ~= '' then
    return false
  end
  local buf = api.nvim_win_get_buf(win)
  if vim.bo[buf].buftype ~= '' or vim.wo[win].diff then
    return false
  end
  local ok, fixed = pcall(function() return vim.wo[win].winfixbuf end)
  return not (ok and fixed)
end

-- Start screens a plugin draws in place of an empty buffer (snacks.nvim,
-- dashboard-nvim, alpha, mini.starter): nothing to lose, so following may
-- replace them like an empty file window.
local START_SCREENS = { snacks_dashboard = true, dashboard = true, alpha = true, starter = true, ministarter = true }

function M.start_screen(win)
  if not api.nvim_win_is_valid(win) or api.nvim_win_get_config(win).relative ~= '' then
    return false
  end
  local buf = api.nvim_win_get_buf(win)
  return vim.bo[buf].buftype == 'nofile' and START_SCREENS[vim.bo[buf].filetype] == true and not vim.bo[buf].modified
end

-- A window following may show a file in: a normal file window, or a start
-- screen.
function M.replaceable(win)
  return M.plain_window(win) or M.start_screen(win)
end

-- A window in TAB to show PATH in: one already showing it, else the tab's
-- current window, else any normal file window (or start screen) without
-- unsaved changes.
-- Returns the window, or nil and 'unsaved' (only windows with unsaved
-- changes) or 'nowindow' (no normal file window at all).
function M.window_for(tab, path)
  local wins = api.nvim_tabpage_list_wins(tab)
  for _, win in ipairs(wins) do
    if M.plain_window(win) and M.same_path(api.nvim_buf_get_name(api.nvim_win_get_buf(win)), path) then
      return win
    end
  end
  local saw_plain = false
  for _, win in ipairs(vim.list_extend({ api.nvim_tabpage_get_win(tab) }, wins)) do
    if M.replaceable(win) then
      saw_plain = true
      if not vim.bo[api.nvim_win_get_buf(win)].modified then
        return win
      end
    end
  end
  return nil, saw_plain and 'unsaved' or 'nowindow'
end

function M.state_dir()
  local base = vim.env.XDG_STATE_HOME
  if not base or base == '' then
    base = vim.env.HOME .. '/.local/state'
  end
  return base .. '/weaver'
end

function M.read_json(path)
  local f = path and io.open(path, 'r')
  if not f then
    return nil
  end
  local text = f:read('*a')
  f:close()
  local ok, value = pcall(vim.json.decode, text, { luanil = { object = true, array = true } })
  return ok and type(value) == 'table' and value or nil
end

-- The weaver binary, for `weaver snapshot` and friends.
function M.weaver_bin()
  local bin = vim.env.WEAVER_BIN
  if bin and bin ~= '' and fn.executable(bin) == 1 then
    return bin
  end
  local here = debug.getinfo(1, 'S').source:sub(2)
  local guess = fn.fnamemodify(here, ':p:h:h:h:h') .. '/weaver'
  if fn.executable(guess) == 1 then
    return guess
  end
  if fn.executable('weaver') == 1 then
    return 'weaver'
  end
end

return M
