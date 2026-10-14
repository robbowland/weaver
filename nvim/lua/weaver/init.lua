-- weaver's side of the nvim bound to an agent (see docs/design.md).
--
-- The agent's hooks call WeaverEvent(json) over the --listen socket. It must
-- never block or take input: it returns at once and schedules the work.
local M = {}

local api = vim.api
local util = require('weaver.util')

local modules = { 'status', 'reload', 'follow', 'review' }

-- Handle one event from the hook (see docs/design.md, Hook to Neovim).
function M.dispatch(ev)
  if type(ev) ~= 'table' then
    return
  end
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

local function decode(json)
  local ok, ev = pcall(vim.json.decode, json or '', { luanil = { object = true, array = true } })
  return ok and ev or nil
end

local function define_entry_points()
  function _G.WeaverEvent(json)
    vim.schedule(function() M.dispatch(decode(json)) end)
    return 0
  end
  -- The calls of the previous release, for a hook that predates WeaverEvent.
  function _G.AgentFollow(root, path)
    local ev = { ev = (path and path ~= '') and 'edit' or 'root', root = root or '', file = path }
    vim.schedule(function() M.dispatch(ev) end)
    return 0
  end
  function _G.AgentChecktime()
    vim.schedule(function() M.dispatch({ ev = 'checktime' }) end)
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
  end
  local ok, wk = pcall(require, 'which-key')
  if ok and wk.add then
    pcall(wk.add, { { prefix, group = 'agent' } })
  end
  local leader = vim.g.mapleader == ' ' and '␣' or (vim.g.mapleader or '\\')
  vim.g.weaver_jump_key_label = ((prefix .. 'j'):gsub('<[Ll]eader>', leader))
end

function M.setup()
  define_entry_points()
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
