-- Polite following. An agent edit is followed (its worktree's tab brought
-- forward, its file shown at the change) only once you have been idle for
-- about 3 s, in normal mode, in a normal file window, and not reviewing a
-- diff. Until then it waits as the pending target in the winbar, and
-- :AgentJump (<leader>aj) goes there at once.
--
-- Following never types into nvim and never switches away from your windows
-- while you work. It never replaces a buffer with unsaved changes, a
-- terminal, quickfix, help, diff or git UI window, and decides all of that
-- before it switches tabs. A tab that is not the current one is brought up
-- to date in the background, without switching to it.
--
-- Each worktree gets its own tab with a tab-local cwd, so an agent (or its
-- subagents) moving between worktrees switches tabs rather than re-rooting
-- one window.
local M = {}

local api, fn = vim.api, vim.fn
local uv = vim.uv or vim.loop
local util = require('weaver.util')

local following = true
local pending -- the Target waiting to be followed
local last_target -- the last Target, for :AgentJump when nothing is pending
local last_seq = 0
local last_input = uv.now()
local poll -- retries the pending Target; runs exactly while one is pending
local ns = api.nvim_create_namespace('weaver_follow_input')

function M.enabled()
  return following
end

local function idle_ms()
  return tonumber(vim.g.weaver_follow_idle_ms or vim.env.WEAVER_FOLLOW_IDLE_MS) or 3000
end

function M.jump_key_label()
  return vim.g.weaver_jump_key_label or '␣aj'
end

-- Targets -------------------------------------------------------------------

-- A place the agent went: { root = existing directory or nil, file = absolute
-- path or nil, seq }, never both nil. Built only by target_of.
local function target_of(ev)
  local root = type(ev.root) == 'string' and ev.root ~= '' and util.is_dir(ev.root) and ev.root or nil
  local file = type(ev.file) == 'string' and ev.file ~= '' and ev.file:sub(1, 1) == '/' and ev.file or nil
  if not root and not file then
    return nil
  end
  return { root = root, file = file, seq = tonumber(ev.seq) or 0 }
end

-- The pending target as the bar shows it: its file name, or its worktree.
function M.pending_label()
  if not pending then
    return nil
  end
  return pending.file and fn.fnamemodify(pending.file, ':t') or (fn.fnamemodify(pending.root, ':t') .. '/')
end

-- Conditions ------------------------------------------------------------------

-- Reviewing a diff: a diffview tab, or two diff windows in this tab (a lone
-- leftover 'diff' window doesn't count).
local function reviewing_diff()
  if util.diffview_view() then
    return true
  end
  local n = 0
  for _, win in ipairs(api.nvim_tabpage_list_wins(0)) do
    if vim.wo[win].diff then
      n = n + 1
    end
  end
  return n >= 2
end

-- Why following has to wait ('mode', 'state', 'cmdwin', 'float', 'diff',
-- 'window' or 'busy'), or nil when it may go ahead now. Most binding first:
-- you asking (Alt v) overrides only the last two.
function M.why_wait()
  local mode = api.nvim_get_mode()
  if mode.blocking or mode.mode ~= 'n' then
    return 'mode'
  end
  -- A mapping or operator half typed, a completion menu, a hit-enter prompt.
  if fn.state():find('[moas]') then
    return 'state'
  end
  if fn.getcmdwintype() ~= '' then
    return 'cmdwin'
  end
  local win = api.nvim_get_current_win()
  if api.nvim_win_get_config(win).relative ~= '' then
    return 'float'
  end
  if reviewing_diff() then
    return 'diff'
  end
  if not util.plain_window(win) then
    return 'window'
  end
  if (uv.now() - last_input) < idle_ms() then
    return 'busy'
  end
end

-- Planning ----------------------------------------------------------------

-- The tab whose working directory is ROOT.
function M.tab_for(root)
  for _, tab in ipairs(api.nvim_list_tabpages()) do
    if util.same_path(util.tab_cwd(tab), root) and not util.diffview_view(tab) then
      return tab
    end
  end
end

-- What going to TARGET means, decided before anything changes. One of
--   { kind = 'new_tab' }                       its worktree has no tab yet
--   { kind = 'tab', tab, show = Show or nil }  that tab (nil show: root only)
--   { kind = 'blocked' }                       only windows with unsaved changes
-- where Show is { kind = 'in', win } or { kind = 'beside', win } (a tab with
-- only a terminal, quickfix or git UI gets a split, never a replacement).
local function plan(target)
  local tab = api.nvim_get_current_tabpage()
  if target.root and not util.same_path(target.root, util.tab_cwd(0)) then
    tab = M.tab_for(target.root)
    if not tab then
      return { kind = 'new_tab' }
    end
  end
  if not target.file then
    return { kind = 'tab', tab = tab }
  end
  local win, why = util.window_for(tab, target.file)
  if win then
    return { kind = 'tab', tab = tab, show = { kind = 'in', win = win } }
  elseif why == 'nowindow' then
    return { kind = 'tab', tab = tab, show = { kind = 'beside', win = api.nvim_tabpage_get_win(tab) } }
  end
  return { kind = 'blocked' }
end

-- Doing -------------------------------------------------------------------

-- :edit PATH in WIN. A stale swap file would raise the ATTENTION dialog,
-- which blocks this nvim and every remote call into it: open read-only.
local function edit_in(win, path)
  local au = api.nvim_create_autocmd('SwapExists', {
    group = util.group,
    once = true,
    callback = function()
      vim.v.swapchoice = 'o'
      vim.schedule(function() util.msg('swap file exists: opened ' .. util.short(path) .. ' read-only', util.WARN) end)
    end,
  })
  local ok, err = pcall(api.nvim_win_call, win, function()
    vim.cmd('silent keepalt edit ' .. fn.fnameescape(path))
  end)
  pcall(api.nvim_del_autocmd, au)
  return ok, err
end

-- The line of the agent's change in PATH, then CB(line or nil): from the
-- reload that just happened, else the first hunk against the start of the
-- turn (or HEAD).
local function change_line(buf, root, path, cb)
  local line = require('weaver.reload').last_change(buf)
  if line then
    return cb(line)
  end
  local top = root or fn.fnamemodify(path, ':h')
  local sid = require('weaver.status').agent.sid
  local function diff_against(rev)
    util.git_async(top, { 'diff', '-U0', '--no-color', '--no-ext-diff', rev, '--', path }, function(out)
      cb(out and tonumber(out:match('\n@@ %-[%d,]+ %+(%d+)')))
    end)
  end
  if not sid then
    return diff_against('HEAD')
  end
  util.git_async(top, { 'rev-parse', '--verify', '--quiet', 'refs/worktree/weaver/sessions/' .. sid }, function(out)
    diff_against(util.first_line(out) or 'HEAD')
  end)
end

-- Put WIN's cursor on the agent's change, unless you have typed since.
local function place_cursor(win, path, root)
  local buf = api.nvim_win_get_buf(win)
  local asked = uv.now()
  change_line(buf, root, path, function(line)
    if not line or last_input > asked or not api.nvim_win_is_valid(win) or api.nvim_win_get_buf(win) ~= buf then
      return
    end
    pcall(api.nvim_win_set_cursor, win, { math.min(line, api.nvim_buf_line_count(buf)), 0 })
    pcall(api.nvim_win_call, win, function() vim.cmd('normal! zz') end)
  end)
end

-- Show TARGET's file in WIN, at the change. Returns whether it is shown.
local function show_file(win, target)
  if not util.same_path(api.nvim_buf_get_name(api.nvim_win_get_buf(win)), target.file) then
    local ok, err = edit_in(win, target.file)
    if not ok then
      util.msg(tostring(err), util.WARN)
      return false
    end
  end
  place_cursor(win, target.file, target.root)
  return true
end

-- A window beside WIN for TARGET's file.
local function split_beside(win, target)
  local buf = fn.bufadd(target.file)
  vim.bo[buf].buflisted = true
  local ok, new = pcall(api.nvim_open_win, buf, false, { split = 'left', win = win })
  if not ok then
    util.msg(tostring(new), util.WARN)
    return nil
  end
  pcall(api.nvim_win_call, new, function() vim.cmd('silent! edit') end)
  return new
end

local function set_pending(target)
  pending = target
  if not poll then
    poll = uv.new_timer()
    poll:start(250, 250, vim.schedule_wrap(function() M.retry() end))
  end
  require('weaver.status').redraw()
end

local function clear_pending()
  pending = nil
  if poll then
    pcall(function()
      poll:stop()
      poll:close()
    end)
    poll = nil
  end
  require('weaver.status').redraw()
end

-- Go to TARGET now, as BY wants it: 'agent' (following) or 'you' (you asked:
-- you are told every time something is in the way, and the cursor moves even
-- out of a terminal). Returns true when TARGET is shown.
local function go(target, by)
  local p = plan(target)
  if p.kind == 'blocked' then
    if by == 'you' or not target.warned then
      target.warned = true
      util.msg('not opening ' .. util.short(target.file) .. ': you have unsaved changes there', util.WARN)
    end
    return false
  end
  if p.kind == 'new_tab' then
    vim.cmd('tabnew')
    vim.cmd('silent tcd ' .. fn.fnameescape(target.root))
    return not target.file or show_file(api.nvim_get_current_win(), target)
  end
  if p.tab ~= api.nvim_get_current_tabpage() then
    api.nvim_set_current_tabpage(p.tab)
  end
  if not p.show then
    return true
  end
  local win = p.show.kind == 'in' and p.show.win or split_beside(p.show.win, target)
  if not win or not show_file(win, target) then
    return false
  end
  -- The cursor moves to the file only from another file window (or when you
  -- asked); a terminal or git UI you were in keeps it.
  if by == 'you' or util.plain_window(api.nvim_get_current_win()) then
    api.nvim_set_current_win(win)
  end
  return true
end

-- Show TARGET in its worktree's tab when that is not the current tab,
-- without switching to it.
local function update_in_background(target)
  local p = plan(target)
  if p.kind == 'tab' and p.tab ~= api.nvim_get_current_tabpage() and p.show and p.show.kind == 'in' then
    show_file(p.show.win, target)
  end
end

-- The idle poll: follow the pending target once nothing is in the way.
function M.retry()
  if not pending or not following or M.why_wait() then
    return
  end
  if go(pending, 'agent') then
    clear_pending()
  end
end

-- An edit or root event from the hook.
function M.on_event(ev)
  local seq = tonumber(ev.seq) or 0
  if seq ~= 0 then
    if seq < last_seq then
      return
    end
    last_seq = seq
  end
  local target = target_of({ root = ev.root, file = ev.ev == 'edit' and ev.file or nil, seq = seq })
  if not target then
    return
  end
  last_target = target
  if not following then
    return
  end
  if not M.why_wait() and go(target, 'agent') then
    clear_pending()
    return
  end
  update_in_background(target)
  set_pending(target)
end

-- You asked to see the agent (Alt v): go now without waiting for idleness,
-- even from a terminal window, but not out of insert or visual mode, a float
-- or a diff review, and never over unsaved changes.
function M.on_focus(ev)
  local target = target_of(ev)
  if not target then
    return
  end
  last_target = target
  local wait = M.why_wait()
  if (wait == nil or wait == 'busy' or wait == 'window') and go(target, 'you') then
    clear_pending()
    return
  end
  set_pending(target)
end

-- :AgentJump: go to the pending target (or the last one) now.
function M.jump()
  local target = pending or last_target
  if not target then
    util.msg('the agent has not edited anything yet')
    return
  end
  if go(target, 'you') then
    clear_pending()
  end
end

function M.toggle()
  following = not following
  if not following then
    clear_pending()
  end
  util.msg('following ' .. (following and 'on' or 'off'))
  require('weaver.status').redraw()
end

-- Tabs of removed worktrees -----------------------------------------------

local function has_unsaved(tab)
  for _, win in ipairs(api.nvim_tabpage_list_wins(tab)) do
    if vim.bo[api.nvim_win_get_buf(win)].modified then
      return true
    end
  end
  return false
end

-- Close tabs whose tab-local directory no longer exists (a removed worktree)
-- unless they hold unsaved changes, and unload that worktree's hidden buffers.
function M.prune_tabs()
  local cur = api.nvim_get_current_tabpage()
  for _, tab in ipairs(api.nvim_list_tabpages()) do
    local nr = api.nvim_tabpage_get_number(tab)
    local dir = fn.getcwd(-1, nr)
    if
      tab ~= cur
      and #api.nvim_list_tabpages() > 1
      and fn.haslocaldir(-1, nr) == 1
      and not util.is_dir(dir)
      and not has_unsaved(tab)
    then
      pcall(vim.cmd, 'tabclose ' .. nr)
      for _, buf in ipairs(api.nvim_list_bufs()) do
        if
          api.nvim_buf_is_loaded(buf)
          and not vim.bo[buf].modified
          and api.nvim_buf_get_name(buf):sub(1, #dir + 1) == dir .. '/'
          and fn.bufwinid(buf) == -1
        then
          pcall(api.nvim_buf_delete, buf, {})
        end
      end
      util.msg('closed the tab of removed worktree ' .. fn.fnamemodify(dir, ':t'))
    end
  end
end

function M.setup()
  vim.on_key(function()
    last_input = uv.now()
  end, ns)
  api.nvim_create_autocmd({ 'TabEnter', 'FocusGained' }, {
    group = util.group,
    callback = function() vim.schedule(M.prune_tabs) end,
  })
end

function M.teardown()
  vim.on_key(nil, ns)
  clear_pending()
end

return M
