-- Review notes for the agent: :AgentNote on lines (or a selection), the one
-- message they make, and :AgentSend handing it to `weaver send` for the bound
-- pane (the fake weaver records what it got), keeping them when it fails.
local T = dofile(vim.fn.fnamemodify(debug.getinfo(1, 'S').source:sub(2), ':h') .. '/helper.lua')

T.run(function()
  local R = T.repo()
  local sent = R.base .. '/sent.txt'
  local state = R.base .. '/state.json'
  T.write(state, vim.json.encode({ state = 'done', session_id = 'sid-notes' }))
  local C = T.child({ WEAVER_STATE = state, WEAVER_BIN = T.root .. '/test/nvim/fake-weaver', WEAVER_PANE = '7',
    WEAVER_FAKE_SEND = sent })
  local function notes() return C.lua('return #require("weaver.notes").items') end
  C.cmd('cd ' .. R.a)
  C.cmd('edit ' .. R.a .. '/src/app.txt')

  C.cmd('2,3AgentNote use a constant here')
  C.cmd('1AgentNote and say why')
  T.ok('note: one per :AgentNote, in order', notes(), 2)
  T.ok('note: marked at the end of its lines', C.lua([[
    local ns = vim.api.nvim_create_namespace('weaver_notes')
    return #vim.api.nvim_buf_get_extmarks(0, ns, 0, -1, {})]]), 2)
  T.ok('message: each note its file and lines, the code it is on, then what you said',
    C.lua('return require("weaver.notes").message()'), table.concat({
      'Review notes on your changes (from weaver):',
      '',
      '1. src/app.txt:2-3',
      '   > line two',
      '   > line three',
      '   use a constant here',
      '',
      '2. src/app.txt:1',
      '   > line one',
      '   and say why',
    }, '\n'))

  C.cmd('AgentSend')
  T.wait(3000, function() return notes() == 0 end)
  T.ok('send: weaver send to the bound pane, with the message', vim.fn.readfile(sent)[1], 'send 7')
  T.ok('...all of it', #vim.fn.readfile(sent), 11)
  T.ok('...and the notes go once it has them', notes(), 0)

  -- The agent busy: the notes stay, for later.
  local C2 = T.child({ WEAVER_STATE = state, WEAVER_BIN = T.root .. '/test/nvim/fake-weaver', WEAVER_PANE = '7',
    WEAVER_FAKE_SEND = sent, WEAVER_FAKE_SEND_FAIL = '1' })
  C2.cmd('edit ' .. R.a .. '/README.md')
  C2.cmd('1AgentNote later')
  C2.cmd('AgentSend')
  T.settle(500)
  T.ok('send: refused (the agent busy), the notes stay', C2.lua('return #require("weaver.notes").items'), 1)
  C2.cmd('AgentSend!')
  T.wait(3000, function() return C2.lua('return #require("weaver.notes").items') == 0 end)
  T.ok('send!: weaver sends anyway (--force)', vim.fn.readfile(sent)[1] .. '|' .. C2.lua('return #require("weaver.notes").items'),
    'send --force 7|0')

  -- Not in a file: no note.
  C.cmd('enew')
  C.cmd('setlocal buftype=nofile')
  C.cmd('1AgentNote nowhere')
  T.ok('note: only on a file', notes(), 0)
end)
