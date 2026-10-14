-- The rest of weaver's Neovim side at its public boundaries: the event
-- contract, the winbar, reloading (and its watchers, defect 12), :AgentWhy
-- and :AgentLog.
local T = dofile(vim.fn.fnamemodify(debug.getinfo(1, 'S').source:sub(2), ':h') .. '/helper.lua')

T.run(function()
  local R = T.repo()
  local sid = 'sid-core'
  local state = R.base .. '/state.json'
  local logfile = R.base .. '/log.jsonl'
  T.write(state, vim.json.encode({ session_id = sid, state = 'working', turn = 3, prompt = 'fix the readme', log = logfile }))
  local C = T.child({ WEAVER_STATE = state })
  C.cmd('cd ' .. R.main)
  C.cmd('edit ' .. R.main .. '/README.md')

  -- Events ----------------------------------------------------------------
  T.ok('starts from the state file', C.winbar():match('^● working · turn 3 · fix the readme') ~= nil, true)
  T.ok('WeaverEvent returns at once', C.lua('return WeaverEvent(...)', '{"ev":"state","state":"done"}'), 0)
  C.lua([[
    _G.got = nil
    vim.api.nvim_create_autocmd('User', { pattern = 'WeaverEvent', callback = function(ev) _G.got = ev.data end })
  ]])
  C.event({ ev = 'state', state = 'needs-you', waiting = 'Bash: rm -rf build', sid = sid, turn = 3 })
  T.ok('User WeaverEvent carries the event', C.lua('return _G.got.waiting'), 'Bash: rm -rf build')
  T.ok('winbar: needs you and what it waits on', C.winbar():match('^! needs you · turn 3 · Bash: rm %-rf build') ~= nil, true)
  C.lua('return WeaverEvent(...)', 'not json')
  C.event({ ev = 'state', state = 'working', seq = 1 })
  T.ok('garbage and older events change nothing', C.winbar():match('^! needs you') ~= nil, true)
  C.event({ ev = 'state', state = 'done', sid = sid, turn = 3 })
  T.ok('done: the prompt again', C.winbar():match('^✓ done · turn 3 · fix the readme') ~= nil, true)
  T.ok('a plain-text component for statuslines', C.lua("return require('weaver.status').text()"), '✓ done · turn 3')

  -- The winbar is on file windows only.
  C.cmd('botright copen')
  T.ok('no winbar on quickfix', C.lua('return vim.wo.winbar'), '')
  C.cmd('cclose')
  C.cmd('botright split | terminal')
  T.ok('no winbar on a terminal', C.wait_for(1000, 'return vim.wo.winbar', ''), '')
  C.cmd('bwipeout!')
  C.cmd('new')
  C.cmd('diffthis')
  T.ok('no winbar on a diff window', C.wait_for(1000, 'return vim.wo.winbar', ''), '')
  C.cmd('bwipeout!')

  -- Reloading ---------------------------------------------------------------
  local app = R.main .. '/src/app.txt'
  -- Only a watch can reload this fast: no CursorHold, and the fallback timer
  -- runs every 3s.
  C.cmd('set updatetime=10000')
  C.cmd('edit ' .. app)
  T.write(app, { 'line one', 'line two', 'line three', 'added by the agent' })
  T.ok('an outside change reloads within 0.6s', C.wait_for(600, 'return vim.fn.getline(4)', 'added by the agent'), 'added by the agent')
  C.cmd('normal! ggiMINE ')
  T.write(app, { 'agent again' })
  C.cmd('checktime')
  C.cmd('checktime')
  T.ok('unsaved changes are never reloaded over', C.lua('return vim.fn.getline(1)'), 'MINE line one')
  T.ok('...you are told once', select(2, C.messages():gsub('kept your unsaved changes', '')), 1)
  C.cmd('edit!')
  -- Watches follow what is open: after opening and closing more files than
  -- the watch limit, a file opened now still reloads at once.
  for i = 1, 70 do
    T.write(R.base .. '/d' .. i .. '/f.txt', { 'x' })
  end
  C.lua([[
    for i = 1, 70 do
      vim.cmd('edit ' .. ... .. '/d' .. i .. '/f.txt')
      vim.cmd('bwipeout')
    end
  ]], R.base)
  T.write(R.base .. '/late/f.txt', { 'before' })
  C.cmd('edit ' .. R.base .. '/late/f.txt')
  T.settle(100)
  T.write(R.base .. '/late/f.txt', { 'after' })
  T.ok('after 70 files came and went, a new one still reloads at once', C.wait_for(600, 'return vim.fn.getline(1)', 'after'), 'after')

  -- :AgentWhy ---------------------------------------------------------------
  T.write(app, { 'line one', 'line two', 'line three' })
  T.snapshot(R.main, sid, 1, 'start', 'first prompt')
  T.write(app, { 'line one', 'TURN ONE', 'line three' })
  T.snapshot(R.main, sid, 1, 'end', 'make line two loud')
  T.write(app, { 'line one', 'TURN ONE', 'line three', 'yours' })
  T.snapshot(R.main, sid, 2, 'start', 'second prompt')
  C.cmd('edit! ' .. app)
  C.cmd('normal! Gonot snapshotted')
  local function why(line)
    C.messages()
    C.cmd(tostring(line))
    C.cmd('AgentWhy')
    return C.messages()
  end
  T.ok('why: the turn and prompt that changed the line', why(2):match('line 2: turn 1, %d+s ago.make line two loud') ~= nil, true)
  T.ok('why: a line from before the agent', why(1):match('line 1: unchanged since the agent started here') ~= nil, true)
  T.ok('why: your edit between turns', why(4):match('line 4: changed between turns, before turn 2') ~= nil, true)
  T.ok('why: an unsaved line', why(5):match('line 5: changed after the last snapshot') ~= nil, true)
  C.cmd('edit!')

  -- :AgentLog ---------------------------------------------------------------
  local entries = {
    { t = 1000, pane = 0, turn = 1, ev = 'prompt', text = 'make line two loud' },
    { t = 1001, pane = 0, turn = 1, ev = 'edit', file = app, root = R.main, tool = 'Edit' },
    { t = 1002, pane = 0, turn = 1, ev = 'needs-you', waiting = 'Bash: git push' },
    { t = 1003, pane = 0, turn = 1, ev = 'stop', last = 'Done.' },
  }
  local text = vim.tbl_map(vim.json.encode, entries)
  text[#text + 1] = '{broken'
  T.write(logfile, text)
  C.cmd('edit ' .. R.main .. '/README.md')
  C.cmd('AgentLog')
  local lines = C.lua('return vim.api.nvim_buf_get_lines(0, 0, -1, false)')
  T.ok('log: a heading per turn with its prompt', lines[1]:match('^turn 1 · %d%d:%d%d · make line two loud$') ~= nil, true)
  T.ok('log: one line per event, a broken line skipped', #lines, 4)
  T.ok('log: what it waited on', lines[3]:match('needs you%s+Bash: git push') ~= nil, true)
  C.cmd('2')
  C.input('<CR>')
  T.ok('log: <CR> on an edit opens the file', C.bufname(), 'app.txt')
  C.cmd('AgentLog')
  local f = io.open(logfile, 'a')
  f:write(vim.json.encode({ t = 1004, pane = 0, turn = 2, ev = 'prompt', text = 'now turn two' }) .. '\n')
  f:close()
  C.event({ ev = 'state', state = 'working', turn = 2, sid = sid })
  T.ok('log: kept current as events arrive', C.lua('return vim.fn.getline("$")'):match('^turn 2 · .* · now turn two$') ~= nil, true)
  C.cmd('close')

  -- The previous release's calls still work, as edit and checktime events.
  C.lua('_G.got = nil')
  T.ok('AgentFollow returns at once', C.lua('return AgentFollow(...)', R.main, app), 0)
  C.poke()
  T.ok('...as an edit event', C.lua('return _G.got.ev .. "|" .. _G.got.file'), 'edit|' .. app)
  C.lua('return AgentChecktime()')
  C.poke()
  T.ok('AgentChecktime: a checktime event', C.lua('return _G.got.ev'), 'checktime')

  C.close()
end)
