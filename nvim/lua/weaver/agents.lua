-- :AgentDiffs: every agent's own changes, the main thread's and each
-- subagent's, in every worktree it changed (docs/design.md, Agent chains),
-- to pick one and review it in the one review view. The view compares two
-- commits, so nothing of another worktree is opened as a file (and no
-- language server starts there), and it follows the agent as it works.
local M = {}

local fn = vim.fn
local util = require('weaver.util')

local RS, US = '\30', '\31'

function M.ref(sid, agent)
  return 'refs/worktree/weaver/agents/' .. sid .. '/' .. agent
end

local function ago(t)
  local d = os.time() - (t or os.time())
  if d < 60 then
    return d .. 's'
  elseif d < 3600 then
    return math.floor(d / 60) .. 'm'
  elseif d < 86400 then
    return math.floor(d / 3600) .. 'h'
  end
  return math.floor(d / 86400) .. 'd'
end

-- What an agent is called: the main thread, else the subagent's task (the
-- Agent call's description), else its type, with its id's start.
local function name_of(agent, info)
  if agent == 'main' then
    return 'main thread'
  end
  local what = info and (info.desc or info.type) or 'subagent'
  return what .. ' · ' .. agent:sub(1, 7)
end

-- The chains in ROOT: { [agent] = { tip, time, stats } }, in two git calls.
local function chains_in(root, sid)
  local out = util.git(root, {
    'for-each-ref',
    '--format=%(refname:lstrip=5)' .. '%1f' .. '%(objectname)',
    'refs/worktree/weaver/agents/' .. sid .. '/',
  })
  local by, by_tip, tips = {}, {}, {}
  for line in (out or ''):gmatch('[^\n]+') do
    local agent, tip = line:match('^([^\31]+)\31(%x+)$')
    if agent then
      by[agent] = { tip = tip, stats = { add = 0, del = 0, files = 0 } }
      by_tip[tip] = by[agent]
      tips[#tips + 1] = tip
    end
  end
  if #tips == 0 then
    return by
  end
  -- Each tip against its parent, the chain's start.
  local log = util.git(root, vim.list_extend({ 'log', '--no-walk=unsorted', '--format=' .. RS .. '%H' .. US .. '%ct', '--numstat' }, tips))
  for rec in (log or ''):gmatch('[^' .. RS .. ']+') do
    local sha, t = rec:match('^(%x+)' .. US .. '(%d+)')
    local c = sha and by_tip[sha]
    if c then
      c.time = tonumber(t)
      for a, d in rec:gmatch('\n(%S+)\t(%S+)\t') do
        c.stats.files = c.stats.files + 1
        c.stats.add = c.stats.add + (tonumber(a) or 0)
        c.stats.del = c.stats.del + (tonumber(d) or 0)
      end
    end
  end
  return by
end

-- One item per agent and worktree it changed, the running and the latest
-- first: { agent, root, ref, commit, time, stats, name, running, text }.
function M.items()
  local status = require('weaver.status')
  local sid = status.refresh_from_file().sid
  if not sid then
    return nil, 'no agent session is bound to this nvim'
  end
  local idx = require('weaver.log').agents()
  local roots, seen = {}, {}
  for _, c in ipairs(idx.chains) do
    if not seen[c.root] and util.is_dir(c.root) then
      seen[c.root] = true
      roots[#roots + 1] = c.root
    end
  end
  local items = {}
  for _, root in ipairs(roots) do
    for agent, c in pairs(chains_in(root, sid)) do
      if c.stats.files > 0 then
        local info = idx.info[agent]
        local it = {
          agent = agent,
          root = root,
          cwd = root,
          ref = M.ref(sid, agent),
          commit = c.tip,
          time = c.time,
          stats = c.stats,
          name = name_of(agent, info),
          running = agent == 'main' and status.agent.state == 'working' or (info and info.running) or false,
        }
        it.text = it.name .. ' ' .. fn.fnamemodify(root, ':t')
        items[#items + 1] = it
      end
    end
  end
  table.sort(items, function(a, b)
    if a.running ~= b.running then
      return a.running
    end
    if (a.time or 0) ~= (b.time or 0) then
      return (a.time or 0) > (b.time or 0)
    end
    return a.text < b.text
  end)
  return items
end

function M.label(it)
  return ('%s %-40s %-20s +%d -%d  %d file%s  %s'):format(
    it.running and '●' or ' ',
    it.name,
    fn.fnamemodify(it.root, ':t'),
    it.stats.add,
    it.stats.del,
    it.stats.files,
    it.stats.files == 1 and '' or 's',
    ago(it.time)
  )
end

-- :AgentDiffs
function M.pick()
  local items, why = M.items()
  if not items then
    util.msg(why, util.WARN)
    return
  end
  if #items == 0 then
    util.msg('no agent has changed anything yet')
    return
  end
  local function open(it)
    if it then
      require('weaver.review').open_agent(it)
    end
  end
  if type(_G.Snacks) == 'table' and _G.Snacks.picker ~= nil then
    _G.Snacks.picker({
      title = "Agents' changes",
      items = items,
      format = function(it)
        return {
          { it.running and '● ' or '  ', 'DiagnosticWarn' },
          { ('%-40s '):format(it.name), 'Normal' },
          { ('%-20s '):format(fn.fnamemodify(it.root, ':t')), 'Directory' },
          { ('+%d'):format(it.stats.add), 'DiagnosticOk' },
          { (' -%d  '):format(it.stats.del), 'DiagnosticError' },
          { ('%d file%s  %s'):format(it.stats.files, it.stats.files == 1 and '' or 's', ago(it.time)), 'Comment' },
        }
      end,
      -- The tip against its parent: the agent's diff.
      preview = 'git_show',
      confirm = function(picker, it)
        picker:close()
        vim.schedule(function() open(it) end)
      end,
    })
    return
  end
  vim.ui.select(items, { prompt = "Agents' changes", format_item = M.label }, open)
end

return M
