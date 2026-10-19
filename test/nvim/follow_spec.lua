-- Polite following (defect 1), at weaver's public boundaries: hook events go
-- in through WeaverEvent, keys are typed into a child nvim, and what is
-- checked is what you would see (buffers, tabs, modes, the winbar).
local T = dofile(vim.fn.fnamemodify(debug.getinfo(1, 'S').source:sub(2), ':h') .. '/helper.lua')

T.run(function()
  local R = T.repo()
  local C = T.child()
  C.cmd('cd ' .. R.main)
  C.cmd('let g:weaver_follow_idle_ms = 300')
  local app = R.main .. '/src/app.txt'
  local readme = R.main .. '/README.md'
  local function idle() T.settle(450) end
  local function pending() return C.winbar():match('→ (%S+)') or '' end
  local function shown() return C.wait_for(1500, "return vim.fn.expand('%:t')", C.bufname()) end
  local function wait_file(name) return C.wait_for(2000, "return vim.fn.expand('%:t')", name) end

  -- Idle in normal mode: followed at once, to the changed line.
  C.cmd('edit ' .. readme)
  T.write(app, { 'line one', 'line two', 'CHANGED', 'line four' })
  idle()
  C.event({ ev = 'edit', root = R.main, file = app, sid = 's1', state = 'working', turn = 1 })
  T.ok('idle: the edit is followed', wait_file('app.txt'), 'app.txt')
  T.ok('...to the changed line', C.wait_for(1500, 'return vim.fn.line(".")', 3), 3)

  -- Typing in insert mode: every key stays where you type it.
  C.cmd('edit ' .. readme)
  C.input('Go' .. 'typed ')
  C.event({ ev = 'edit', root = R.main, file = app })
  C.input('more')
  T.ok('insert mode: not followed, still typing', C.bufname() .. '|' .. C.mode(), 'README.md|i')
  T.ok('...the edit waits in the winbar', pending(), 'app.txt')
  idle()
  T.ok('...even past the idle time, while you are in insert mode', C.bufname(), 'README.md')
  C.input('<Esc>')
  T.ok('every keystroke landed in your file', C.lua('return vim.fn.getline(2)'), 'typed more')

  -- Unsaved changes: never replaced; once saved, the edit is followed.
  idle()
  T.ok('unsaved buffer: not replaced', C.bufname() .. '|' .. tostring(C.lua('return vim.bo.modified')), 'README.md|true')
  T.ok('...said once', select(2, C.messages():gsub('you have unsaved changes there', '')), 1)
  T.ok('...still pending', pending(), 'app.txt')
  idle()
  T.ok('...and not said again', select(2, C.messages():gsub('you have unsaved changes there', '')), 0)
  C.cmd('write')
  T.ok('after saving: followed', wait_file('app.txt'), 'app.txt')
  T.ok('...nothing pending any more', pending(), '')

  -- Keys typed in normal mode: the edit waits until you have been idle.
  C.cmd('edit ' .. readme)
  C.input('gg')
  C.event({ ev = 'edit', root = R.main, file = app })
  T.ok('just typed: not followed yet', C.bufname(), 'README.md')
  T.ok('...followed once you are idle', wait_file('app.txt'), 'app.txt')

  -- In quickfix: no follow. A terminal-only tab is never replaced; jumping
  -- opens the file beside the terminal.
  C.cmd('edit ' .. readme)
  C.cmd('botright copen')
  idle()
  C.event({ ev = 'edit', root = R.main, file = app })
  T.ok('in quickfix: no follow', C.lua('return vim.bo.buftype') .. '|' .. pending(), 'quickfix|app.txt')
  C.cmd('cclose')
  wait_file('app.txt')
  C.cmd('tabnew')
  C.cmd('tcd ' .. R.b)
  C.cmd('terminal')
  idle()
  C.event({ ev = 'edit', root = R.b, file = R.b .. '/README.md' })
  T.ok('terminal-only tab: the terminal is not replaced', C.lua('return vim.bo.buftype'), 'terminal')
  C.cmd('AgentJump')
  T.ok(
    'AgentJump opens the file beside the terminal',
    C.lua([[
      local kinds = {}
      for _, w in ipairs(vim.api.nvim_tabpage_list_wins(0)) do
        local b = vim.api.nvim_win_get_buf(w)
        kinds[#kinds + 1] = vim.bo[b].buftype == 'terminal' and 'term' or vim.fn.fnamemodify(vim.api.nvim_buf_get_name(b), ':t')
      end
      table.sort(kinds)
      return table.concat(kinds, ',')
    ]]),
    'README.md,term'
  )
  C.cmd('tabonly!')

  -- A worktree's tab is updated in the background while you work elsewhere.
  C.cmd('tcd ' .. R.main)
  C.cmd('edit ' .. readme)
  C.cmd('tabnew')
  C.cmd('tcd ' .. R.a)
  C.cmd('edit ' .. R.a .. '/README.md')
  C.cmd('tabfirst')
  C.input('i')
  C.event({ ev = 'edit', root = R.a, file = R.a .. '/src/app.txt' })
  T.ok('busy: you stay in your tab', C.lua('return vim.fn.tabpagenr()') .. '|' .. C.mode(), '1|i')
  T.ok(
    "...the worktree's own tab already shows the file",
    C.lua("return vim.fn.fnamemodify(vim.fn.bufname(vim.fn.tabpagebuflist(2)[1]), ':t')"),
    'app.txt'
  )
  C.input('<Esc>')
  T.ok('idle again: switched to that tab', C.wait_for(2000, "return vim.fn.tabpagenr() .. '|' .. vim.fn.expand('%:t')", '2|app.txt'), '2|app.txt')

  -- A worktree with no tab yet gets one, with its own working directory.
  idle()
  C.event({ ev = 'edit', root = R.b, file = R.b .. '/src/app.txt' })
  T.ok(
    'new worktree: a tab of its own',
    C.wait_for(1500, "return vim.fn.tabpagenr('$') .. '|' .. vim.fn.fnamemodify(vim.fn.getcwd(), ':t') .. '|' .. vim.fn.expand('%:t')", '3|wt-b|app.txt'),
    '3|wt-b|app.txt'
  )

  -- A diff review is never taken over.
  C.cmd('tabnew')
  C.cmd('edit ' .. R.a .. '/src/app.txt')
  C.cmd('diffthis')
  C.cmd('vnew')
  C.lua("vim.api.nvim_buf_set_lines(0, 0, -1, false, { 'other' })")
  C.cmd('diffthis')
  local layout = 'return vim.fn.tabpagenr() .. "|" .. #vim.api.nvim_tabpage_list_wins(0)'
  local before = C.lua(layout)
  idle()
  C.event({ ev = 'edit', root = R.main, file = readme })
  idle()
  T.ok('diff review: your tab and windows kept', C.lua(layout), before)
  C.cmd('tabclose!')
  T.ok('...and the edit is followed once you leave it', C.wait_for(2000, "return vim.fn.expand('%:p')", readme), readme)

  -- Events can arrive out of order: an older one never wins.
  idle()
  C.event({ ev = 'edit', root = R.main, file = app, seq = 9e12 })
  C.event({ ev = 'edit', root = R.main, file = readme, seq = 9e12 - 5 })
  idle()
  T.ok('an older event never wins', wait_file('app.txt'), 'app.txt')

  -- ...even when a call from the previous release (no seq) comes between.
  C.event({ ev = 'edit', root = R.main, file = app, seq = 9e12 + 0.5 })
  C.lua('return AgentFollow(...)', R.main, app)
  C.poke()
  idle()
  C.event({ ev = 'edit', root = R.main, file = readme, seq = 9e12 - 7 })
  idle()
  T.ok('...even after a call without seq', wait_file('app.txt'), 'app.txt')

  -- focus (Alt v): no idle wait, but never out of insert mode.
  C.cmd('edit ' .. readme)
  C.input('gg')
  C.event({ ev = 'focus', root = R.main, file = app, seq = 9e12 + 1 })
  T.ok('focus: goes at once', C.bufname(), 'app.txt')
  C.cmd('edit ' .. readme)
  C.input('A')
  C.event({ ev = 'focus', root = R.main, file = app, seq = 9e12 + 2 })
  T.ok('focus in insert mode: pending instead', C.bufname() .. '|' .. C.mode() .. '|' .. pending(), 'README.md|i|app.txt')
  C.input('<Esc>')
  wait_file('app.txt')

  -- Following off: nothing moves, nothing pends.
  C.cmd('AgentFollow')
  C.cmd('edit ' .. readme)
  idle()
  C.event({ ev = 'edit', root = R.main, file = app, seq = 9e12 + 3 })
  idle()
  T.ok('following off: you stay, nothing pending', C.bufname() .. '|' .. pending(), 'README.md|')
  T.ok('...and the winbar says so', C.winbar():find('following off', 1, true) ~= nil, true)
  C.cmd('AgentFollow')

  -- A start screen (a dashboard plugin's, as a new thread's nvim shows) is
  -- replaced like an empty window, not waited on.
  C.cmd('enew')
  C.lua("vim.bo.buftype = 'nofile'; vim.bo.filetype = 'snacks_dashboard'")
  idle()
  C.event({ ev = 'edit', root = R.main, file = app, seq = 9e12 + 3.5 })
  T.ok('dashboard: replaced by the edit', wait_file('app.txt'), 'app.txt')

  -- A removed worktree's tab is closed when the agent moves on.
  C.cmd('tabonly!')
  C.cmd('tabnew')
  C.cmd('tcd ' .. R.b)
  C.cmd('edit ' .. R.b .. '/README.md')
  C.cmd('tabfirst')
  T.git(R.main, { 'worktree', 'remove', '--force', R.b })
  C.event({ ev = 'root', root = R.main, seq = 9e12 + 4 })
  T.ok('removed worktree: its tab closed', C.wait_for(1000, "return vim.fn.tabpagenr('$')", 1), 1)

  -- The start screen, left in a tab of its own, is taken by the next
  -- worktree's tab rather than left in the tab line; so is a worktree's tab
  -- still empty.
  local tabs = "return vim.fn.tabpagenr('$') .. '|' .. vim.fn.tabpagenr() .. '|' .. vim.fn.fnamemodify(vim.fn.getcwd(), ':t') .. '|' .. vim.fn.expand('%:t')"
  C.cmd('only!')
  C.cmd('enew')
  C.lua("vim.bo.buftype = 'nofile'; vim.bo.filetype = 'snacks_dashboard'")
  C.cmd('tabnew')
  C.cmd('edit ' .. readme)
  idle()
  C.event({ ev = 'edit', root = R.a, file = R.a .. '/src/app.txt', seq = 9e12 + 5 })
  T.ok('start screen: the new worktree takes its tab', C.wait_for(1500, tabs, '2|1|wt-a|app.txt'), '2|1|wt-a|app.txt')
  local c = R.base .. '/wt-c'
  T.git(R.main, { 'worktree', 'add', '-q', '-b', 'feat-c', c })
  C.cmd('tablast')
  C.cmd('tabnew')
  C.cmd('tabprevious')
  idle()
  C.event({ ev = 'edit', root = c, file = c .. '/src/app.txt', seq = 9e12 + 6 })
  T.ok('...and an empty tab', C.wait_for(1500, tabs, '3|3|wt-c|app.txt'), '3|3|wt-c|app.txt')

  C.close()
end)
