-- weaver's side of the nvim bound to an agent (see docs/design.md).
--
-- The agent's hooks call WeaverEvent(json) over the --listen socket. It must
-- never block or take input: it returns at once and schedules the work.
local M = {}

local api = vim.api
local util = require('weaver.util')

local modules = { 'status', 'reload', 'follow', 'review' }

local STRING_FIELDS = {
  'sid', 'root', 'file', 'tool', 'agent_id', 'prompt', 'waiting', 'title', 'ref', 'commit', 'point', 'agent',
}
local STATES = { working = true, ['needs-you'] = true, done = true, idle = true, interrupted = true, ended = true }

-- A hook event (docs/design.md, Hook to Neovim) from its JSON: the contract's
-- fields with the right types (a field of the wrong type is dropped), seq a
-- number (0 when absent), anything else passed through. nil if it is not an
-- event at all.
function M.parse_event(json)
  local ok, raw = pcall(vim.json.decode, json or '', { luanil = { object = true, array = true } })
  if not ok or type(raw) ~= 'table' or type(raw.ev) ~= 'string' then
    return nil
  end
  local ev = raw
  for _, k in ipairs(STRING_FIELDS) do
    if type(ev[k]) ~= 'string' or ev[k] == '' then
      ev[k] = nil
    end
  end
  ev.seq = tonumber(ev.seq) or 0
  ev.turn = tonumber(ev.turn)
  ev.state = STATES[ev.state] and ev.state or nil
  return ev
end

-- Handle one parsed event.
function M.dispatch(ev)
  local status = require('weaver.status')
  status.update(ev)
  local kind = ev.ev
  if kind == 'edit' or kind == 'checktime' or kind == 'snapshot' then
    require('weaver.reload').checktime()
  end
  if kind == 'edit' or kind == 'root' then
    require('weaver.follow').on_event(ev)
  elseif kind == 'focus' then
    require('weaver.follow').on_focus(ev)
  end
  if kind == 'root' then
    require('weaver.follow').prune_tabs()
  end
  if kind == 'edit' or kind == 'checktime' or kind == 'snapshot' then
    require('weaver.review').poke()
  end
  require('weaver.log').refresh()
  pcall(api.nvim_exec_autocmds, 'User', { pattern = 'WeaverEvent', data = ev, modeline = false })
end

local function define_entry_points()
  function _G.WeaverEvent(json)
    vim.schedule(function()
      local ev = M.parse_event(json)
      if ev then
        M.dispatch(ev)
      end
    end)
    return 0
  end
  -- The calls of the previous release, for a hook that predates WeaverEvent.
  function _G.AgentFollow(root, path)
    local ev = M.parse_event(vim.json.encode({ ev = (path and path ~= '') and 'edit' or 'root', root = root, file = path }))
    vim.schedule(function() M.dispatch(ev) end)
    return 0
  end
  function _G.AgentChecktime()
    vim.schedule(function() M.dispatch({ ev = 'checktime', seq = 0 }) end)
    return 0
  end
end

local function complete_scope(lead)
  return vim.tbl_filter(function(s) return s:find(lead, 1, true) == 1 end, require('weaver.review').SCOPES)
end

local function commands()
  local cmd = api.nvim_create_user_command
  cmd('AgentFollow', function() require('weaver.follow').toggle() end, {
    desc = 'weaver: toggle following the agent',
  })
  cmd('AgentJump', function() require('weaver.follow').jump() end, {
    desc = "weaver: go to the agent's latest edit now",
  })
  cmd('AgentReview', function(o) require('weaver.review').open(o.args ~= '' and o.args or 'branch') end, {
    nargs = '?',
    complete = complete_scope,
    desc = 'weaver: review a scope in diffview (turn, uncommitted, branch, reviewed, mine)',
  })
  cmd('AgentReviewed', function() require('weaver.review').mark_reviewed() end, {
    desc = 'weaver: mark the worktree reviewed as it is now',
  })
  cmd('AgentChanges', function(o) require('weaver.review').changes(o.args ~= '' and o.args or 'branch') end, {
    nargs = '?',
    complete = complete_scope,
    desc = 'weaver: every hunk of a scope in quickfix',
  })
  cmd('AgentTurns', function() require('weaver.turns').pick() end, {
    desc = "weaver: pick one of the agent's turns and review its diff",
  })
  cmd('AgentLog', function() require('weaver.log').open() end, {
    desc = "weaver: the agent's activity log",
  })
  cmd('AgentWhy', function(o) require('weaver.turns').why_cmd(o.bang) end, {
    bang = true,
    desc = 'weaver: which turn and prompt last changed this line (! opens that turn)',
  })
  cmd('AgentNote', function(o) require('weaver.notes').add(o.line1, o.line2, o.args ~= '' and o.args or nil) end, {
    range = true,
    nargs = '?',
    desc = 'weaver: note these lines for the agent (the note, else asked for)',
  })
  cmd('AgentSend', function(o) require('weaver.notes').send(o.bang) end, {
    bang = true,
    desc = 'weaver: send your notes to the agent (! even while it works)',
  })
  cmd('AgentNotes', function() require('weaver.notes').list() end, {
    desc = 'weaver: your notes in the quickfix list',
  })
  cmd('AgentNotesClear', function() require('weaver.notes').clear() end, {
    desc = 'weaver: drop your notes',
  })
end

M.KEYS = {
  { 'j', 'AgentJump', "Jump to the agent's edit" },
  { 'f', 'AgentFollow', 'Toggle following' },
  { 't', 'AgentReview turn', 'Review last turn' },
  { 'u', 'AgentReview uncommitted', 'Review uncommitted' },
  { 'b', 'AgentReview branch', 'Review branch' },
  { 'r', 'AgentReview reviewed', 'Review since reviewed' },
  { 'm', 'AgentReview mine', "Review the agent's files" },
  { 'R', 'AgentReviewed', 'Mark reviewed' },
  { 'T', 'AgentTurns', 'Pick a turn' },
  { 'l', 'AgentLog', 'Activity log' },
  { 'w', 'AgentWhy', 'Why is this line here' },
  { 'c', 'AgentChanges turn', 'Last turn hunks in quickfix' },
  { 'n', 'AgentNote', 'Note this line for the agent' },
  { 's', 'AgentSend', 'Send your notes to the agent' },
}

-- <leader>a… unless you have mapped those keys yourself.
local function keys()
  local prefix = vim.g.weaver_key_prefix or '<leader>a'
  if prefix == '' then
    return
  end
  for _, k in ipairs(M.KEYS) do
    local lhs = prefix .. k[1]
    if vim.fn.maparg(lhs, 'n') == '' then
      vim.keymap.set('n', lhs, '<cmd>' .. k[2] .. '<cr>', { desc = 'weaver: ' .. k[3] })
    end
    -- A note on the lines you selected.
    if k[2] == 'AgentNote' and vim.fn.maparg(lhs, 'x') == '' then
      vim.keymap.set('x', lhs, ':AgentNote<cr>', { desc = 'weaver: Note these lines for the agent', silent = true })
    end
  end
  local ok, wk = pcall(require, 'which-key')
  if ok and wk.add then
    pcall(wk.add, { { prefix, group = 'agent' } })
  end
  local leader = vim.g.mapleader == ' ' and '␣' or (vim.g.mapleader or '\\')
  vim.g.weaver_jump_key_label = ((prefix .. 'j'):gsub('<[Ll]eader>', leader))
  vim.g.weaver_review_key_label = ((prefix .. 't'):gsub('<[Ll]eader>', leader))
end

-- No UI left (tmux closed the pane: a crash, quit or close) means nobody
-- can answer a prompt, yet an error on the way out (most often a ShaDa file
-- that several nvims, all told to go at once, race to write) makes nvim wait
-- for a key, for good. So with no UI: write ShaDa with its errors silenced,
-- then skip nvim's own write; and once the exit autocommands have run, end.
local function orphaned()
  return #api.nvim_list_uis() == 0 and not vim.tbl_contains(vim.v.argv, '--headless')
end

local function exit_guard()
  api.nvim_create_autocmd('VimLeavePre', {
    group = util.group,
    callback = function()
      if orphaned() then
        pcall(vim.cmd, 'silent! wshada')
        vim.o.shada = ''
      end
    end,
  })
  api.nvim_create_autocmd('VimLeave', {
    group = util.group,
    callback = function()
      if orphaned() then
        os.exit(tonumber(vim.v.exiting) or 1)
      end
    end,
  })
end

function M.setup()
  define_entry_points()
  exit_guard()
  for _, name in ipairs(modules) do
    require('weaver.' .. name).setup()
  end
  commands()
  keys()
  -- `weaver nvim` opened the startup file read-only if it had a swap file:
  -- say so, and stop overriding the swap dialog for your own edits.
  pcall(api.nvim_del_augroup_by_name, 'weaver_start')
  if vim.g.weaver_swap then
    local swapped = vim.g.weaver_swap
    vim.g.weaver_swap = nil
    vim.schedule(function() util.msg('swap file exists: opened ' .. util.short(swapped) .. ' read-only', util.WARN) end)
  end
end

function M.teardown()
  for _, name in ipairs(modules) do
    local mod = package.loaded['weaver.' .. name]
    if mod and mod.teardown then
      pcall(mod.teardown)
    end
  end
  pcall(api.nvim_del_augroup_by_name, 'weaver')
end

return M
