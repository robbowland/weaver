-- Review notes for the agent: as you review (a diffview review, or any file in
-- the agent's nvim), you note lines with a comment; then send them all as one
-- message, which weaver pastes at the agent's prompt (`weaver send`) when it
-- waits for you to say more.
local M = {}

local api, fn = vim.api, vim.fn
local util = require('weaver.util')

local NS = api.nvim_create_namespace('weaver_notes')
-- The lines of code a note quotes, at most.
local QUOTE = 6

-- { path, l1, l2, as_of, lines, text, buf }, in the order you noted them.
M.items = {}

local function span(n)
  return n.l1 == n.l2 and tostring(n.l1) or (n.l1 .. '-' .. n.l2)
end

-- Where lines L1..L2 of the current window are: the file (relative to its
-- repository), and, in a review, which version of it they read as.
local function where(l1, l2)
  local buf = api.nvim_get_current_buf()
  local name = api.nvim_buf_get_name(buf)
  local path, as_of
  local view = util.diffview_view()
  if view then
    local ok, file = pcall(view.infer_cur_file, view)
    if ok and file and file.path then
      if name:match('^diffview://') then
        path = file.path
        local rev = name:match('/%.git/([^/]+)/')
        if rev == ':0:' then
          as_of = 'staged'
        elseif rev and rev:match('^%x+$') then
          as_of = 'as of ' .. rev:sub(1, 8)
        end
      elseif file.absolute_path and util.same_path(file.absolute_path, name) then
        path = file.path
      end
    end
  end
  if not path then
    if name == '' or vim.bo[buf].buftype ~= '' then
      return nil
    end
    local root = util.toplevel(fn.fnamemodify(name, ':p:h'))
    local real = util.real(name) or name
    if root and util.under(real, root) then
      path = real:sub(#util.real(root) + 2)
    else
      path = fn.fnamemodify(name, ':~')
    end
  end
  return { path = path, l1 = l1, l2 = l2, as_of = as_of, lines = api.nvim_buf_get_lines(buf, l1 - 1, l2, false), buf = buf }
end

-- Note lines L1..L2 of the current window with TEXT (asked for when nil).
function M.add(l1, l2, text)
  local n = where(l1, l2)
  if not n then
    util.msg('a note goes on a file', util.WARN)
    return
  end
  local function keep(t)
    if not t or not t:match('%S') then
      return
    end
    n.text = vim.trim(t)
    table.insert(M.items, n)
    pcall(api.nvim_buf_set_extmark, n.buf, NS, l2 - 1, 0,
      { virt_text = { { '  note ' .. #M.items, 'Comment' } }, virt_text_pos = 'eol' })
    util.msg(('note %d on %s:%s; send them with :AgentSend'):format(#M.items, n.path, span(n)))
  end
  if text then
    keep(text)
  else
    vim.ui.input({ prompt = ('note on %s:%s: '):format(n.path, span(n)) }, keep)
  end
end

-- The notes as the message the agent gets.
function M.message()
  local out = { 'Review notes on your changes (from weaver):', '' }
  for i, n in ipairs(M.items) do
    out[#out + 1] = ('%d. %s:%s%s'):format(i, n.path, span(n), n.as_of and (' (' .. n.as_of .. ')') or '')
    for j = 1, math.min(#n.lines, QUOTE) do
      out[#out + 1] = '   > ' .. n.lines[j]:gsub('%s+$', ''):sub(1, 160)
    end
    if #n.lines > QUOTE then
      out[#out + 1] = '   > …'
    end
    for line in (n.text .. '\n'):gmatch('(.-)\n') do
      out[#out + 1] = '   ' .. line
    end
    out[#out + 1] = ''
  end
  return (table.concat(out, '\n'):gsub('\n+$', ''))
end

function M.clear()
  for _, n in ipairs(M.items) do
    if api.nvim_buf_is_valid(n.buf) then
      api.nvim_buf_clear_namespace(n.buf, NS, 0, -1)
    end
  end
  M.items = {}
end

-- Send the notes to the agent this nvim is bound to; they go once it has them.
function M.send(force)
  if #M.items == 0 then
    util.msg('no notes to send (note lines with :AgentNote)')
    return
  end
  local bin, pane = vim.env.WEAVER_BIN, vim.env.WEAVER_PANE
  if not bin or bin == '' or not pane or pane == '' then
    util.msg('this nvim is not bound to an agent', util.WARN)
    return
  end
  local cmd = { bin, 'send', pane }
  if force then
    table.insert(cmd, 3, '--force')
  end
  vim.system(cmd, { stdin = M.message(), text = true }, vim.schedule_wrap(function(r)
    if r.code == 0 then
      local n = #M.items
      M.clear()
      util.msg(('sent %d note%s to the agent'):format(n, n == 1 and '' or 's'))
    else
      util.msg('notes not sent: ' .. vim.trim((r.stderr or ''):gsub('^weaver: ', '')), util.WARN)
    end
  end))
end

-- The notes in the quickfix list, to look them over.
function M.list()
  local qf = {}
  for i, n in ipairs(M.items) do
    qf[#qf + 1] = { bufnr = n.buf, lnum = n.l1, text = ('note %d: %s'):format(i, n.text) }
  end
  fn.setqflist({}, ' ', { title = 'weaver notes', items = qf })
  if #qf > 0 then
    vim.cmd('copen')
  else
    util.msg('no notes')
  end
end

return M
