-- The agent's state as this nvim knows it, and the bar that shows it: a
-- winbar on normal file windows (state, turn, prompt, and the edit waiting to
-- be followed). Seeded once from the state file, then kept current by the
-- hook's events.
local M = {}

local api = vim.api
local util = require('weaver.util')

M.agent = {}

local seq = 0

-- Fields an event or the state file may carry.
local FIELDS = { 'state', 'turn', 'title', 'prompt', 'waiting', 'root', 'agent' }

function M.seed()
  local st = util.read_json(vim.env.WEAVER_STATE)
  if not st then
    return
  end
  for _, k in ipairs(FIELDS) do
    if st[k] ~= nil then
      M.agent[k] = st[k]
    end
  end
  M.agent.sid = st.session_id
  M.agent.log = st.log
  M.agent.files = st.files
end

-- Take what an event says about the agent. Older events (by seq) never
-- overwrite newer ones.
function M.update(ev)
  local s = tonumber(ev.seq) or 0
  if s ~= 0 and s < seq then
    return
  end
  seq = s
  for _, k in ipairs(FIELDS) do
    if ev[k] ~= nil then
      M.agent[k] = ev[k]
    end
  end
  if ev.sid and ev.sid ~= '' then
    if M.agent.sid ~= ev.sid then
      M.agent.files = nil
    end
    M.agent.sid = ev.sid
  end
  if ev.ev == 'state' and ev.state ~= 'needs-you' then
    M.agent.waiting = nil
  end
  if ev.ev == 'edit' and ev.file then
    local files = M.agent.files or {}
    files[#files + 1] = ev.file
    M.agent.files = files
  end
  M.redraw()
end

-- Re-read the state file for what events don't carry (the log path, files).
function M.refresh_from_file()
  local st = util.read_json(vim.env.WEAVER_STATE)
  if st then
    M.agent.log = st.log or M.agent.log
    M.agent.files = st.files or M.agent.files
    if not M.agent.sid then
      M.agent.sid = st.session_id
    end
  end
  return M.agent
end

-- Highlights --------------------------------------------------------------

-- Micrographics colours when that scheme is loaded, else links to groups
-- every scheme defines.
local function set_highlights()
  local p = vim.g.micrographics_palette
  local bar = api.nvim_get_hl(0, { name = 'WinBar', link = false })
  local bg = bar.bg
  local function set(name, fg, link)
    if p and fg then
      api.nvim_set_hl(0, name, { fg = fg, bg = bg })
    else
      api.nvim_set_hl(0, name, { link = link })
    end
  end
  set('WeaverBar', p and p.metadata, 'WinBar')
  set('WeaverBarFaint', p and p.faint, 'Comment')
  set('WeaverBarInk', p and p.ink, 'WinBar')
  set('WeaverBarOk', p and p.success, 'DiagnosticOk')
  set('WeaverBarAlert', p and p.danger, 'DiagnosticError')
end

-- The bar ---------------------------------------------------------------

local GLYPH = {
  working = { '●', 'WeaverBarInk' },
  ['needs-you'] = { '!', 'WeaverBarAlert' },
  done = { '✓', 'WeaverBarOk' },
  idle = { '○', 'WeaverBarFaint' },
  interrupted = { '○', 'WeaverBarFaint' },
  ended = { '·', 'WeaverBarFaint' },
}
local WORD = { ['needs-you'] = 'needs you' }

local function esc(s)
  return (tostring(s or ''):gsub('%%', '%%%%'):gsub('[%c]', ' '))
end

-- Plain text, for a statusline component (lualine and the like).
function M.text()
  local a = M.agent
  if not a.state then
    return ''
  end
  local parts = { (GLYPH[a.state] or { '·' })[1] .. ' ' .. (WORD[a.state] or a.state) }
  if a.turn and a.turn ~= 0 then
    parts[#parts + 1] = 'turn ' .. a.turn
  end
  local pending = require('weaver.follow').pending_label()
  if pending then
    parts[#parts + 1] = '→ ' .. pending
  end
  return table.concat(parts, ' · ')
end

-- The winbar: state and turn, then the edit waiting to be followed (the one
-- thing you may act on), then what the agent was asked (or waits on), dim,
-- cut to the window's width.
function M.winbar()
  local a = M.agent
  local follow = require('weaver.follow')
  local parts, width = {}, 0
  local function add(text, hl)
    parts[#parts + 1] = '%#' .. hl .. '#' .. esc(text)
    width = width + vim.fn.strdisplaywidth(text)
  end
  if a.state then
    local g = GLYPH[a.state] or { '·', 'WeaverBarFaint' }
    add(g[1] .. ' ' .. (WORD[a.state] or a.state), g[2])
    if a.turn and a.turn ~= 0 then
      add(' · ', 'WeaverBarFaint')
      add('turn ' .. a.turn, 'WeaverBar')
    end
  else
    add('weaver', 'WeaverBarFaint')
  end
  local pending = follow.pending_label()
  if pending then
    add('  → ', 'WeaverBarInk')
    add(pending, 'WeaverBarInk')
    add(' ' .. follow.jump_key_label(), 'WeaverBarFaint')
  elseif not follow.enabled() then
    add('  following off', 'WeaverBarFaint')
  end
  local about = a.state == 'needs-you' and a.waiting or a.title or a.prompt
  if about and about ~= '' then
    local win = vim.g.statusline_winid
    local room = (win and api.nvim_win_is_valid(win) and api.nvim_win_get_width(win) or 80) - width - 4
    about = tostring(about):gsub('%s+', ' ')
    if room >= 8 then
      if vim.fn.strdisplaywidth(about) > room then
        about = vim.fn.strcharpart(about, 0, room - 1) .. '…'
      end
      add(' · ', 'WeaverBarFaint')
      add(about, 'WeaverBarFaint')
    end
  end
  return table.concat(parts) .. '%#WeaverBarFaint#'
end

local EXPR = "%{%v:lua.require'weaver.status'.winbar()%}"

-- Our winbar goes on normal file windows only: not on floats, terminals,
-- quickfix, help, diffs or diffview's tabs.
local function wants_bar(win)
  if not util.plain_window(win) then
    return false
  end
  local tab = api.nvim_win_get_tabpage(win)
  return util.diffview_view(tab) == nil
end

function M.apply(win)
  if not api.nvim_win_is_valid(win) then
    return
  end
  local cur = vim.wo[win].winbar
  if wants_bar(win) then
    if cur == '' then
      vim.wo[win].winbar = EXPR
    end
  elseif cur == EXPR then
    vim.wo[win].winbar = ''
  end
end

function M.apply_tab()
  for _, win in ipairs(api.nvim_tabpage_list_wins(0)) do
    M.apply(win)
  end
end

local redraw_queued = false
function M.redraw()
  if redraw_queued then
    return
  end
  redraw_queued = true
  vim.schedule(function()
    redraw_queued = false
    pcall(vim.cmd, 'redrawstatus!')
  end)
end

function M.setup()
  M.seed()
  set_highlights()
  api.nvim_create_autocmd('ColorScheme', { group = util.group, callback = set_highlights })
  api.nvim_create_autocmd({ 'BufWinEnter', 'WinEnter', 'WinNew', 'TabEnter', 'TermOpen', 'FileType' }, {
    group = util.group,
    callback = function() vim.schedule(M.apply_tab) end,
  })
  api.nvim_create_autocmd('OptionSet', {
    group = util.group,
    pattern = { 'diff', 'buftype', 'winfixbuf' },
    callback = function() vim.schedule(M.apply_tab) end,
  })
  api.nvim_create_autocmd('User', {
    group = util.group,
    pattern = { 'DiffviewViewOpened', 'DiffviewViewEnter', 'DiffviewDiffBufWinEnter' },
    callback = function() vim.schedule(M.apply_tab) end,
  })
  for _, tab in ipairs(api.nvim_list_tabpages()) do
    for _, win in ipairs(api.nvim_tabpage_list_wins(tab)) do
      M.apply(win)
    end
  end
end

function M.teardown()
  local function clear(win)
    if api.nvim_win_is_valid(win) and vim.wo[win].winbar == EXPR then
      vim.wo[win].winbar = ''
    end
  end
  for _, win in ipairs(api.nvim_list_wins()) do
    clear(win)
  end
end

return M
