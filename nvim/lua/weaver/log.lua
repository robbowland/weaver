-- :AgentLog: the agent's activity log (docs/design.md, Activity log) as a
-- timeline grouped by turn, newest at the bottom, kept current while shown.
-- <CR> opens an edited file in its worktree's tab, or a turn's own diff from
-- its heading; q closes.
local M = {}

local api, fn = vim.api, vim.fn
local util = require('weaver.util')

local MAX_ENTRIES = 1000
local ns = api.nvim_create_namespace('weaver_log')
local bufnr
local actions_by_line = {}

function M.path()
  local agent = require('weaver.status').refresh_from_file()
  if agent.log and agent.log ~= '' then
    return agent.log
  end
  if agent.sid and agent.sid ~= '' then
    return util.state_dir() .. '/log/' .. agent.sid .. '.jsonl'
  end
end

-- The log's entries, oldest first (the last MAX_ENTRIES).
function M.read(path)
  local f = path and io.open(path, 'r')
  if not f then
    return {}
  end
  local entries = {}
  for line in f:lines() do
    local ok, e = pcall(vim.json.decode, line, { luanil = { object = true, array = true } })
    if ok and type(e) == 'table' and e.ev then
      entries[#entries + 1] = e
      if #entries > MAX_ENTRIES * 2 then
        entries = vim.list_slice(entries, MAX_ENTRIES + 1)
      end
    end
  end
  f:close()
  if #entries > MAX_ENTRIES then
    entries = vim.list_slice(entries, #entries - MAX_ENTRIES + 1)
  end
  return entries
end

local function one(s, n)
  s = tostring(s or ''):gsub('%s+', ' ')
  if fn.strchars(s) > n then
    s = fn.strcharpart(s, 0, n - 1) .. '…'
  end
  return s
end

-- What an entry says, as { kind, text, hl }.
local function describe(e)
  local ev = e.ev
  if ev == 'prompt' then
    return 'prompt', one(e.text, 200), 'Normal'
  elseif ev == 'edit' then
    local who = e.agent_id and (' · ' .. e.agent_id) or ''
    return 'edit', fn.fnamemodify(e.file or '', ':~:.') .. '  ' .. (e.tool or '') .. who, 'Normal'
  elseif ev == 'bash' then
    return 'bash', one(e.cmd, 120) .. (e.file and ('  → ' .. fn.fnamemodify(e.file, ':t')) or ''), 'Comment'
  elseif ev == 'needs-you' then
    return 'needs you', one(e.waiting, 120), 'WeaverBarAlert'
  elseif ev == 'stop' then
    return 'done', one(e.last, 160), 'WeaverBarOk'
  elseif ev == 'error' then
    return 'error', one(e.error, 160), 'WeaverBarAlert'
  elseif ev == 'interrupt' then
    return 'interrupted', e.tool or '', 'WeaverBarFaint'
  elseif ev == 'subagent' then
    return 'subagent', (e.phase or '') .. ' ' .. (e.agent_type or '') .. ' ' .. one(e.desc, 80), 'Comment'
  elseif ev == 'snapshot' then
    return 'snapshot', (e.point or '') .. ' ' .. fn.fnamemodify(e.root or '', ':t') .. ' ' .. (e.commit or ''):sub(1, 7), 'WeaverBarFaint'
  elseif ev == 'session' then
    return 'session', e.source or '', 'WeaverBarFaint'
  elseif ev == 'cwd' then
    return 'cwd', fn.fnamemodify(e.root or e.cwd or '', ':~'), 'WeaverBarFaint'
  elseif ev == 'state' then
    return 'state', e.state or '', 'WeaverBarFaint'
  elseif ev == 'end' then
    return 'ended', '', 'WeaverBarFaint'
  end
  return ev, '', 'WeaverBarFaint'
end

-- Lines and, per line, what <CR> does there.
function M.render(entries)
  local lines, actions, hls = {}, {}, {}
  local turn
  for _, e in ipairs(entries) do
    if e.ev == 'prompt' or (e.turn and e.turn ~= turn and e.ev ~= 'state') then
      turn = e.turn
      if #lines > 0 then
        lines[#lines + 1] = ''
      end
      local head = ('turn %s · %s'):format(turn or '?', os.date('%H:%M', e.t or 0))
      if e.ev == 'prompt' then
        head = head .. ' · ' .. one(e.text, 100)
      end
      lines[#lines + 1] = head
      actions[#lines] = { turn = turn }
      hls[#lines] = 'Title'
    end
    if e.ev ~= 'prompt' then
      local kind, text, hl = describe(e)
      lines[#lines + 1] = ('  %s  %-11s %s'):format(os.date('%H:%M:%S', e.t or 0), kind, text)
      hls[#lines] = hl
      if e.ev == 'edit' or (e.ev == 'bash' and e.file) then
        actions[#lines] = { file = e.file, root = e.root }
      end
    end
  end
  return lines, actions, hls
end

local function draw(buf)
  local entries = M.read(M.path())
  local lines, actions, hls = M.render(entries)
  if #lines == 0 then
    lines = { 'no activity yet' }
  end
  vim.bo[buf].modifiable = true
  api.nvim_buf_set_lines(buf, 0, -1, false, lines)
  vim.bo[buf].modifiable = false
  vim.bo[buf].modified = false
  api.nvim_buf_clear_namespace(buf, ns, 0, -1)
  for l, hl in pairs(hls) do
    pcall(api.nvim_buf_set_extmark, buf, ns, l - 1, 0, { end_row = l, hl_group = hl, hl_eol = false })
  end
  actions_by_line = actions
  for _, win in ipairs(fn.win_findbuf(buf)) do
    -- Stay at the bottom, unless you scrolled up to read.
    local last = api.nvim_buf_line_count(buf)
    local cur = api.nvim_win_get_cursor(win)[1]
    if win ~= api.nvim_get_current_win() or cur >= last - 1 or vim.w[win].weaver_log_tail then
      pcall(api.nvim_win_set_cursor, win, { last, 0 })
      vim.w[win].weaver_log_tail = true
    end
  end
end

function M.refresh()
  if bufnr and api.nvim_buf_is_valid(bufnr) and #fn.win_findbuf(bufnr) > 0 then
    draw(bufnr)
  end
end

local function act()
  local a = actions_by_line[api.nvim_win_get_cursor(0)[1]]
  if not a then
    return
  end
  if a.file then
    if not util.is_dir(fn.fnamemodify(a.file, ':h')) or fn.filereadable(a.file) == 0 then
      util.msg('not on disk: ' .. util.short(a.file), util.WARN)
      return
    end
    local follow = require('weaver.follow')
    local tab = a.root and follow.tab_for(a.root) or nil
    if tab then
      api.nvim_set_current_tabpage(tab)
    else
      vim.cmd('wincmd p')
    end
    local win = util.window_for(api.nvim_get_current_tabpage(), a.file) or api.nvim_get_current_win()
    api.nvim_set_current_win(win)
    vim.cmd('edit ' .. fn.fnameescape(a.file))
  elseif a.turn then
    local root = util.current_root()
    local sid = require('weaver.status').agent.sid
    if not root or not sid then
      return
    end
    for _, it in ipairs(require('weaver.turns').items(root, sid)) do
      if it.turn == a.turn then
        require('weaver.review').open_turn(root, it)
        return
      end
    end
    util.msg('turn ' .. a.turn .. ' changed nothing in ' .. fn.fnamemodify(root, ':t'))
  end
end

function M.open()
  local path = M.path()
  if not path then
    util.msg('no agent session is bound to this nvim', util.WARN)
    return
  end
  if not (bufnr and api.nvim_buf_is_valid(bufnr)) then
    bufnr = api.nvim_create_buf(false, true)
    api.nvim_buf_set_name(bufnr, 'weaver://log')
    vim.bo[bufnr].filetype = 'weaverlog'
    vim.bo[bufnr].bufhidden = 'hide'
    vim.keymap.set('n', '<CR>', act, { buffer = bufnr, desc = 'weaver: open the file or the turn' })
    vim.keymap.set('n', 'q', '<cmd>close<cr>', { buffer = bufnr, desc = 'close' })
  end
  local win = fn.win_findbuf(bufnr)[1]
  if win then
    api.nvim_set_current_win(win)
  else
    vim.cmd('botright 12split')
    api.nvim_win_set_buf(0, bufnr)
    vim.wo.number = false
    vim.wo.relativenumber = false
    vim.wo.wrap = false
    vim.wo.winfixheight = true
  end
  vim.w.weaver_log_tail = true
  draw(bufnr)
end

return M
