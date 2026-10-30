-- Review in place at its public boundaries: :AgentInPlace in, the quickfix
-- list and gitsigns' hunks in your own buffers out. Needs gitsigns.
local T = dofile(vim.fn.fnamemodify(debug.getinfo(1, 'S').source:sub(2), ':h') .. '/helper.lua')

T.run(function()
  -- Where a line of an older version is now, through a -U0 diff's hunks.
  vim.opt.runtimepath:prepend(T.root .. '/nvim')
  local inplace = require('weaver.inplace')
  local h = { { 0, 0, 1, 2 }, { 5, 1, 7, 1 }, { 8, 2, 9, 0 } }
  T.ok('a line below added lines moves down', { inplace.map_line(3, h) }, { 5, false })
  T.ok('a changed line is marked so', { inplace.map_line(5, h) }, { 7, true })
  T.ok('a deleted line points just past where it was', { inplace.map_line(9, h) }, { 10, true })
  T.ok('a line below a deletion moves up', { inplace.map_line(12, h) }, { 12, false })
  T.ok('a hunk changed below its first line is changed', inplace.touches(4, 2, h), true)
  T.ok('...and one beside a change is not', inplace.touches(6, 1, h), false)
  local patch = inplace.parse_patch(table.concat({
    'diff --git a/x.lua b/x.lua',
    '--- a/x.lua',
    '+++ b/x.lua',
    '@@ -1 +0,0 @@',
    '--- a comment',
    '@@ -3,0 +3 @@',
    '+++ added',
    'diff --git "a/sp ace" "b/sp ace"',
    '--- "a/sp ace"\t',
    '+++ "b/sp ace"\t',
    '@@ -1 +1 @@',
    '-a',
    '+b',
  }, '\n'))
  T.ok("a hunk's '--- x' and '+++ x' lines are not file headers", {
    patch[1].old,
    patch[1].new,
    #patch[1].hunks,
    patch[1].hunks[1].removed,
    patch[1].hunks[2].text,
    patch[2].new,
  }, { 'x.lua', 'x.lua', 2, 1, '++ added', 'sp ace' })

  local plugins = vim.g.weaver_test_plugins or {}
  if not plugins.gitsigns then
    T.skip('review in place', 'gitsigns.nvim not found (set WEAVER_TEST_PLUGINS)')
    return
  end
  local R = T.repo()
  local state = R.base .. '/state.json'
  T.write(state, vim.json.encode({ session_id = 'sid-inplace', state = 'done', turn = 1 }))

  -- On feat-a: two commits, main merged in, then what is not committed.
  T.write(R.a .. '/src/app.txt', { 'line one', 'line two, c1', 'line three' })
  T.write(R.a .. '/src/new.txt', { 'new in c1' })
  T.write(R.a .. '/src/x.lua', { '-- one', 'two', 'three' })
  T.git(R.a, { 'add', '.' })
  T.git(R.a, { 'commit', '-qm', 'c1: change line two' })
  T.write(R.a .. '/src/app.txt', { 'header one', 'header two', 'line one', 'line two, c1', 'line three' })
  T.write(R.a .. '/README.md', { 'readme, c2' })
  T.write(R.a .. '/src/x.lua', { 'two', 'three' })
  T.git(R.a, { 'commit', '-qam', 'c2: headers' })
  T.write(R.main .. '/src/main-only.txt', { 'from main' })
  T.git(R.main, { 'add', '.' })
  T.git(R.main, { 'commit', '-qm', 'on main' })
  T.git(R.a, { 'merge', '-q', '--no-edit', 'main' })
  T.write(R.a .. '/src/app.txt', { 'header one', 'header two', 'line one', 'line two, c1', 'line three', 'line four, yours' })
  T.write(R.a .. '/src/untracked.txt', { 'not yet added' })
  local index_before = T.git(R.a, { 'ls-files', '--stage' })

  local C = T.child({ WEAVER_STATE = state, WEAVER_BIN = T.root .. '/test/nvim/fake-weaver' })
  C.cmd('cd ' .. R.a)
  C.cmd('edit ' .. R.b .. '/README.md')
  local other = C.lua('return vim.api.nvim_get_current_buf()')
  C.cmd('edit ' .. R.a .. '/src/app.txt')
  local app = C.lua('return vim.api.nvim_get_current_buf()')

  -- The quickfix list: "<file>:<line>", with the text's head when asked.
  local function listed(with_text)
    return C.lua([[
      local out = {}
      for _, it in ipairs(vim.fn.getqflist()) do
        local s = vim.fn.fnamemodify(vim.api.nvim_buf_get_name(it.bufnr), ':t') .. ':' .. it.lnum
        if ... then s = (it.text:match('^(%S+)') or '') .. ' ' .. s end
        out[#out + 1] = s
      end
      return out
    ]], with_text or false)
  end
  local function lists(want, with_text)
    T.wait(5000, function() return vim.deep_equal(listed(with_text), want) end)
    return listed(with_text)
  end
  -- gitsigns' hunks in BUF, as "<type>@<line>".
  local function hunks(buf)
    return C.lua([[
      local hs = require('gitsigns').get_hunks(...)
      if not hs then return nil end
      local out = {}
      for _, h in ipairs(hs) do out[#out + 1] = h.type .. '@' .. h.added.start end
      return out
    ]], buf)
  end
  local function hunks_become(buf, want)
    T.wait(5000, function() return vim.deep_equal(hunks(buf), want) end)
    return hunks(buf)
  end
  hunks_become(app, { 'add@6' })

  C.cmd('AgentInPlace')
  local branch = { 'README.md:1', 'app.txt:1', 'app.txt:4', 'app.txt:6', 'new.txt:1', 'untracked.txt:1', 'x.lua:1' }
  T.ok("the branch in place: every change since its root, committed or not, file by file (not main's)", lists(branch), branch)
  T.ok('...said in one line', vim.trim(C.messages()):match('[^\n]*$'), 'weaver: in place: wt-a vs main · 7 changes in 5 files · ]q steps')
  T.ok("...and the file's signs are the branch's", hunks_become(app, { 'add@1', 'change@4', 'add@6' }), { 'add@1', 'change@4', 'add@6' })
  T.ok("another worktree's buffer keeps its own base", C.lua([[
    local c = require('gitsigns.cache').cache[...]
    return c and c.git_obj.revision == nil
  ]], other), true)
  T.ok('your index is untouched', T.git(R.a, { 'ls-files', '--stage' }), index_before)

  -- Opened while on: compared against the root too.
  C.cmd('edit ' .. R.a .. '/src/new.txt')
  local new = C.lua('return vim.api.nvim_get_current_buf()')
  T.ok('a file opened while on: its signs are the branch’s', hunks_become(new, { 'add@1' }), { 'add@1' })

  -- An agent's edit: the list follows; a list of your own stays current.
  T.write(R.a .. '/src/later.txt', { 'agent wrote this' })
  C.event({ ev = 'edit', root = R.a, file = R.a .. '/src/later.txt', sid = 'sid-inplace' })
  local with_later = { 'README.md:1', 'app.txt:1', 'app.txt:4', 'app.txt:6', 'later.txt:1', 'new.txt:1', 'untracked.txt:1', 'x.lua:1' }
  T.ok('an edit while on: the list follows', lists(with_later), with_later)
  C.lua([[vim.fn.setqflist({}, ' ', { title = 'mine', items = { { filename = 'x', lnum = 1, text = 'mine' } } })]])
  T.write(R.a .. '/src/later.txt', { 'agent wrote this', 'and more' })
  C.event({ ev = 'edit', root = R.a, file = R.a .. '/src/later.txt', sid = 'sid-inplace' })
  T.settle(1500)
  T.ok('...but never takes over a list of yours', C.lua('return vim.fn.getqflist({ title = 0 }).title'), 'mine')

  -- Commit by commit: each commit's changes where they are now, oldest
  -- first; the merge lists none of its own; then what is not committed.
  C.cmd('AgentInPlace commits')
  local abbrev = function(rev) return vim.trim(T.git(R.a, { 'rev-parse', '--short', rev })) end
  local c1, c2 = abbrev('HEAD~1~1'), abbrev('HEAD~1')
  local by_commit = {
    c1 .. ' app.txt:4',
    c1 .. ' new.txt:1',
    c1 .. ' x.lua:1',
    c2 .. ' README.md:1',
    c2 .. ' app.txt:1',
    c2 .. ' x.lua:1',
    'uncommitted app.txt:6',
    'uncommitted later.txt:1',
    'uncommitted untracked.txt:1',
  }
  T.ok('commit by commit: oldest first, each change where it is now', lists(by_commit, true), by_commit)
  T.ok('...a change a later commit rewrote marked so, and no other', C.lua([[
    local out = {}
    for _, it in ipairs(vim.fn.getqflist()) do
      if it.text:find('(changed since)', 1, true) then
        out[#out + 1] = vim.fn.fnamemodify(vim.api.nvim_buf_get_name(it.bufnr), ':t')
      end
    end
    return out
  ]]), { 'x.lua' })
  -- gitsigns updates a buffer as it comes into view.
  C.cmd('buffer ' .. app)
  T.ok('...with the same signs', hunks_become(app, { 'add@1', 'change@4', 'add@6' }), { 'add@1', 'change@4', 'add@6' })

  -- The agent commits: its commit joins the steps.
  T.git(R.a, { 'add', 'src/later.txt' })
  T.git(R.a, { 'commit', '-qm', 'c3: later' })
  local c3 = abbrev('HEAD')
  C.event({ ev = 'edit', root = R.a, file = R.a .. '/src/later.txt', sid = 'sid-inplace' })
  local with_c3 = {
    c1 .. ' app.txt:4',
    c1 .. ' new.txt:1',
    c1 .. ' x.lua:1',
    c2 .. ' README.md:1',
    c2 .. ' app.txt:1',
    c2 .. ' x.lua:1',
    c3 .. ' later.txt:1',
    'uncommitted app.txt:6',
    'uncommitted untracked.txt:1',
  }
  T.ok('a commit made while on: its changes become its step', lists(with_c3, true), with_c3)

  C.cmd('AgentInPlace commits')
  C.cmd('buffer ' .. app)
  T.ok('the same again: off, the signs back to the index', hunks_become(app, { 'add@6' }), { 'add@6' })
  C.cmd('buffer ' .. new)
  T.ok('...in the file opened while on too', hunks_become(new, {}), {})
  T.ok('...and the list emptied', listed(), {})
  C.close()
end)
