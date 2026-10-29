-- The agent's per-turn snapshot chain (see docs/design.md, Snapshots):
-- reading it, :AgentTurns to pick one turn's diff, and :AgentWhy to trace a
-- line back to the turn and prompt that last changed it.
local M = {}

local api, fn = vim.api, vim.fn
local util = require('weaver.util')

local US, RS = '\31', '\30'

function M.ref(sid)
  return 'refs/worktree/weaver/sessions/' .. sid
end

local function sid_or_warn()
  local sid = require('weaver.status').agent.sid
  if not sid then
    util.msg('no agent session is bound to this nvim', util.WARN)
    return nil
  end
  return sid
end

local function trailer(key)
  return '%(trailers:key=' .. key .. ',valueonly,separator=%x2c)'
end

local FORMAT = table.concat({
  '%H',
  '%P',
  '%ct',
  trailer('Weaver-Turn'),
  trailer('Weaver-Point'),
  '%s',
}, '%x1f') .. '%x1e'

-- The prompt from a snapshot subject: "weaver: turn N end: <prompt>".
local function subject_prompt(s)
  return s:match('^weaver: turn %d+ %a+: (.*)$') or s
end

local function parse(out)
  local list = {}
  for rec in (out or ''):gmatch('[^' .. RS .. ']+') do
    rec = rec:gsub('^%s+', '')
    local f = vim.split(rec, US, { plain = true })
    if #f >= 6 and f[1] ~= '' then
      list[#list + 1] = {
        sha = f[1],
        parent = f[2]:match('^(%x+)'),
        time = tonumber(f[3]),
        turn = tonumber(vim.trim(f[4])),
        point = vim.trim(f[5]),
        prompt = subject_prompt(vim.trim(f[6])),
      }
    end
  end
  return list
end

-- The chain in ROOT for session SID, newest first (at most LIMIT).
function M.chain(root, sid, limit)
  local out = util.git(root, {
    'log',
    '--first-parent',
    '-n',
    tostring(limit or 200),
    '--format=' .. FORMAT,
    M.ref(sid),
    '--',
  })
  return parse(out)
end

-- The base of the agent's latest turn in ROOT, for the review "turn" scope:
-- while it works, the tip (the start of this turn); after, the parent of the
-- last end snapshot. Returns { base, label } or nil and why.
function M.turn_base(root, sid, agent)
  local chain = M.chain(root, sid, 100)
  if #chain == 0 then
    return nil, 'no snapshots of this agent in ' .. fn.fnamemodify(root, ':t') .. ' yet'
  end
  if agent.state == 'working' or agent.state == 'needs-you' then
    local turn = agent.turn or chain[1].turn
    return { base = chain[1].sha, label = 'turn ' .. (turn or '?') .. ' so far' }
  end
  for _, c in ipairs(chain) do
    if c.point == 'end' then
      return { base = c.parent or 'HEAD', label = 'turn ' .. (c.turn or '?'), turn = c.turn }
    end
  end
  return { base = chain[1].sha, label = 'since turn ' .. (chain[1].turn or '?') }
end

-- The oldest snapshot of the session in ROOT (where it started there).
function M.session_base(root, sid)
  local out = util.git(root, { 'rev-list', '--max-parents=0', '--first-parent', M.ref(sid), '--' })
  return util.first_line(out)
end

-- Paths (relative to ROOT) changed in any of the agent's turns there.
function M.agent_paths(root, sid)
  local ends = {}
  for _, c in ipairs(M.chain(root, sid, 500)) do
    if c.point == 'end' and c.parent then
      ends[#ends + 1] = c.sha
    end
  end
  local seen, paths = {}, {}
  if #ends > 0 then
    local out = util.git(root, { 'diff-tree', '--stdin', '-r', '--name-only', '-z', '--no-commit-id' }, {
      stdin = table.concat(ends, '\n') .. '\n',
    })
    for _, p in ipairs(util.nul_list(out)) do
      if not seen[p] then
        seen[p] = true
        paths[#paths + 1] = p
      end
    end
  end
  return paths, seen
end

-- :AgentTurns -----------------------------------------------------------------

local function ago(t)
  if not t then
    return ''
  end
  local d = os.time() - t
  if d < 60 then
    return d .. 's'
  elseif d < 3600 then
    return math.floor(d / 60) .. 'm'
  elseif d < 86400 then
    return math.floor(d / 3600) .. 'h'
  end
  return math.floor(d / 86400) .. 'd'
end

-- One item per turn that changed something in ROOT, newest first.
function M.items(root, sid)
  local items = {}
  local shas = {}
  for _, c in ipairs(M.chain(root, sid, 500)) do
    if c.point == 'end' and c.parent then
      items[#items + 1] = {
        text = ('turn %s %s'):format(c.turn or '?', c.prompt),
        commit = c.sha,
        parent = c.parent,
        turn = c.turn,
        prompt = c.prompt,
        time = c.time,
        cwd = root,
        stats = { add = 0, del = 0, files = 0 },
      }
      shas[#shas + 1] = c.sha
    end
  end
  if #shas > 0 then
    -- One git call for every turn's numbers.
    local out = util.git(root, vim.list_extend({ 'log', '--no-walk=unsorted', '--format=%x1e%H', '--numstat' }, shas))
    local by = {}
    for _, it in ipairs(items) do
      by[it.commit] = it.stats
    end
    for rec in (out or ''):gmatch('[^' .. RS .. ']+') do
      local sha = rec:match('^(%x+)')
      local st = sha and by[sha]
      if st then
        for a, d in rec:gmatch('\n(%S+)\t(%S+)\t') do
          st.files = st.files + 1
          st.add = st.add + (tonumber(a) or 0)
          st.del = st.del + (tonumber(d) or 0)
        end
      end
    end
  end
  return items
end

function M.label(it)
  return ('turn %-3s %4s  +%d -%d  %d file%s  %s'):format(
    it.turn or '?',
    ago(it.time),
    it.stats.add,
    it.stats.del,
    it.stats.files,
    it.stats.files == 1 and '' or 's',
    it.prompt
  )
end

function M.pick()
  local sid = sid_or_warn()
  local root = sid and require('weaver.review').root()
  if not root then
    if sid then
      util.msg('not in a git repository', util.WARN)
    end
    return
  end
  local items = M.items(root, sid)
  if #items == 0 then
    util.msg('no turns with changes in ' .. fn.fnamemodify(root, ':t') .. ' yet')
    return
  end
  local function open(it)
    if it then
      require('weaver.review').open_turn(root, it)
    end
  end
  local ok_snacks = type(_G.Snacks) == 'table' and _G.Snacks.picker ~= nil
  if ok_snacks then
    _G.Snacks.picker({
      title = 'Agent turns · ' .. fn.fnamemodify(root, ':t'),
      items = items,
      format = function(it)
        return {
          { ('turn %-3s'):format(it.turn or '?'), 'Normal' },
          { ('%4s  '):format(ago(it.time)), 'Comment' },
          { ('+%d'):format(it.stats.add), 'DiagnosticOk' },
          { (' -%d  '):format(it.stats.del), 'DiagnosticError' },
          { it.prompt, 'Comment' },
        }
      end,
      preview = 'git_show',
      confirm = function(picker, it)
        picker:close()
        vim.schedule(function() open(it) end)
      end,
    })
    return
  end
  vim.ui.select(items, { prompt = 'Agent turns', format_item = M.label }, open)
end

-- :AgentWhy -----------------------------------------------------------------

local ZERO = string.rep('0', 40)

-- The body of a snapshot commit without its trailer block: the full prompt.
-- The trailers are the last paragraph, and only when every line of it is a
-- "Key: value" trailer, so a prompt that ends "Note: keep it short" keeps it.
local function prompt_of(root, sha)
  local body = vim.trim(util.git(root, { 'log', '-1', '--format=%b', sha }) or '')
  local paragraphs = vim.split(body, '\n%s*\n')
  local last = paragraphs[#paragraphs]
  local is_trailers = #paragraphs > 1 and last ~= ''
  for line in (last or ''):gmatch('[^\n]+') do
    if not line:match('^[%w-]+: ') then
      is_trailers = false
    end
  end
  if is_trailers then
    paragraphs[#paragraphs] = nil
  end
  return vim.trim(table.concat(paragraphs, '\n\n'))
end

-- What last changed line LNUM of BUF. One of
--   { kind = 'unknown', reason }             it cannot be said (and why)
--   { kind = 'unsaved', line }               after the last snapshot
--   { kind = 'turn', line, turn, prompt, time, root, sha, parent }
--   { kind = 'between', line, turn }         between turns, before TURN
--   { kind = 'before', line }                before the agent started here
function M.why(buf, lnum)
  local function unknown(reason)
    return { kind = 'unknown', reason = reason }
  end
  local sid = require('weaver.status').agent.sid
  if not sid then
    return unknown('no agent session is bound to this nvim')
  end
  local path = api.nvim_buf_get_name(buf)
  if path == '' or vim.bo[buf].buftype ~= '' then
    return unknown('not a file')
  end
  local root = util.toplevel(fn.fnamemodify(path, ':p:h'))
  if not root then
    return unknown('not in a git repository')
  end
  local tip = util.first_line(util.git(root, { 'rev-parse', '--verify', '--quiet', M.ref(sid) }))
  if not tip then
    return unknown('no snapshots of this agent in ' .. fn.fnamemodify(root, ':t') .. ' yet')
  end
  local lines = api.nvim_buf_get_lines(buf, 0, -1, false)
  local out, err = util.git(root, {
    'blame',
    '--porcelain',
    '-L',
    lnum .. ',' .. lnum,
    '--contents',
    '-',
    tip,
    '--',
    util.real(path):sub(#root + 2),
  }, { stdin = table.concat(lines, '\n') .. '\n' })
  local sha = out and out:match('^(%x+)')
  if not sha then
    return unknown('git blame failed: ' .. vim.trim(err or ''))
  end
  if sha == ZERO then
    return { kind = 'unsaved', line = lnum }
  end
  local info = util.git(root, { 'log', '-1', '--format=%P%x1f' .. trailer('Weaver-Turn') .. '%x1f' .. trailer('Weaver-Point') .. '%x1f%ct', sha })
  local f = vim.split(vim.trim(info or ''), US, { plain = true })
  local parent, turn, point = (f[1] or ''):match('^(%x+)'), tonumber(vim.trim(f[2] or '')), vim.trim(f[3] or '')
  if point == 'end' and parent then
    return {
      kind = 'turn',
      line = lnum,
      turn = turn,
      prompt = prompt_of(root, sha),
      time = tonumber(f[4]),
      root = root,
      sha = sha,
      parent = parent,
    }
  end
  if not parent then
    return { kind = 'before', line = lnum }
  end
  return { kind = 'between', line = lnum, turn = turn }
end

local DESCRIBE = {
  unknown = function(w) return w.reason end,
  unsaved = function(w) return 'line ' .. w.line .. ': changed after the last snapshot (the turn in progress, or your own edit)' end,
  turn = function(w) return ('line %d: turn %s, %s ago\n%s'):format(w.line, w.turn or '?', ago(w.time), w.prompt) end,
  between = function(w)
    return ('line %d: changed between turns, before turn %s (your edit, or another agent)'):format(w.line, w.turn or '?')
  end,
  before = function(w) return 'line ' .. w.line .. ': unchanged since the agent started here' end,
}

function M.describe(w)
  return DESCRIBE[w.kind](w)
end

-- :AgentWhy[!]: say what last changed the cursor's line; with !, open that
-- turn's diff.
function M.why_cmd(bang)
  local w = M.why(api.nvim_get_current_buf(), api.nvim_win_get_cursor(0)[1])
  if bang and w.kind == 'turn' then
    require('weaver.review').open_turn(w.root, { commit = w.sha, parent = w.parent, turn = w.turn })
    return
  end
  util.msg(M.describe(w), w.kind == 'unknown' and util.WARN or util.INFO)
end

return M
