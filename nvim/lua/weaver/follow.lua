-- Polite following. An agent edit is followed (its worktree's tab brought
-- forward, its file shown at the change) only once you have been idle for
-- about 3 s, in normal mode, in a normal file window, and not reviewing a
-- diff. Until then it waits as the pending target in the winbar, and
-- :AgentJump (<leader>aj) goes there at once.
--
-- Following never types into nvim and never switches away from your windows
-- while you work. It never replaces a buffer with unsaved changes, a
-- terminal, quickfix, help, diff or git UI window, and checks all of that
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
local pending -- { root, file, seq }
local last_target -- the last target followed or pending, for :AgentJump
local last_seq = 0
local last_input = uv.now()
local warned = {} -- target key -> true, so a blocked follow warns once
local poll
local ns = api.nvim_create_namespace('weaver_follow_input')

function M.enabled()
  return following
end

local function idle_ms()
  local v = tonumber(vim.g.weaver_follow_idle_ms or vim.env.WEAVER_FOLLOW_IDLE_MS)
  return v or 3000
end

function M.jump_key_label()
  return vim.g.weaver_jump_key_label or '␣aj'
end

function M.pending_label()
  if not pending then
    return nil
  end
  if pending.file and pending.file ~= '' then
    return fn.fnamemodify(pending.file, ':t')
  end
  return fn.fnamemodify(pending.root, ':t') .. '/'
end

function M.pending()
  return pending
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
M.reviewing_diff = reviewing_diff

-- Why following has to wait, or nil when it may go ahead now. The reasons
-- are checked most binding first: on_focus overrides only the last two.
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
local function tab_for(root)
  for _, tab in ipairs(api.nvim_list_tabpages()) do
    if util.same_path(util.tab_cwd(tab), root) and not util.diffview_view(tab) then
      return tab
    end
  end
end
M.tab_for = tab_for

-- What following TARGET would do, decided before anything changes:
-- { tab = tab or nil (a new tab), win = win or nil, split = win or nil }.
local function plan(target)
  local p = {}
  local root = target.root
  if root and root ~= '' and util.is_dir(root) and not util.same_path(root, util.tab_cwd(0)) then
    p.tab = tab_for(root)
    p.new_tab = p.tab == nil
  else
    p.tab = api.nvim_get_current_tabpage()
  end
  if not target.file or target.file == '' then
    return p
  end
  if p.new_tab then
    return p
  end
  local win, why = util.window_for(p.tab, target.file)
  if win then
    p.win = win
  elseif why == 'nowindow' then
    -- Only a terminal, quickfix or git UI here: open beside it, never over it.
    p.split = api.nvim_tabpage_get_win(p.tab)
  else
    p.blocked = why
  end
  return p
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
  local top = root and root ~= '' and root or fn.fnamemodify(path, ':h')
  local sid = require('weaver.status').agent.sid
  local function diff_against(rev)
    util.git_async(top, { 'diff', '-U0', '--no-color', '--no-ext-diff', rev, '--', path }, function(out)
      cb(out and tonumber(out:match('\n@@ %-[%d,]+ %+(%d+)')))
    end)
  end
  if sid and sid ~= '' then
    util.git_async(top, { 'rev-parse', '--verify', '--quiet', 'refs/worktree/weaver/sessions/' .. sid }, function(out)
      diff_against(util.first_line(out) or 'HEAD')
    end)
  else
    diff_against('HEAD')
  end
end

local function place_cursor(win, path, root)
  if not api.nvim_win_is_valid(win) then
    return
  end
  local buf = api.nvim_win_get_buf(win)
  local asked = uv.now()
  change_line(buf, root, path, function(line)
    if not line or not api.nvim_win_is_valid(win) or api.nvim_win_get_buf(win) ~= buf then
      return
    end
    -- You may have started working there in the meantime.
    if last_input > asked then
      return
    end
    line = math.min(line, api.nvim_buf_line_count(buf))
    pcall(api.nvim_win_set_cursor, win, { line, 0 })
    pcall(api.nvim_win_call, win, function() vim.cmd('normal! zz') end)
  end)
end

local function show_file(win, path, root)
  if not util.same_path(api.nvim_buf_get_name(api.nvim_win_get_buf(win)), path) then
    local ok, err = edit_in(win, path)
    if not ok then
      util.msg(tostring(err), util.WARN)
      return false
    end
  end
  place_cursor(win, path, root)
  return true
end

local function key_of(t)
  return (t.root or '') .. '\0' .. (t.file or '')
end

local function set_pending(target)
  pending = target
  require('weaver.status').redraw()
  if not poll then
    poll = uv.new_timer()
    poll:start(250, 250, vim.schedule_wrap(function() M.retry() end))
  end
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

-- Bring a tab that is not the current one up to date without switching to it.
local function update_in_background(target, p)
  if not target.file or target.file == '' or not p.tab or p.tab == api.nvim_get_current_tabpage() then
    return
  end
  if p.win then
    show_file(p.win, target.file, target.root)
  end
end

-- Follow TARGET now. FORCE (you asked) skips the idle rule but never the
-- unsaved-changes rule. Returns true when it was followed.
local function follow_now(target, force)
  local p = plan(target)
  if p.blocked then
    local k = key_of(target)
    if force or not warned[k] then
      warned[k] = true
      util.msg('not opening ' .. util.short(target.file) .. ': you have unsaved changes there', util.WARN)
    end
    return false
  end
  if p.new_tab then
    vim.cmd('tabnew')
    vim.cmd('silent tcd ' .. fn.fnameescape(target.root))
    if target.file and target.file ~= '' then
      show_file(api.nvim_get_current_win(), target.file, target.root)
    end
    return true
  end
  if p.tab ~= api.nvim_get_current_tabpage() then
    api.nvim_set_current_tabpage(p.tab)
  end
  if not target.file or target.file == '' then
    return true
  end
  local win = p.win
  if p.split then
    local buf = fn.bufadd(target.file)
    vim.bo[buf].buflisted = true
    local ok, w = pcall(api.nvim_open_win, buf, false, { split = 'left', win = p.split })
    if not ok then
      util.msg(tostring(w), util.WARN)
      return false
    end
    win = w
    pcall(api.nvim_win_call, win, function() vim.cmd('silent! edit') end)
  end
  if not show_file(win, target.file, target.root) then
    return false
  end
  -- Focus moves to the file only from another file window; a terminal or
  -- git UI you were in keeps the cursor.
  if util.plain_window(api.nvim_get_current_win()) or force then
    api.nvim_set_current_win(win)
  end
  return true
end

-- Try the pending target again (the idle poll, and after you return).
function M.retry()
  if not pending then
    return clear_pending()
  end
  if not following then
    return
  end
  if M.why_wait() then
    return
  end
  local target = pending
  local p = plan(target)
  if p.blocked then
    -- Stays pending until the change is saved; warn once.
    follow_now(target)
    return
  end
  clear_pending()
  follow_now(target)
end

-- An edit or root event from the hook.
function M.on_event(ev)
  local s = tonumber(ev.seq) or 0
  if s ~= 0 and s < last_seq then
    return
  end
  last_seq = s
  local target = { root = ev.root or '', file = ev.ev == 'edit' and ev.file or nil, seq = s }
  if target.root == '' and not target.file then
    return
  end
  last_target = target
  if not following then
    return
  end
  local wait = M.why_wait()
  if not wait then
    if follow_now(target) then
      clear_pending()
    else
      set_pending(target)
    end
    return
  end
  update_in_background(target, plan(target))
  set_pending(target)
end

-- You asked to see the agent (Alt v): go now without waiting for idleness,
-- even from a terminal window, but not out of insert or visual mode, a
-- float or a diff review, and never over unsaved changes.
function M.on_focus(ev)
  local target = { root = ev.root or '', file = (ev.file ~= '' and ev.file) or nil, seq = tonumber(ev.seq) or 0 }
  if target.root == '' and not target.file then
    return
  end
  last_target = target
  local wait = M.why_wait()
  if (wait == nil or wait == 'busy' or wait == 'window') and follow_now(target, true) then
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
  if follow_now(target, true) then
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

-- Close tabs whose tab-local directory no longer exists (a removed worktree)
-- unless they hold unsaved changes, and unload that worktree's buffers.
function M.prune_tabs()
  local tabs = api.nvim_list_tabpages()
  if #tabs < 2 then
    return
  end
  local cur = api.nvim_get_current_tabpage()
  for _, tab in ipairs(tabs) do
    local nr = api.nvim_tabpage_get_number(tab)
    local dir = fn.getcwd(-1, nr)
    if tab ~= cur and fn.haslocaldir(-1, nr) == 1 and not util.is_dir(dir) and #api.nvim_list_tabpages() > 1 then
      local dirty = false
      for _, win in ipairs(api.nvim_tabpage_list_wins(tab)) do
        if vim.bo[api.nvim_win_get_buf(win)].modified then
          dirty = true
        end
      end
      if not dirty then
        pcall(vim.cmd, 'tabclose ' .. api.nvim_tabpage_get_number(tab))
        for _, buf in ipairs(api.nvim_list_bufs()) do
          local name = api.nvim_buf_get_name(buf)
          if
            api.nvim_buf_is_loaded(buf)
            and not vim.bo[buf].modified
            and name:sub(1, #dir + 1) == dir .. '/'
            and fn.bufwinid(buf) == -1
          then
            pcall(api.nvim_buf_delete, buf, {})
          end
        end
        util.msg('closed the tab of removed worktree ' .. fn.fnamemodify(dir, ':t'))
      end
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
