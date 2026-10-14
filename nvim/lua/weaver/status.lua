-- The agent's state as this nvim knows it, and the bar that shows it: a
-- winbar on normal file windows (state, turn, prompt, and the edit waiting to
-- be followed). Seeded once from the state file, then kept current by the
-- hook's events.
local M = {}

local api = vim.api
local util = require('weaver.util')

-- What this nvim knows of its agent: the state-file fields below, plus
-- sid (session id), log (activity log path), files (FileSet) and tasks
-- ({ subject, done }[]), all optional until the agent reports them.
M.agent = {}

local seq = 0

-- Fields an event or the state file may carry, copied as they are.
local FIELDS = { 'state', 'turn', 'title', 'prompt', 'waiting', 'root', 'agent' }

-- The files the agent edited this session, in order, each once (at most
-- 200, like the state file's list).
local function file_set(list)
  local set = { list = {}, has = {} }
  for _, f in ipairs(type(list) == 'table' and list or {}) do
    if type(f) == 'string' and not set.has[f] then
      set.has[f] = true
      set.list[#set.list + 1] = f
    end
  end
  return set
end

local function add_file(set, f)
  if set.has[f] then
    return
  end
  set.has[f] = true
  set.list[#set.list + 1] = f
  if #set.list > 200 then
    set.has[table.remove(set.list, 1)] = nil
  end
end

local function tasks_of(list)
  local tasks = {}
  for _, t in ipairs(type(list) == 'table' and list or {}) do
    if type(t) == 'table' and type(t.subject) == 'string' then
      tasks[#tasks + 1] = { subject = t.subject, done = t.done == true }
    end
  end
  return tasks
end

-- Take the state file: the start, and what events don't carry (the log
-- path, the edited files, the task list).
local function take_state_file()
  local st = util.read_json(vim.env.WEAVER_STATE)
  if not st then
    return
  end
  if not seq or seq == 0 then
    for _, k in ipairs(FIELDS) do
      if st[k] ~= nil then
        M.agent[k] = st[k]
      end
    end
  end
  if type(st.session_id) == 'string' and st.session_id ~= '' then
    M.agent.sid = M.agent.sid or st.session_id
  end
  if st.session_id == M.agent.sid then
    M.agent.log = st.log or M.agent.log
    M.agent.files = file_set(st.files)
    M.agent.tasks = tasks_of(st.tasks)
  end
end

-- What the agent's last finished turn changed, for the bar:
-- agent.last_turn = { turn, files, roots = { [root] = files } }, adding up the
-- end snapshots of every worktree the turn touched (one event each).
local function summarise_turn(root, commit, turn)
  util.git_async(root, { 'diff', '--shortstat', commit .. '^', commit }, function(out)
    local files = out and tonumber(out:match('(%d+) files? changed'))
    if not files then
      return
    end
    local last = M.agent.last_turn
    if not last or last.turn ~= turn then
      last = { turn = turn, files = 0, roots = {} }
      M.agent.last_turn = last
    end
    last.files = last.files - (last.roots[root] or 0) + files
    last.roots[root] = files
    M.redraw()
  end)
end

-- At startup on a finished agent: its last turn, from the chain tips in the
-- worktrees that turn touched (the state file's turn_roots, else root).
local function summarise_tips(st)
  local a = M.agent
  if not a.sid then
    return
  end
  local roots = type(st.turn_roots) == 'table' and st.turn_roots or {}
  if #roots == 0 and type(st.root) == 'string' then
    roots = { st.root }
  end
  local format = '%H%x1f%(trailers:key=Weaver-Turn,valueonly,separator=)%x1f%(trailers:key=Weaver-Point,valueonly,separator=)'
  for _, root in ipairs(roots) do
    if type(root) == 'string' and util.is_dir(root) then
      util.git_async(root, { 'log', '-1', '--format=' .. format, 'refs/worktree/weaver/sessions/' .. a.sid, '--' }, function(out)
        local commit, turn, point = (out or ''):match('^(%x+)\31%s*(%d*)%s*\31%s*(%a*)')
        if commit and point == 'end' and tonumber(turn) == a.turn then
          summarise_turn(root, commit, tonumber(turn))
        end
      end)
    end
  end
end

function M.seed()
  take_state_file()
  summarise_tips(util.read_json(vim.env.WEAVER_STATE) or {})
end

-- Take what an event says about the agent. Older events (by seq) never
-- overwrite newer ones.
function M.update(ev)
  if ev.seq ~= 0 and ev.seq < seq then
    return
  end
  seq = ev.seq ~= 0 and ev.seq or seq
  if ev.sid and ev.sid ~= M.agent.sid then
    -- A new session in the agent's pane: nothing of the old one carries over.
    M.agent = { sid = ev.sid }
  end
  for _, k in ipairs(FIELDS) do
    if ev[k] ~= nil then
      M.agent[k] = ev[k]
    end
  end
  if ev.ev == 'state' then
    if ev.state ~= 'needs-you' then
      M.agent.waiting = nil
    end
    take_state_file()
  elseif ev.ev == 'edit' and ev.file then
    M.agent.files = M.agent.files or file_set()
    add_file(M.agent.files, ev.file)
  elseif ev.ev == 'snapshot' and ev.point == 'end' and ev.commit and ev.root then
    summarise_turn(ev.root, ev.commit, ev.turn)
  end
  M.redraw()
end

-- The agent, with the state file re-read (for the log path and files).
function M.refresh_from_file()
  take_state_file()
  return M.agent
end

-- The files the agent edited this session, in order.
function M.files()
  return M.agent.files and M.agent.files.list or {}
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

-- weaver's state cells (the G_* at the top of `weaver`): Micrographics task
-- cells, a mark between faint brackets.
local GLYPH = {
  working = { '/', 'WeaverBar' },
  ['needs-you'] = { '!', 'WeaverBarAlert' },
  done = { 'x', 'WeaverBarOk' },
  idle = { ' ', 'WeaverBarFaint' },
  interrupted = { '-', 'WeaverBar' },
  ended = { '-', 'WeaverBarFaint' },
}
local WORD = { ['needs-you'] = 'needs you' }

local function esc(s)
  return (tostring(s or ''):gsub('%%', '%%%%'):gsub('[%c]', ' '))
end

-- "3/5 tasks" while the agent has a task list, else nil.
local function tasks_label()
  local tasks = M.agent.tasks
  if not tasks or #tasks == 0 then
    return nil
  end
  local done = 0
  for _, t in ipairs(tasks) do
    if t.done then
      done = done + 1
    end
  end
  return done .. '/' .. #tasks .. ' tasks'
end

-- Plain text, for a statusline component (lualine and the like).
function M.text()
  local a = M.agent
  if not a.state then
    return ''
  end
  local parts = { '[' .. (GLYPH[a.state] or { '-' })[1] .. '] ' .. (WORD[a.state] or a.state) }
  if a.turn and a.turn ~= 0 then
    parts[#parts + 1] = 'turn ' .. a.turn
  end
  parts[#parts + 1] = tasks_label()
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
    local g = GLYPH[a.state] or GLYPH.ended
    add('[', 'WeaverBarFaint')
    add(g[1], g[2])
    add(']', 'WeaverBarFaint')
    add(' ' .. (WORD[a.state] or a.state), g[2])
    if a.turn and a.turn ~= 0 then
      add(' · ', 'WeaverBarFaint')
      add('turn ' .. a.turn, 'WeaverBar')
    end
    local tasks = tasks_label()
    local last = a.last_turn
    if last and last.turn == a.turn and a.state ~= 'working' and a.state ~= 'needs-you' then
      -- The turn is over: how much it changed, and the key to review it.
      add(' · ', 'WeaverBarFaint')
      add(last.files .. (last.files == 1 and ' file' or ' files'), 'WeaverBarInk')
      add(' ' .. (vim.g.weaver_review_key_label or '␣at'), 'WeaverBarFaint')
    elseif tasks then
      add(' · ', 'WeaverBarFaint')
      add(tasks, 'WeaverBar')
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

-- For a statusline component: the agent where its bar is not shown (a
-- review, a terminal), so it is never on screen twice.
function M.statusline()
  if vim.wo.winbar == EXPR then
    return ''
  end
  return M.text()
end

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
