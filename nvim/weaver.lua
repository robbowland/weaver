-- weaver.lua: keeps this nvim in sync with the Claude Code agent it is bound
-- to. Loaded with `nvim -c "luafile …"`, after the user's own config.
--
-- The agent's hook calls AgentFollow(root, file) over the --listen socket. It
-- must never block: work is scheduled, and messages are single echo lines that
-- cannot raise a hit-enter prompt.

local uv = vim.uv or vim.loop
local api = vim.api
local fn = vim.fn
local WARN = vim.log.levels.WARN
local group = api.nvim_create_augroup('weaver', { clear = true })
local following = true

-- nvim-notify, noice and snacks replace vim.notify with a non-blocking UI
-- (nvim-notify's is a callable table).
local function notify_replaced()
  if type(vim.notify) ~= 'function' then
    return true
  end
  local info = debug.getinfo(vim.notify, 'S')
  return not (info and info.source:match('vim/_[%w_/]*editor'))
end

local function msg(text, level)
  text = 'weaver: ' .. text
  if notify_replaced() then
    vim.notify(text, level or vim.log.levels.INFO, { title = 'weaver' })
    return
  end
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
  api.nvim_echo({ { text, level == WARN and 'WarningMsg' or 'Normal' } }, false, {})
end

-- Not while typing a command, in the command-line window, or at a prompt.
local function busy()
  local mode = api.nvim_get_mode()
  return mode.blocking or mode.mode:match('^[cr]') ~= nil or fn.getcmdwintype() ~= ''
end

local function checktime()
  if not busy() then
    vim.cmd('silent! checktime')
  end
end

-- Reloading ---------------------------------------------------------------

vim.o.autoread = true

-- Watch the directories of open files and re-check 100ms after a change.
-- Directory watches use FSEvents on macOS (no file handle per file, and they
-- survive the rename-over-original that editors and formatters do). A slow
-- timer, focus and buffer switches catch anything a watcher misses.
local MAX_WATCHED_DIRS = 64
local DEBOUNCE_MS = 100

local function stop_all()
  for _, w in pairs(_G.__weaver_watchers or {}) do
    w:stop()
    w:close()
  end
  _G.__weaver_watchers = {}
  if _G.__weaver_timer then
    _G.__weaver_timer:stop()
    _G.__weaver_timer:close()
    _G.__weaver_timer = nil
  end
end
stop_all() -- when this file is sourced again

local watchers = _G.__weaver_watchers
local check_queued = false

local function queue_check()
  if check_queued then
    return
  end
  check_queued = true
  vim.defer_fn(function()
    check_queued = false
    checktime()
  end, DEBOUNCE_MS)
end

local function watch(buf)
  if not api.nvim_buf_is_valid(buf) or vim.bo[buf].buftype ~= '' then
    return
  end
  local name = api.nvim_buf_get_name(buf)
  if name == '' then
    return
  end
  local dir = fn.fnamemodify(name, ':p:h')
  if watchers[dir] or fn.isdirectory(dir) == 0 or vim.tbl_count(watchers) >= MAX_WATCHED_DIRS then
    return
  end
  local w = uv.new_fs_event()
  if not w then
    return
  end
  local ok = w:start(dir, {}, function(err)
    if not err then
      vim.schedule(queue_check)
    end
  end)
  if not ok then
    w:close()
    return
  end
  watchers[dir] = w
end

for _, buf in ipairs(api.nvim_list_bufs()) do
  if api.nvim_buf_is_loaded(buf) then
    watch(buf)
  end
end

api.nvim_create_autocmd({ 'BufReadPost', 'BufNewFile', 'BufWritePost', 'BufEnter' }, {
  group = group,
  callback = function(ev) watch(ev.buf) end,
})

api.nvim_create_autocmd('VimLeavePre', { group = group, callback = stop_all })

_G.__weaver_timer = uv.new_timer()
_G.__weaver_timer:start(3000, 3000, vim.schedule_wrap(checktime))

api.nvim_create_autocmd({ 'FocusGained', 'BufEnter', 'CursorHold' }, {
  group = group,
  callback = checktime,
})

-- A path for messages: relative to the cwd when it can be, and never long.
local function short(path)
  local p = fn.fnamemodify(path, ':~:.')
  if #p > 40 then
    p = '…/' .. fn.fnamemodify(path, ':h:t') .. '/' .. fn.fnamemodify(path, ':t')
  end
  return p
end

-- Decide here rather than let nvim ask: its W12 "file changed and buffer
-- modified" dialog blocks this nvim and every remote call into it.
api.nvim_create_autocmd('FileChangedShell', {
  group = group,
  callback = function(ev)
    local name = short(ev.file)
    if vim.v.fcs_reason == 'deleted' then
      -- nvim re-fires this on every check while the file is gone: say it once.
      vim.v.fcs_choice = ''
      if not vim.b[ev.buf].weaver_deleted then
        vim.b[ev.buf].weaver_deleted = true
        vim.schedule(function() msg('deleted on disk: ' .. name, WARN) end)
      end
      return
    end
    vim.b[ev.buf].weaver_deleted = nil
    if vim.bo[ev.buf].modified then
      vim.v.fcs_choice = ''
      vim.schedule(function() msg('kept your unsaved changes; ' .. name .. ' changed on disk', WARN) end)
    else
      vim.v.fcs_choice = 'reload'
    end
  end,
})

api.nvim_create_autocmd('FileChangedShellPost', {
  group = group,
  callback = function(ev)
    local name = short(ev.file)
    vim.schedule(function() msg('reloaded ' .. name) end)
  end,
})

-- Following -----------------------------------------------------------------

local function same_path(a, b)
  if a == '' or b == '' then
    return false
  end
  return fn.resolve(fn.fnamemodify(a, ':p')) == fn.resolve(fn.fnamemodify(b, ':p'))
end

-- A normal file window in this tab: the current one unless it is a sidebar,
-- terminal, quickfix or float.
local function file_window()
  local cur = api.nvim_get_current_win()
  if vim.bo.buftype == '' and api.nvim_win_get_config(cur).relative == '' then
    return cur
  end
  for _, win in ipairs(api.nvim_tabpage_list_wins(0)) do
    local buf = api.nvim_win_get_buf(win)
    if vim.bo[buf].buftype == '' and api.nvim_win_get_config(win).relative == '' then
      return win
    end
  end
  return cur
end

local function jump_to_last_hunk(win, path)
  vim.defer_fn(function()
    if api.nvim_get_current_win() ~= win or busy() then
      return
    end
    local buf = api.nvim_win_get_buf(win)
    if vim.bo[buf].modified or not same_path(api.nvim_buf_get_name(buf), path) then
      return
    end
    pcall(function() require('gitsigns').nav_hunk('last', { navigation_message = false }) end)
  end, 300)
end

-- The agent's edits must not take over a diff review in this tab (two or more
-- diff windows; a lone leftover 'diff' window doesn't count).
local function reviewing_diff()
  local n = 0
  for _, win in ipairs(api.nvim_tabpage_list_wins(0)) do
    if vim.wo[win].diff then
      n = n + 1
    end
  end
  return n >= 2
end

-- :edit in WIN. A stale swap file would raise the ATTENTION dialog, which
-- blocks this nvim and every remote call into it: open read-only instead.
local function edit_in(win, path)
  local au = api.nvim_create_autocmd('SwapExists', {
    group = group,
    once = true,
    callback = function()
      vim.v.swapchoice = 'o'
      vim.schedule(function() msg('swap file exists: opened ' .. short(path) .. ' read-only', WARN) end)
    end,
  })
  local ok, err = pcall(api.nvim_win_call, win, function()
    vim.cmd('silent edit ' .. fn.fnameescape(path))
  end)
  pcall(api.nvim_del_autocmd, au)
  return ok, err
end

-- The tab whose working directory is ROOT.
local function tab_for(root)
  for _, tab in ipairs(api.nvim_list_tabpages()) do
    if same_path(fn.getcwd(-1, api.nvim_tabpage_get_number(tab)), root) then
      return tab
    end
  end
end

-- Each worktree gets its own tab with a tab-local cwd, so an agent (or its
-- subagents) moving between worktrees switches tabs rather than re-rooting
-- one window: each worktree keeps its windows, and pickers and :AgentReview
-- see the tab's own repo.
local function follow(root, path)
  if busy() then
    vim.defer_fn(function() follow(root, path) end, 200)
    return
  end
  vim.cmd('silent! checktime')
  if not following then
    return
  end
  if root ~= '' and fn.isdirectory(root) == 1 and not same_path(root, fn.getcwd()) then
    if reviewing_diff() then
      msg('not following to ' .. fn.fnamemodify(root, ':t') .. ': you are reviewing a diff')
      return
    end
    local tab = tab_for(root)
    if tab then
      api.nvim_set_current_tabpage(tab)
    else
      vim.cmd('tabnew')
      vim.cmd('silent tcd ' .. fn.fnameescape(root))
    end
    msg('following agent to ' .. fn.fnamemodify(root, ':t'))
  end
  if path == '' then
    return
  end
  if reviewing_diff() then
    msg('not opening ' .. short(path) .. ': you are reviewing a diff')
    return
  end
  local win = file_window()
  local buf = api.nvim_win_get_buf(win)
  if not same_path(api.nvim_buf_get_name(buf), path) then
    if vim.bo[buf].modified then
      msg('not opening ' .. short(path) .. ': you have unsaved changes', WARN)
      return
    end
    local ok, err = edit_in(win, path)
    if not ok then
      msg(tostring(err), WARN)
      return
    end
  end
  jump_to_last_hunk(win, path)
end

function _G.AgentFollow(root, path)
  root, path = root or '', path or ''
  vim.schedule(function() follow(root, path) end)
  return 0
end

function _G.AgentChecktime()
  vim.schedule(checktime)
  return 0
end

api.nvim_create_user_command('AgentFollow', function()
  following = not following
  msg('following ' .. (following and 'on' or 'off'))
end, { desc = 'weaver: toggle following the agent to its worktrees and files' })

-- Reviewing -------------------------------------------------------------------

-- stdout of git run in root, or nil if it failed.
local function git(root, args)
  local res = vim.system(vim.list_extend({ 'git', '-C', root }, args), { text = true }):wait()
  return res.code == 0 and res.stdout or nil
end

local function first_line(out)
  local line = out and out:match('^[^\n]*')
  return line ~= '' and line or nil
end

-- Paths from `git … -z`, which (unlike plain output) never C-quotes them.
local function paths(out)
  local list = {}
  for p in (out or ''):gmatch('[^%z]+') do
    list[#list + 1] = p
  end
  return list
end

local function repo_root()
  return first_line(git(fn.getcwd(), { 'rev-parse', '--show-toplevel' }))
end

-- $WEAVER_BASE, else `git config branch.<branch>.weaverbase`, else main/master.
local function base_ref(root)
  local env = vim.env.WEAVER_BASE
  if env and env ~= '' then
    return env
  end
  local branch = first_line(git(root, { 'symbolic-ref', '--quiet', '--short', 'HEAD' }))
  local base = branch and first_line(git(root, { 'config', 'branch.' .. branch .. '.weaverbase' }))
  if base then
    return base
  end
  for _, ref in ipairs({ 'main', 'master' }) do
    if git(root, { 'rev-parse', '--verify', '--quiet', ref .. '^{commit}' }) then
      return ref
    end
  end
end

local function agent_changes()
  local root = repo_root()
  if not root then
    msg('not in a git repository', WARN)
    return
  end
  local base = base_ref(root)
  local files, kind = {}, {}
  local function add(out, k)
    for _, f in ipairs(paths(out)) do
      if not kind[f] then
        kind[f] = k
        files[#files + 1] = f
      end
    end
  end
  if base then
    add(git(root, { 'diff', '-z', '--name-only', base .. '...HEAD' }), 'changed')
  end
  add(git(root, { 'diff', '-z', '--name-only', 'HEAD' }), 'changed')
  add(git(root, { 'ls-files', '-z', '--others', '--exclude-standard' }), 'new')

  -- Per-file stats from the merge base (or HEAD) to the working tree, so
  -- committed and uncommitted changes both count.
  local from = base and first_line(git(root, { 'merge-base', base, 'HEAD' })) or 'HEAD'
  local stats = {}
  for _, rec in ipairs(paths(git(root, { 'diff', '-z', '--numstat', '--no-renames', from }))) do
    local added, removed, f = rec:match('^(%S+)\t(%S+)\t(.+)$')
    if f then
      stats[f] = '+' .. added .. ' -' .. removed
    end
  end

  local items = {}
  for _, f in ipairs(files) do
    items[#items + 1] = {
      filename = root .. '/' .. f,
      lnum = 1,
      text = kind[f] == 'new' and 'new file' or stats[f] or '',
    }
  end
  fn.setqflist({}, ' ', { title = 'AgentChanges vs ' .. (base or 'HEAD'), items = items })
  if #items == 0 then
    msg('no changes vs ' .. (base or 'HEAD'))
  else
    vim.cmd('botright copen')
  end
end

api.nvim_create_user_command('AgentChanges', agent_changes, {
  desc = 'weaver: quickfix list of the files changed on this branch',
})

api.nvim_create_user_command('AgentReview', function()
  local root = repo_root()
  local base = root and base_ref(root)
  if base and fn.exists(':DiffviewOpen') ~= 2 then
    pcall(require, 'diffview')
  end
  if base and fn.exists(':DiffviewOpen') == 2 then
    -- -C: the repo of the cwd, not of the current buffer (which may still be in
    -- the worktree the agent left). --imply-local: HEAD's side is the working
    -- tree, so uncommitted work shows too.
    vim.cmd.DiffviewOpen({ args = { '-C' .. fn.fnameescape(root), base .. '...HEAD', '--imply-local' } })
  else
    agent_changes()
  end
end, { desc = 'weaver: review the branch in diffview (or AgentChanges)' })

-- Startup: `weaver nvim` opened the initial file read-only if it had a swap
-- file; say so, and stop overriding the swap dialog for the user's own edits.
pcall(api.nvim_del_augroup_by_name, 'weaver_start')
if vim.g.weaver_swap then
  local swapped = vim.g.weaver_swap
  vim.g.weaver_swap = nil
  vim.schedule(function() msg('swap file exists: opened ' .. short(swapped) .. ' read-only', WARN) end)
end
