-- :AgentDiffs at its public boundaries: agent chains and the activity log
-- in (as the engine writes them, docs/design.md, Agent chains), the picker's
-- items and diffview's file panel out. Needs diffview-plus.
local T = dofile(vim.fn.fnamemodify(debug.getinfo(1, 'S').source:sub(2), ':h') .. '/helper.lua')

T.run(function()
  if not T.has_diffview() then
    T.skip("agents' changes", 'diffview-plus.nvim not found (set WEAVER_TEST_PLUGINS)')
    return
  end
  local R = T.repo()
  local sid = 'sid-agents'
  local state = R.base .. '/state.json'
  local log = R.base .. '/log.jsonl'
  T.write(state, vim.json.encode({ session_id = sid, state = 'working', turn = 1, log = log }))

  -- A chain as the engine keeps it: start, the worktree as it is; the tip on
  -- start, with the agent's files taken in.
  local function ref(agent) return 'refs/worktree/weaver/agents/' .. sid .. '/' .. agent end
  local function chain(root, agent)
    local gitdir = vim.trim(T.git(root, { 'rev-parse', '--path-format=absolute', '--git-path', 'weaver' }))
    vim.fn.mkdir(gitdir, 'p')
    local env = { GIT_INDEX_FILE = gitdir .. '/index-' .. sid .. '-' .. agent }
    local ident = {
      GIT_INDEX_FILE = env.GIT_INDEX_FILE,
      GIT_AUTHOR_NAME = 'weaver',
      GIT_AUTHOR_EMAIL = 'weaver@localhost',
      GIT_COMMITTER_NAME = 'weaver',
      GIT_COMMITTER_EMAIL = 'weaver@localhost',
    }
    T.git(root, { 'add', '--all' }, { env = env })
    local tree = vim.trim(T.git(root, { 'write-tree' }, { env = env }))
    local start = vim.trim(T.git(root, { 'commit-tree', tree, '-m', 'weaver: agent ' .. agent .. ' start' }, { env = ident }))
    T.git(root, { 'update-ref', ref(agent), start })
    return function(paths)
      T.git(root, vim.list_extend({ 'add', '--all', '--' }, paths), { env = env })
      tree = vim.trim(T.git(root, { 'write-tree' }, { env = env }))
      local tip = vim.trim(T.git(root, { 'commit-tree', tree, '-p', start, '-m', 'weaver: agent ' .. agent }, { env = ident }))
      T.git(root, { 'update-ref', ref(agent), tip })
      return tip
    end
  end
  local lines = {}
  local function logged(e)
    e.t, e.pane, e.turn = os.time(), '1', 1
    lines[#lines + 1] = vim.json.encode(e)
    T.write(log, lines)
  end

  -- A subagent in worktree a: a new file and a change; your README edit
  -- there is not its.
  logged({ ev = 'subagent', agent_id = 'sub-a', agent_type = 'general-purpose', phase = 'start' })
  local sub_a = chain(R.a, 'sub-a')
  T.write(R.a .. '/src/token.txt', { 'token' })
  T.write(R.a .. '/src/app.txt', { 'line one', 'line two, by sub-a', 'line three' })
  local tip = sub_a({ 'src/token.txt', 'src/app.txt' })
  logged({ ev = 'snapshot', point = 'agent', agent_id = 'sub-a', root = R.a, commit = tip })
  logged({ ev = 'agent', agent_id = 'sub-a', agent_type = 'general-purpose', desc = 'write the token' })
  T.write(R.a .. '/README.md', { 'readme, edited by you' })
  -- The main thread in the main checkout.
  local main = chain(R.main, 'main')
  T.write(R.main .. '/src/app.txt', { 'line one', 'line two, by main', 'line three' })
  logged({ ev = 'snapshot', point = 'agent', agent_id = 'main', root = R.main, commit = main({ 'src/app.txt' }) })

  local C = T.child({ WEAVER_STATE = state, WEAVER_BIN = T.root .. '/test/nvim/fake-weaver' })
  C.cmd('cd ' .. R.main)
  C.cmd('edit ' .. R.main .. '/README.md')

  local items = C.lua([[
    local out = {}
    for _, it in ipairs(require('weaver.agents').items()) do
      out[#out + 1] = ('%s|%s|%d|%s'):format(it.name, vim.fn.fnamemodify(it.root, ':t'), it.stats.files, tostring(it.running))
    end
    table.sort(out)
    return out
  ]])
  T.ok("every agent's changes, by worktree, named by its task", items, {
    'main thread|repo|1|true',
    'write the token · sub-a|wt-a|2|true',
  })

  -- What the file panel lists: "<status> <name>" per file, sorted.
  local function listed()
    local out = {}
    for _, line in ipairs(vim.split(C.panel(), '\n')) do
      local st, name = line:match('^%s*([MADRC?])%s+(%S+%.%w+)')
      if st then
        out[#out + 1] = st .. ' ' .. name
      end
    end
    table.sort(out)
    return out
  end
  local function shows(want)
    T.wait(5000, function() return vim.deep_equal(listed(), want) end)
    return listed()
  end

  -- :AgentDiffs, picking the subagent (the picker is vim.ui.select here).
  C.lua([[
    _G.Snacks = nil
    vim.ui.select = function(items, _, on_choice)
      for _, it in ipairs(items) do
        if it.agent == 'sub-a' then return on_choice(it) end
      end
    end
  ]])
  C.cmd('AgentDiffs')
  T.ok("picking an agent: its own files in that worktree, not yours", shows({ 'A token.txt', 'M app.txt' }), { 'A token.txt', 'M app.txt' })
  T.ok('...labelled for the statusline', C.lua('return vim.t.diffview_context or ""'), 'write the token · sub-a in wt-a')
  T.ok('...with no file of that worktree opened as a file (no language server there)', C.wait_for(3000, [[
    local n = 0
    for _, w in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
      if vim.wo[w].diff and vim.bo[vim.api.nvim_win_get_buf(w)].buftype == '' then n = n + 1 end
    end
    return n
  ]], 0), 0)

  -- The subagent works on: the open view follows its chain.
  T.write(R.a .. '/src/more.txt', { 'more' })
  tip = sub_a({ 'src/more.txt' })
  C.event({ ev = 'snapshot', point = 'agent', root = R.a, ref = ref('sub-a'), commit = tip, sid = sid })
  T.ok('the agent works on: the view follows it', shows({ 'A more.txt', 'A token.txt', 'M app.txt' }), { 'A more.txt', 'A token.txt', 'M app.txt' })

  C.cmd('AgentDiffs')
  T.ok('the same agent again, inside the view: closes it', C.wait_for(3000, 'return #vim.api.nvim_list_tabpages()', 1), 1)
  C.close()
end)
