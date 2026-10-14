-- Reloading files the agent changes: watch the directories of loaded files
-- and :checktime shortly after a change. Directory watches use FSEvents on
-- macOS (no handle per file, and they survive the rename-over-original that
-- editors and formatters do). A slow timer, focus and buffer switches catch
-- anything a watcher misses.
--
-- A watch lives as long as some loaded buffer is in its directory, so the cap
-- is a limit on what is open at once, not on what was ever opened.
local M = {}

local api, fn = vim.api, vim.fn
local uv = vim.uv or vim.loop
local util = require('weaver.util')

M.MAX_WATCHED_DIRS = 64
local DEBOUNCE_MS = 100

-- dir -> { handle, bufs = { [buf] = true }, used = ms }
local watched = {}
-- buf -> dir it counts towards
local buf_dir = {}
local timer
local check_queued = false

-- Not while typing a command, in the command-line window, or at a prompt.
function M.busy()
  local mode = api.nvim_get_mode()
  return mode.blocking or mode.mode:match('^[cr]') ~= nil or fn.getcmdwintype() ~= ''
end

function M.checktime()
  if not M.busy() then
    vim.cmd('silent! checktime')
  end
end

local function queue_check()
  if check_queued then
    return
  end
  check_queued = true
  vim.defer_fn(function()
    check_queued = false
    M.checktime()
  end, DEBOUNCE_MS)
end

local function stop(dir)
  local w = watched[dir]
  if not w then
    return
  end
  watched[dir] = nil
  pcall(function()
    w.handle:stop()
    w.handle:close()
  end)
  for b, d in pairs(buf_dir) do
    if d == dir then
      buf_dir[b] = nil
    end
  end
end

local function release(buf)
  local dir = buf_dir[buf]
  if not dir then
    return
  end
  buf_dir[buf] = nil
  local w = watched[dir]
  if w then
    w.bufs[buf] = nil
    if next(w.bufs) == nil then
      stop(dir)
    end
  end
end

-- At the cap, make room by dropping the least recently used watch whose
-- files are not on screen. Returns false when every watch is in view.
local function evict()
  local shown = {}
  for _, win in ipairs(api.nvim_list_wins()) do
    shown[api.nvim_win_get_buf(win)] = true
  end
  local victim, oldest
  for dir, w in pairs(watched) do
    local visible = false
    for b in pairs(w.bufs) do
      if shown[b] then
        visible = true
        break
      end
    end
    if not visible and (not oldest or w.used < oldest) then
      victim, oldest = dir, w.used
    end
  end
  if victim then
    stop(victim)
    return true
  end
  return false
end

function M.watch(buf)
  if not api.nvim_buf_is_valid(buf) or vim.bo[buf].buftype ~= '' then
    return
  end
  local name = api.nvim_buf_get_name(buf)
  if name == '' then
    return
  end
  local dir = fn.fnamemodify(name, ':p:h')
  if buf_dir[buf] == dir then
    watched[dir].used = uv.now()
    return
  end
  release(buf)
  local w = watched[dir]
  if not w then
    if not util.is_dir(dir) then
      return
    end
    if vim.tbl_count(watched) >= M.MAX_WATCHED_DIRS and not evict() then
      return
    end
    local handle = uv.new_fs_event()
    if not handle then
      return
    end
    local ok = handle:start(dir, {}, function(err)
      if not err then
        vim.schedule(queue_check)
      end
    end)
    if not ok then
      handle:close()
      return
    end
    w = { handle = handle, bufs = {} }
    watched[dir] = w
  end
  w.bufs[buf] = true
  w.used = uv.now()
  buf_dir[buf] = dir
end

function M.stop_all()
  for dir in pairs(watched) do
    stop(dir)
  end
  watched, buf_dir = {}, {}
  if timer then
    pcall(function()
      timer:stop()
      timer:close()
    end)
    timer = nil
  end
end

-- Where a reload changed a buffer: the first line of the first changed hunk,
-- kept with the time, so following can put the cursor on the agent's edit.
local before = {}

local function changed_line(old, new)
  local ok, hunks = pcall(vim.diff, table.concat(old, '\n') .. '\n', table.concat(new, '\n') .. '\n', {
    result_type = 'indices',
  })
  if not ok or not hunks or #hunks == 0 then
    return nil
  end
  local h = hunks[1]
  -- { start_a, count_a, start_b, count_b }; a pure deletion has count_b 0.
  return math.max(1, h[3] + (h[4] == 0 and 1 or 0))
end

function M.last_change(buf)
  local c = vim.b[buf].weaver_change
  if type(c) == 'table' and c.line and c.at and (uv.now() - c.at) < 10000 then
    return c.line
  end
end

function M.setup()
  -- With 'autoread' on, nvim reloads an unchanged buffer without asking
  -- FileChangedShell, so the change could not be located. The handler below
  -- makes the same choice (reload unless you have unsaved changes).
  if M.autoread == nil then
    M.autoread = vim.o.autoread
  end
  vim.o.autoread = false
  for _, buf in ipairs(api.nvim_list_bufs()) do
    if api.nvim_buf_is_loaded(buf) then
      M.watch(buf)
    end
  end
  api.nvim_create_autocmd({ 'BufReadPost', 'BufNewFile', 'BufWritePost', 'BufEnter', 'BufFilePost' }, {
    group = util.group,
    callback = function(ev) M.watch(ev.buf) end,
  })
  api.nvim_create_autocmd({ 'BufUnload', 'BufDelete', 'BufWipeout' }, {
    group = util.group,
    callback = function(ev) release(ev.buf) end,
  })
  api.nvim_create_autocmd('VimLeavePre', { group = util.group, callback = M.stop_all })
  api.nvim_create_autocmd({ 'FocusGained', 'BufEnter', 'CursorHold' }, {
    group = util.group,
    callback = M.checktime,
  })

  timer = uv.new_timer()
  timer:start(3000, 3000, vim.schedule_wrap(M.checktime))

  -- Decide here rather than let nvim ask: its W12 "file changed and buffer
  -- modified" dialog blocks this nvim and every remote call into it.
  api.nvim_create_autocmd('FileChangedShell', {
    group = util.group,
    callback = function(ev)
      local name = util.short(ev.file)
      if vim.v.fcs_reason == 'deleted' then
        -- nvim re-fires this on every check while the file is gone: say it once.
        vim.v.fcs_choice = ''
        if not vim.b[ev.buf].weaver_deleted then
          vim.b[ev.buf].weaver_deleted = true
          vim.schedule(function() util.msg('deleted on disk: ' .. name, util.WARN) end)
        end
        return
      end
      vim.b[ev.buf].weaver_deleted = nil
      if vim.bo[ev.buf].modified then
        -- Never reload over your unsaved changes.
        vim.v.fcs_choice = ''
        vim.schedule(function()
          util.msg('kept your unsaved changes; ' .. name .. ' changed on disk', util.WARN)
        end)
      else
        before[ev.buf] = api.nvim_buf_get_lines(ev.buf, 0, -1, false)
        vim.v.fcs_choice = 'reload'
      end
    end,
  })

  api.nvim_create_autocmd('FileChangedShellPost', {
    group = util.group,
    callback = function(ev)
      local old = before[ev.buf]
      before[ev.buf] = nil
      if old and api.nvim_buf_is_valid(ev.buf) then
        local line = changed_line(old, api.nvim_buf_get_lines(ev.buf, 0, -1, false))
        if line then
          vim.b[ev.buf].weaver_change = { line = line, at = uv.now() }
        end
      end
    end,
  })
end

function M.teardown()
  M.stop_all()
  if M.autoread ~= nil then
    vim.o.autoread = M.autoread
  end
end

return M
