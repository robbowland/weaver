-- The agent's review scopes, shown in the one review view of weaver.diffview.
-- The agent's edits refresh the view (debounced), so it never goes stale.
--
--   turn         the agent's latest turn (live while it works)
--   uncommitted  the worktree against the index (and staged changes)
--   branch       the whole branch, committed and not, against its base
--   reviewed     everything since you last marked the worktree reviewed
--   mine         only the files this agent changed, since it started here
local M = {}

local fn = vim.fn
local util = require('weaver.util')
local turns = require('weaver.turns')
local dv = require('weaver.diffview')

M.SCOPES = { 'turn', 'uncommitted', 'branch', 'reviewed', 'mine' }
M.REVIEWED_REF = 'refs/worktree/weaver/reviewed'

local function verify(root, rev)
  return util.first_line(util.git(root, { 'rev-parse', '--verify', '--quiet', rev .. '^{commit}' }))
end

-- What the branch is compared against: $WEAVER_BASE, else
-- `git config branch.<branch>.weaverbase`, else the remote's default branch.
function M.base(root)
  local env = vim.env.WEAVER_BASE
  if env and env ~= '' and verify(root, env) then
    return env
  end
  local branch = util.first_line(util.git(root, { 'symbolic-ref', '--quiet', '--short', 'HEAD' }))
  local set = branch and util.first_line(util.git(root, { 'config', 'branch.' .. branch .. '.weaverbase' }))
  if set and verify(root, set) then
    return set
  end
  return dv.default_base(root)
end

local function rel_paths_under(root, files)
  local out = {}
  for _, f in ipairs(files or {}) do
    local r = util.real(f) or f
    if r ~= root and util.under(r, root) then
      out[#out + 1] = r:sub(#root + 2)
    end
  end
  return out
end

local function need_sid()
  local sid = require('weaver.status').agent.sid
  if not sid then
    return nil, 'no agent session is bound to this nvim'
  end
  return sid
end

-- Each scope resolves, in ROOT, to a weaver.diffview spec, or nil and why.
local resolvers = {}

function resolvers.uncommitted(root)
  return { label = 'uncommitted', compare = { kind = 'index' } }
end

function resolvers.branch(root)
  local base = M.base(root)
  if not base then
    return nil, 'no base branch found; set WEAVER_BASE or git config branch.<name>.weaverbase'
  end
  return { label = 'branch vs ' .. base, compare = { kind = 'branch', base = base } }
end

function resolvers.turn(root)
  local sid, why = need_sid()
  if not sid then
    return nil, why
  end
  local t
  t, why = turns.turn_base(root, sid, require('weaver.status').agent)
  if not t then
    return nil, why
  end
  return { label = t.label, compare = { kind = 'worktree', base = t.base } }
end

function resolvers.reviewed(root)
  local mark = verify(root, M.REVIEWED_REF)
  if mark then
    local when = tonumber(util.first_line(util.git(root, { 'log', '-1', '--format=%ct', mark })) or '')
    return { label = 'since reviewed' .. (when and os.date(' %H:%M', when) or ''), compare = { kind = 'worktree', base = mark } }
  end
  local sid = need_sid()
  local start = sid and turns.session_base(root, sid)
  if not start then
    return nil, 'nothing marked reviewed in ' .. fn.fnamemodify(root, ':t') .. ' yet (:AgentReviewed marks it)'
  end
  return { label = 'since the agent started (nothing marked reviewed)', compare = { kind = 'worktree', base = start } }
end

function resolvers.mine(root)
  local sid, why = need_sid()
  if not sid then
    return nil, why
  end
  local paths, seen = turns.agent_paths(root, sid)
  local status = require('weaver.status')
  status.refresh_from_file()
  for _, p in ipairs(rel_paths_under(root, status.files())) do
    if not seen[p] then
      seen[p] = true
      paths[#paths + 1] = p
    end
  end
  if #paths == 0 then
    return nil, 'the agent has not changed any files in ' .. fn.fnamemodify(root, ':t')
  end
  table.sort(paths)
  local start = turns.session_base(root, sid) or 'HEAD'
  return { label = 'agent files', compare = { kind = 'worktree', base = start }, paths = paths }
end

-- SCOPE in ROOT as a weaver.diffview spec, or nil and why.
function M.resolve(scope, root)
  local resolve = resolvers[scope]
  if not resolve then
    return nil, 'unknown scope ' .. tostring(scope) .. ' (' .. table.concat(M.SCOPES, ', ') .. ')'
  end
  local spec, why = resolve(root)
  if not spec then
    return nil, why
  end
  spec.key, spec.root = scope, root
  return spec
end

local function here_spec(scope)
  local root = dv.root_here()
  if not root then
    return nil, 'not in a git repository'
  end
  return M.resolve(scope, root)
end

-- :AgentReview [scope]
function M.open(scope)
  local spec, why = here_spec(scope)
  if not spec then
    util.msg(why, util.WARN)
    return
  end
  if not dv.available() then
    util.msg('diffview is not installed; listing the changes in quickfix instead', util.WARN)
    return M.changes(scope)
  end
  return dv.show(spec)
end

-- One turn's own diff, from :AgentTurns, :AgentLog or :AgentWhy!.
function M.open_turn(root, turn)
  return dv.show({
    key = 'turn ' .. (turn.turn or '?'),
    root = root,
    label = 'turn ' .. (turn.turn or '?') .. ' only',
    compare = { kind = 'commits', from = turn.parent, to = turn.commit },
  })
end

-- :AgentChanges [scope]: every hunk of a scope in quickfix, to step through
-- in your own buffers.
function M.changes(scope)
  local spec, why = here_spec(scope)
  if not spec then
    util.msg(why, util.WARN)
    return
  end
  local items = dv.hunks(spec)
  fn.setqflist({}, ' ', { title = 'AgentChanges: ' .. spec.label, items = items })
  if #items == 0 then
    util.msg('no changes: ' .. spec.label)
  else
    vim.cmd('botright copen')
  end
end

-- :AgentReviewed
function M.mark_reviewed()
  local root = dv.root_here()
  local bin = util.weaver_bin()
  if not root or not bin then
    util.msg(root and 'cannot find weaver' or 'not in a git repository', util.WARN)
    return
  end
  vim.system({ bin, 'snapshot', '--reviewed', root }, { text = true }, function(r)
    vim.schedule(function()
      if r.code ~= 0 then
        util.msg('could not mark reviewed: ' .. vim.trim(r.stderr or ''), util.WARN)
        return
      end
      util.msg('marked ' .. fn.fnamemodify(root, ':t') .. ' reviewed')
      local cur = dv.current()
      if cur and (vim.t[cur.tabpage].diffview_scope or ''):match('^reviewed%z') then
        local spec = M.resolve('reviewed', root)
        if spec then
          dv.show(spec)
        end
      end
    end)
  end)
end

-- The agent changed something: refresh the view, at most every 250ms.
local poke_queued = false
function M.poke()
  if poke_queued then
    return
  end
  poke_queued = true
  vim.defer_fn(function()
    poke_queued = false
    dv.refresh()
  end, 250)
end

function M.setup()
  dv.setup()
end

return M
