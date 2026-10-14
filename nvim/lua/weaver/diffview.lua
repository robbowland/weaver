-- One review view on diffview(-plus), for any nvim: nothing here knows about
-- agents. weaver's review scopes build on it, and so can your own mappings.
--
-- A view is opened with show(spec). Pressing the same thing again focuses
-- and refreshes the view, or closes it when you are in it; anything else
-- replaces it, so there is never a stack of review tabs. Views are tagged with
-- the tab variables `diffview_scope` (identity) and `diffview_context` (a
-- label for statuslines); any view with a `diffview_scope` is "the" view.
--
-- A spec is { key, label, root, compare, paths? }, where compare says what is
-- compared with what (see compare_args):
--   { kind = 'index' }                   the worktree against the index
--   { kind = 'worktree', base = rev }    the worktree against a commit
--   { kind = 'branch', base = rev }      the worktree against the merge base
--   { kind = 'commits', from = rev, to = rev }
--
-- diffview hides untracked files whenever the left side is a commit, and
-- `git diff <commit>` calls a file that is in the commit but untracked
-- DELETED. Views with a commit on the left and the worktree on the right
-- therefore run git against a private copy of the index in which untracked
-- files are intent-to-add. Your own index is never touched.
local M = {}

local api, fn = vim.api, vim.fn
local uv = vim.uv or vim.loop
local util = require('weaver.util')

-- Whether diffview can be used (loading it if it can).
function M.available()
  if not package.loaded['diffview.lib'] then
    if not pcall(require, 'diffview') then
      return false
    end
    M.install_keymaps()
  end
  return pcall(require, 'diffview.lib')
end

-- Bases ---------------------------------------------------------------------

local BASE_REFS = {
  'refs/remotes/upstream/HEAD',
  'refs/remotes/upstream/main',
  'refs/remotes/upstream/master',
  'refs/remotes/origin/HEAD',
  'refs/remotes/origin/main',
  'refs/remotes/origin/master',
  'refs/heads/main',
  'refs/heads/master',
}

-- What a branch is compared against: the default branch of the upstream or
-- origin remote (where their HEAD points, else main or master), else a local
-- main or master. One git call. Returns a short ref name or nil.
function M.default_base(root)
  local out = util.git(root, vim.list_extend({ 'for-each-ref', '--format=%(refname)%00%(symref)' }, vim.deepcopy(BASE_REFS)))
  local have = {}
  for line in (out or ''):gmatch('[^\n]+') do
    local ref, target = line:match('^([^%z]*)%z(.*)$')
    if ref then
      have[ref] = target ~= '' and target or ref
    end
  end
  for _, ref in ipairs(BASE_REFS) do
    local resolved = have[ref]
    -- A remote HEAD counts only if what it points to exists.
    if resolved and (resolved == ref or have[resolved] or util.git(root, { 'rev-parse', '--verify', '--quiet', resolved })) then
      return (resolved:gsub('^refs/remotes/', ''):gsub('^refs/heads/', ''))
    end
  end
end

-- Comparisons -----------------------------------------------------------------

-- diffview arguments for a comparison, and whether its left side is a commit
-- shown against the worktree (which needs the private index).
local function compare_args(compare)
  local kind = compare.kind
  if kind == 'index' then
    return {}, false
  elseif kind == 'worktree' then
    return { compare.base }, true
  elseif kind == 'branch' then
    return { compare.base .. '...HEAD', '--imply-local' }, true
  elseif kind == 'commits' then
    return { compare.from .. '..' .. compare.to }, false
  end
  error('weaver.diffview: unknown comparison ' .. vim.inspect(compare))
end

-- The commit hunks are listed from, for M.hunks.
local function compare_base(root, compare)
  if compare.kind == 'index' then
    return 'HEAD'
  elseif compare.kind == 'worktree' then
    return compare.base
  elseif compare.kind == 'branch' then
    return util.first_line(util.git(root, { 'merge-base', compare.base, 'HEAD' })) or compare.base
  end
  return compare.from
end

local function identity(spec)
  local args = compare_args(spec.compare)
  return table.concat(vim.list_extend({ spec.key, spec.root }, vim.list_extend(args, spec.paths or {})), '\0')
end

-- The private index -----------------------------------------------------------

local function git_path(root, name)
  return util.first_line(util.git(root, { 'rev-parse', '--path-format=absolute', '--git-path', name }))
end

-- The real and the private index of ROOT's worktree.
function M.index_paths(root)
  local real = git_path(root, 'index')
  local dir = git_path(root, 'weaver')
  if not real or not dir then
    return nil
  end
  fn.mkdir(dir, 'p')
  return real, dir .. '/view-index'
end

-- What the private index was last built from, per index path, so a refresh
-- that changes neither the real index nor the untracked files costs one
-- `git ls-files` and nothing else.
local built = {}
local seq = 0

local function index_stamp(real)
  local st = uv.fs_stat(real)
  return st and (st.mtime.sec .. '.' .. st.mtime.nsec .. ':' .. st.size) or ''
end

-- Bring IDX up to date: the real index REAL plus every untracked file as
-- intent-to-add, built aside and renamed into place. CB runs when done.
local function rebuild_index(root, real, idx, cb)
  vim.system({ 'git', '-C', root, 'ls-files', '-z', '--others', '--exclude-standard' }, {}, function(r)
    local untracked = r.code == 0 and r.stdout or ''
    local stamp = index_stamp(real) .. '\0' .. untracked
    if built[idx] == stamp and uv.fs_stat(idx) then
      return vim.schedule(cb)
    end
    seq = seq + 1
    local tmp = ('%s.%d.%d'):format(idx, uv.os_getpid(), seq)
    if uv.fs_stat(real) then
      uv.fs_copyfile(real, tmp)
    end
    local function done(res)
      if not res or res.code == 0 then
        uv.fs_rename(tmp, idx)
        built[idx] = stamp
      else
        uv.fs_unlink(tmp)
      end
      vim.schedule(cb)
    end
    if untracked == '' then
      return done()
    end
    vim.system({ 'git', '-C', root, 'add', '--intent-to-add', '--pathspec-from-file=-', '--pathspec-file-nul' }, {
      env = { GIT_INDEX_FILE = tmp },
      stdin = untracked,
    }, done)
  end)
end

-- Build ROOT's private index now and wait for it (at most 3s). Returns its path.
function M.build_index(root)
  local real, idx = M.index_paths(root)
  if not real then
    return nil
  end
  local done = false
  rebuild_index(root, real, idx, function() done = true end)
  vim.wait(3000, function() return done end, 5)
  return idx
end

-- Make VIEW list its files against ROOT's private index, rebuilt before each
-- listing.
local function use_private_index(view, root)
  local real, idx = M.index_paths(root)
  if not real then
    return
  end
  local cmd = vim.list_extend({ 'env', 'GIT_INDEX_FILE=' .. idx }, vim.deepcopy(view.adapter:get_command()))
  view.adapter.get_command = function() return cmd end
  local update = view.update_files
  view.update_files = function(self, opts, cb)
    rebuild_index(root, real, idx, function() update(self, opts, cb) end)
  end
  -- The first listing can start before an update goes through the wrapper.
  M.build_index(root)
end

-- Views -------------------------------------------------------------------------

-- The tagged view, if one is open.
function M.current()
  if not package.loaded['diffview.lib'] then
    return nil
  end
  for _, v in ipairs(require('diffview.lib').views) do
    if v.tabpage and api.nvim_tabpage_is_valid(v.tabpage) and vim.t[v.tabpage].diffview_scope then
      return v
    end
  end
end

-- diffview opens a view's first file asynchronously and does not check that
-- the view still exists when it finishes; closing it in that window raises
-- errors inside diffview. Give a view that is still loading a moment.
local function settle(view)
  vim.wait(1500, function()
    return not view.is_loading and (uv.now() - (view.weaver_listed_at or 0)) > 150
  end, 10)
end

-- The view's own close (not diffview.close(tabpage), which upstream diffview
-- treats as "tidy up stray views"). diffview-plus returns false when staged
-- edits in the view are unsaved.
local function close(view)
  settle(view)
  -- Leave the view's tab first, for the one you came from: closing the
  -- current tab fails (E445) while noice has a float open in it.
  if view.tabpage == api.nvim_get_current_tabpage() and #api.nvim_list_tabpages() > 1 then
    local last = fn.tabpagenr('#')
    pcall(vim.cmd, (last > 0 and last ~= fn.tabpagenr()) and ('tabnext ' .. last) or 'tabprevious')
  end
  local ok, closed = pcall(view.close, view)
  if ok and closed ~= false then
    pcall(require('diffview.lib').dispose_view, view)
  end
  if not ok then
    util.msg('could not close the review: ' .. tostring(closed), util.WARN)
    return false
  end
  if closed == false then
    util.msg('the review has unsaved staged edits; save or :DiffviewClose! first', util.WARN)
    return false
  end
  return true
end

local function refresh(view)
  if view.update_files then
    view:update_files()
  end
end

local function open_new(spec, ident)
  local lib = require('diffview.lib')
  local args, private_index = compare_args(spec.compare)
  args = vim.list_extend({ '-C' .. spec.root }, args)
  if spec.paths then
    args[#args + 1] = '--'
    vim.list_extend(args, spec.paths)
  end
  local view = lib.diffview_open(args)
  if not view then
    return nil
  end
  if view.tabpage and api.nvim_tabpage_is_valid(view.tabpage) then
    -- diffview reused an identical untagged view: adopt it.
    api.nvim_set_current_tabpage(view.tabpage)
    refresh(view)
  else
    if private_index then
      use_private_index(view, spec.root)
    end
    pcall(function()
      view.emitter:on('files_updated', function() view.weaver_listed_at = uv.now() end)
    end)
    view:open()
  end
  vim.t[view.tabpage].diffview_scope = ident
  vim.t[view.tabpage].diffview_context = spec.label
  pcall(function() require('lualine').refresh({ place = { 'statusline' } }) end)
  return view
end

-- Show SPEC as the one review view (see the top of this file). Returns the
-- view, or nil when it was closed or could not open.
function M.show(spec)
  if not M.available() then
    util.msg('diffview is not installed', util.WARN)
    return nil
  end
  local ident = identity(spec)
  local cur = M.current()
  if cur and vim.t[cur.tabpage].diffview_scope == ident then
    if cur.tabpage == api.nvim_get_current_tabpage() then
      close(cur)
      return nil
    end
    api.nvim_set_current_tabpage(cur.tabpage)
    refresh(cur)
    return cur
  end
  if cur and not close(cur) then
    return nil
  end
  return open_new(spec, ident)
end

function M.close()
  local cur = M.current()
  if cur then
    close(cur)
  end
end

-- Refresh the view if it is in the current tab (diffview refreshes a view
-- itself when you enter its tab).
function M.refresh()
  local cur = M.current()
  if cur and cur.tabpage == api.nvim_get_current_tabpage() then
    refresh(cur)
  end
end

-- The repository a key press is about: the view's when you are in it, else
-- the current tab's.
function M.root_here()
  local cur = M.current()
  if cur and cur.tabpage == api.nvim_get_current_tabpage() then
    return util.real(cur.adapter.ctx.toplevel)
  end
  return util.current_root()
end

-- gf: the file in the tab of its own worktree -----------------------------------

local function deepest_tab_for(path)
  local best, len
  for _, tab in ipairs(api.nvim_list_tabpages()) do
    if not util.diffview_view(tab) then
      local cwd = util.real(util.tab_cwd(tab))
      if cwd and util.under(path, cwd) and (not len or #cwd > len) then
        best, len = tab, #cwd
      end
    end
  end
  return best
end

-- Open the file under the cursor (diff window or file panel) at the same line
-- in the tab whose working directory holds it, else in a new tab rooted at
-- its worktree. The review stays open.
function M.goto_file()
  local view = require('diffview.lib').get_current_view()
  local file = view and view.infer_cur_file and view:infer_cur_file()
  if not file then
    return
  end
  local path = file.absolute_path
  if not path or not uv.fs_stat(path) then
    util.msg('not on disk: ' .. tostring(file.path), util.WARN)
    return
  end
  local cursor
  if file == view.cur_entry and view.cur_layout then
    local main = view.cur_layout:get_main_win()
    if main and main.id and api.nvim_win_is_valid(main.id) then
      cursor = api.nvim_win_get_cursor(main.id)
    end
  end
  pcall(function() file.layout:restore_winopts() end)
  local tab = deepest_tab_for(path)
  if tab then
    api.nvim_set_current_tabpage(tab)
    local win = util.window_for(tab, path)
    if win then
      api.nvim_set_current_win(win)
    else
      vim.cmd('vsplit')
    end
  else
    vim.cmd('tabnew')
    local top = view.adapter and view.adapter.ctx and view.adapter.ctx.toplevel
    if top then
      vim.cmd('silent tcd ' .. fn.fnameescape(top))
    end
  end
  if not util.same_path(api.nvim_buf_get_name(0), path) then
    vim.cmd('silent keepalt edit ' .. fn.fnameescape(path))
  end
  if cursor then
    cursor[1] = math.min(cursor[1], api.nvim_buf_line_count(0))
    pcall(api.nvim_win_set_cursor, 0, cursor)
    vim.cmd('normal! zz')
  end
end

-- Put gf into diffview's keymaps (it reads them each time it attaches).
function M.install_keymaps()
  local ok, config = pcall(require, 'diffview.config')
  local conf = ok and config.get_config()
  if not conf or not conf.keymaps or M._installed_for == conf then
    return
  end
  M._installed_for = conf
  for _, section in ipairs({ 'view', 'file_panel', 'file_history_panel' }) do
    local list = conf.keymaps[section]
    if type(list) == 'table' then
      table.insert(list, { 'n', 'gf', M.goto_file, { desc = 'Open the file in its worktree tab' } })
    end
  end
end

-- Hunks ---------------------------------------------------------------------------

local function unquote(p)
  local q = p:match('^"(.*)"$')
  if not q then
    return p
  end
  q = q:gsub('\\(%d%d%d)', function(o) return string.char(tonumber(o, 8)) end)
  return (q:gsub('\\(.)', { t = '\t', n = '\n', ['"'] = '"', ['\\'] = '\\' }))
end

-- `git diff -U0` output as quickfix items, one per hunk.
function M.parse_hunks(root, out)
  local items, path = {}, nil
  local lines = vim.split(out or '', '\n', { plain = true })
  local i = 1
  while i <= #lines do
    local plus = lines[i]:match('^%+%+%+ (.*)$')
    if plus then
      path = plus ~= '/dev/null' and unquote(plus):gsub('^b/', '', 1) or nil
    else
      local c, d = lines[i]:match('^@@ %-[%d,]+ %+(%d+),?(%d*) @@')
      if c and path then
        c, d = tonumber(c), tonumber(d ~= '' and d or '1')
        local text, removed = nil, 0
        local j = i + 1
        while j <= #lines and not lines[j]:match('^@@') and not lines[j]:match('^diff ') do
          local sign = lines[j]:sub(1, 1)
          if sign == '+' and not text then
            text = vim.trim(lines[j]:sub(2))
          elseif sign == '-' then
            removed = removed + 1
          end
          j = j + 1
        end
        items[#items + 1] = {
          filename = root .. '/' .. path,
          lnum = math.max(1, d == 0 and c + 1 or c),
          text = d == 0 and ('(%d line%s removed)'):format(removed, removed == 1 and '' or 's') or text or '',
        }
        i = j - 1
      end
    end
    i = i + 1
  end
  return items
end

-- Every hunk of SPEC's comparison, as quickfix items.
function M.hunks(spec)
  local base = compare_base(spec.root, spec.compare)
  local env
  local args = { '-c', 'core.quotePath=false', 'diff', '-U0', '--no-color', '--no-ext-diff', '--no-renames' }
  if spec.compare.kind == 'commits' then
    vim.list_extend(args, { spec.compare.from, spec.compare.to })
  else
    local idx = M.build_index(spec.root)
    env = idx and { GIT_INDEX_FILE = idx } or nil
    args[#args + 1] = base
  end
  if spec.paths then
    args[#args + 1] = '--'
    vim.list_extend(args, spec.paths)
  end
  return M.parse_hunks(spec.root, util.git(spec.root, args, { env = env }))
end

function M.setup()
  if package.loaded['diffview.config'] then
    M.install_keymaps()
  end
  api.nvim_create_autocmd('User', {
    group = api.nvim_create_augroup('weaver_diffview', { clear = true }),
    pattern = { 'LazyLoad', 'DiffviewViewOpened' },
    callback = function(ev)
      if ev.match == 'DiffviewViewOpened' or (type(ev.data) == 'string' and ev.data:match('^diffview')) then
        M.install_keymaps()
      end
    end,
  })
end

return M
