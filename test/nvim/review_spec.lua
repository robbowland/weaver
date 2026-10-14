-- The review surface at its public boundaries: commands and keys in, what
-- diffview's file panel lists (and the tab's statusline label) out. Needs
-- diffview-plus.
local T = dofile(vim.fn.fnamemodify(debug.getinfo(1, 'S').source:sub(2), ':h') .. '/helper.lua')

T.run(function()
  if not T.has_diffview() then
    T.skip('review surface', 'diffview-plus.nvim not found (set WEAVER_TEST_PLUGINS)')
    return
  end
  local R = T.repo()
  local sid = 'sid-review'
  local state = R.base .. '/state.json'
  local function set_state(st)
    st.session_id = sid
    T.write(state, vim.json.encode(st))
  end
  set_state({ state = 'done', turn = 2 })
  local C = T.child({ WEAVER_STATE = state, WEAVER_BIN = T.root .. '/test/nvim/fake-weaver' })
  C.cmd('cd ' .. R.main)
  C.cmd('tcd ' .. R.a)
  C.cmd('edit ' .. R.a .. '/README.md')

  -- Two turns in worktree a, with your own edit between them.
  T.snapshot(R.a, sid, 1, 'start', 'first prompt')
  T.write(R.a .. '/src/app.txt', { 'line one', 'line two, turn one', 'line three' })
  T.write(R.a .. '/src/new.txt', { 'created in turn one' })
  T.snapshot(R.a, sid, 1, 'end', 'first prompt')
  T.write(R.a .. '/README.md', { 'readme, edited by you' })
  T.snapshot(R.a, sid, 2, 'start', 'second prompt')
  T.write(R.a .. '/src/new.txt', { 'created in turn one', 'grown in turn two' })
  T.write(R.a .. '/src/other.txt', { 'one', 'two', 'three', 'four', 'five' })
  T.snapshot(R.a, sid, 2, 'end', 'second prompt')
  local index_before = T.git(R.a, { 'ls-files', '--stage' })

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
  local function review_tabs()
    return C.lua([[
      local n = 0
      for _, t in ipairs(vim.api.nvim_list_tabpages()) do
        if vim.t[t].diffview_scope then n = n + 1 end
      end
      return n
    ]])
  end
  local function label() return C.lua('return vim.t.diffview_context or ""') end

  -- turn: the last turn only, untracked files included, and a file that was
  -- already untracked shown as modified (not deleted).
  C.cmd('AgentReview turn')
  T.ok("turn: the last turn's files, untracked included", shows({ 'A other.txt', 'M new.txt' }), { 'A other.txt', 'M new.txt' })
  T.ok('...labelled for the statusline', label(), 'turn 2')
  T.ok('your index is untouched', T.git(R.a, { 'ls-files', '--stage' }), index_before)

  -- The same key from another tab focuses the view; inside it, closes it.
  local review_tab = C.lua('return vim.fn.tabpagenr()')
  C.cmd('tabfirst')
  C.cmd('AgentReview turn')
  T.ok('same scope from another tab: back to the view', C.lua('return vim.fn.tabpagenr()') .. '|' .. review_tabs(), review_tab .. '|1')
  C.cmd('AgentReview turn')
  T.ok('same scope inside the view: closes it', C.wait_for(3000, 'return #vim.api.nvim_list_tabpages()', 1), 1)

  -- Another scope replaces the view instead of stacking a second.
  C.cmd('AgentReview turn')
  shows({ 'A other.txt', 'M new.txt' })
  C.cmd('AgentReview branch')
  local branch = { 'A new.txt', 'A other.txt', 'M README.md', 'M app.txt' }
  T.ok('branch: committed and uncommitted work, untracked included', shows(branch), branch)
  T.ok('...replacing the turn view', review_tabs(), 1)

  -- The agent edits: the open view refreshes itself.
  T.write(R.a .. '/src/third.txt', { 'agent wrote this' })
  C.event({ ev = 'edit', root = R.a, file = R.a .. '/src/third.txt', sid = sid })
  local with_third = vim.list_extend(vim.deepcopy(branch), { 'A third.txt' })
  table.sort(with_third)
  T.ok('agent edit: the view refreshes', shows(with_third), with_third)

  -- mine: the files the agent changed (and the ones the state file lists),
  -- not your README edit.
  set_state({ state = 'done', turn = 2, files = { R.a .. '/src/third.txt' } })
  C.cmd('AgentReview mine')
  local mine = { 'A new.txt', 'A other.txt', 'A third.txt', 'M app.txt' }
  T.ok("mine: only the agent's files", shows(mine), mine)

  -- While the agent works, turn means the turn so far.
  T.snapshot(R.a, sid, 3, 'start', 'third prompt')
  T.write(R.a .. '/src/app.txt', { 'line one', 'line two, turn three', 'line three' })
  C.event({ ev = 'state', state = 'working', turn = 3, sid = sid })
  C.cmd('AgentReview turn')
  T.ok('working: the turn so far', { shows({ 'M app.txt' }), label() }, { { 'M app.txt' }, 'turn 3 so far' })

  -- reviewed: before any mark, since the agent started; after marking,
  -- nothing; then whatever changes next.
  C.cmd('AgentReview reviewed')
  local since_start = 'since the agent started (nothing marked reviewed)'
  T.ok('reviewed, nothing marked: since the agent started', C.wait_for(3000, 'return vim.t.diffview_context', since_start), since_start)
  C.cmd('AgentReviewed')
  T.wait(5000, function() return (label() or ''):match('^since reviewed') ~= nil end)
  T.ok('marking reviewed: the view starts from the mark', { (label() or ''):match('^since reviewed') ~= nil, shows({}) }, { true, {} })
  T.write(R.a .. '/src/other.txt', { 'one', 'two', 'THREE', 'four', 'five' })
  C.event({ ev = 'edit', root = R.a, file = R.a .. '/src/other.txt', sid = sid })
  T.ok('...then shows what changes after it', shows({ 'M other.txt' }), { 'M other.txt' })

  -- gf in the diff: the file at the same line, in its worktree's tab.
  T.wait(5000, function()
    return C.lua([[
      for _, w in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
        local b = vim.api.nvim_win_get_buf(w)
        if vim.wo[w].diff and vim.bo[b].buftype == '' and vim.api.nvim_buf_get_name(b):match('other%.txt$') then
          vim.api.nvim_set_current_win(w)
          return vim.api.nvim_buf_line_count(b) == 5
        end
      end
      return false
    ]])
  end)
  C.cmd('3')
  C.input('gf')
  T.ok(
    'gf: the file at the same line, in the tab of its worktree',
    C.wait_for(2000, "return vim.fn.tabpagenr() .. '|' .. vim.fn.expand('%:t') .. '|' .. vim.fn.line('.')", '1|other.txt|3'),
    '1|other.txt|3'
  )
  T.ok('...the review stays open', review_tabs(), 1)

  -- :AgentChanges: one quickfix entry per hunk.
  C.cmd('AgentChanges reviewed')
  T.ok(
    'AgentChanges: the hunks in quickfix',
    C.lua("return vim.tbl_map(function(i) return vim.fn.fnamemodify(vim.fn.bufname(i.bufnr), ':t') .. ':' .. i.lnum .. ':' .. i.text end, vim.fn.getqflist())"),
    { 'other.txt:3:THREE' }
  )
  C.cmd('cclose')

  -- :AgentTurns: one row per turn with changes; picking one opens that turn.
  C.lua([[
    _G.Snacks = nil
    vim.ui.select = function(items, opts, cb)
      _G.rows = vim.tbl_map(opts.format_item, items)
      cb(items[#items])
    end
  ]])
  C.cmd('AgentTurns')
  local rows = C.lua('return _G.rows')
  T.ok('AgentTurns: newest first, with their prompts', { #rows, rows[1]:match('second prompt') ~= nil, rows[2]:match('first prompt') ~= nil }, { 2, true, true })
  T.ok('...picking one opens that turn alone', { shows({ 'A new.txt', 'M app.txt' }), label() }, { { 'A new.txt', 'M app.txt' }, 'turn 1 only' })

  -- uncommitted: diffview's own index view; the key again closes it.
  C.cmd('AgentReview uncommitted')
  T.ok('uncommitted', C.wait_for(3000, 'return vim.t.diffview_context', 'uncommitted'), 'uncommitted')
  C.cmd('AgentReview uncommitted')
  T.ok('...and closed again', C.wait_for(3000, 'return #vim.api.nvim_list_tabpages()', 1), 1)

  C.close()
end)
