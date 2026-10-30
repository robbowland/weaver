-- Review in place: the branch's changes in your own buffers, not a diff
-- view. Every buffer of the worktree compares against the branch's root
-- (its merge-base with the base branch) in gitsigns, so its signs and your
-- hunk keys show all the branch changed, committed or not; the quickfix
-- list holds every change, for ]q and [q across files.
--
--   :AgentInPlace          the branch, file by file (<leader>ai)
--   :AgentInPlace commits  the same, stepped commit by commit, oldest first,
--                          then what is not committed (<leader>aI)
--   the same again, or :AgentInPlace off, ends it
--
-- Commit by commit, each commit's changes are where they are in the file
-- now; one a later change rewrote is marked so. The exact commit is a
-- review view away. The signs are the branch's in both. Nothing opens or
-- moves on its own, and only this review's own list is ever refreshed.
local M = {}

local api, fn = vim.api, vim.fn
local util = require('weaver.util')
local dv = require('weaver.diffview')

-- While on: { root, base (the merge-base), base_name, mode, bufs (buffers
-- given the base), head (HEAD when last read), chain (commit mode: the
-- branch's changes on HEAD's lines), qf (the list's id), busy }.
local state

local DIFF = { '-c', 'core.quotePath=false', 'diff', '-U0', '--no-color', '--no-ext-diff', '-M' }

-- gitsigns ------------------------------------------------------------------

local function gitsigns()
  local ok, gs = pcall(require, 'gitsigns')
  return ok and gs or nil
end

-- gitsigns' own record of BUF, once it has attached.
local function gs_buffer(buf)
  local ok, c = pcall(require, 'gitsigns.cache')
  return ok and type(c.cache) == 'table' and c.cache[buf] or nil
end

-- The worktree gitsigns has BUF in, once it has attached.
local function root_of(buf)
  local st = vim.b[buf].gitsigns_status_dict
  return type(st) == 'table' and type(st.root) == 'string' and util.real(st.root) or nil
end

local function set_base(buf, want)
  local gs, b = gitsigns(), gs_buffer(buf)
  if not gs or not b or not api.nvim_buf_is_loaded(buf) or b.git_obj.revision == want then
    return
  end
  api.nvim_buf_call(buf, function() pcall(gs.change_base, want, false) end)
end

-- Give BUF the base it should have now: the branch's root when it is in the
-- worktree under review; the index again when it was and is not. A buffer
-- never under review keeps whatever base it has.
local function apply(buf)
  if state and root_of(buf) == state.root then
    state.bufs[buf] = true
    set_base(buf, state.base)
  elseif state and state.bufs[buf] then
    state.bufs[buf] = nil
    set_base(buf, nil)
  end
end

-- Once the bases are set: gitsigns drops an update asked for while one is
-- running (its watcher's, after a commit), and holds a hidden buffer's
-- until it is shown; a refresh redoes every buffer, each with its own base.
local function refresh_signs()
  vim.defer_fn(function()
    local gs = gitsigns()
    if gs then
      pcall(gs.refresh)
    end
  end, 300)
end

-- Diffs ---------------------------------------------------------------------

-- Line l of a file as a -U0 diff's old side had it, on its new side: hunks
-- are { old_start, old_count, new_start, new_count }, in order. The line,
-- and whether the diff changed that line itself.
function M.map_line(l, hunks)
  local offset = 0
  for _, h in ipairs(hunks or {}) do
    local os, oc, ns, nc = h[1], h[2], h[3], h[4]
    if oc == 0 then
      -- Lines added after old line os.
      if l <= os then
        break
      end
      offset = offset + nc
    else
      if l < os then
        break
      end
      if l < os + oc then
        return math.max(1, nc == 0 and ns + 1 or ns), true
      end
      offset = offset + nc - oc
    end
  end
  return l + offset, false
end

-- Whether hunks change any of the COUNT lines from FIRST on (old side), or
-- add lines among them.
function M.touches(first, count, hunks)
  if count == 0 then
    return false
  end
  local last = first + count - 1
  for _, h in ipairs(hunks or {}) do
    local os, oc = h[1], h[2]
    if oc == 0 then
      if os >= first and os < last then
        return true
      end
    elseif os <= last and os + oc - 1 >= first then
      return true
    end
  end
  return false
end

-- `git diff -U0 -M` (or a commit's, from `git log -p`) as files { old, new
-- (nil for none), hunks }, each hunk { old_start, old_count, new_start,
-- new_count, text (its first added line), removed }. A file's `---`, `+++`
-- and rename lines are read only in its header: in a hunk, a removed
-- "-- x" reads as "--- x".
function M.parse_patch(out)
  local files, f, header = {}, nil, false
  for _, line in ipairs(vim.split(out or '', '\n', { plain = true })) do
    if line:match('^diff ') then
      f = { hunks = {} }
      files[#files + 1] = f
      header = true
    elseif f and header and line:match('^%-%-%- ') then
      local p = line:sub(5)
      f.old = p ~= '/dev/null' and util.diff_path(p):gsub('^a/', '', 1) or nil
    elseif f and header and line:match('^%+%+%+ ') then
      local p = line:sub(5)
      f.new = p ~= '/dev/null' and util.diff_path(p):gsub('^b/', '', 1) or nil
    elseif f and header and line:match('^rename from ') then
      f.old = util.diff_path(line:sub(13))
    elseif f and header and line:match('^rename to ') then
      f.new = util.diff_path(line:sub(11))
    elseif f then
      local os, oc, ns, nc = line:match('^@@ %-(%d+),?(%d*) %+(%d+),?(%d*) @@')
      if os then
        header = false
        local h = { tonumber(os), tonumber(oc ~= '' and oc or '1'), tonumber(ns), tonumber(nc ~= '' and nc or '1'), removed = 0 }
        f.hunks[#f.hunks + 1] = h
      elseif not header and #f.hunks > 0 then
        local h, sign = f.hunks[#f.hunks], line:sub(1, 1)
        if sign == '+' and not h.text then
          h.text = vim.trim(line:sub(2))
        elseif sign == '-' then
          h.removed = h.removed + 1
        end
      end
    end
  end
  return files
end

local function hunk_text(h)
  if h[4] == 0 then
    return ('(%d line%s removed)'):format(h.removed, h.removed == 1 and '' or 's')
  end
  return h.text or ''
end

local function short(s, n)
  s = vim.trim(s or '')
  return fn.strchars(s) > n and (fn.strcharpart(s, 0, n - 1) .. '…') or s
end

-- The new side's hunks of FILES as quickfix items, their text after PREFIX.
local function hunk_items(root, files, prefix)
  local items = {}
  for _, f in ipairs(files) do
    if f.new then
      for _, h in ipairs(f.hunks) do
        items[#items + 1] = {
          filename = root .. '/' .. f.new,
          lnum = math.max(1, h[4] == 0 and h[3] + 1 or h[3]),
          text = prefix .. hunk_text(h),
        }
      end
    end
  end
  return items
end

-- The branch's commits, from `git log -p` (oldest first, first parents
-- only), as changes on HEAD's lines: { path, lnum, count, text, label,
-- changed }. Each commit moves the changes before it; a merge lists none of
-- its own (what the other side brought).
function M.chain(out)
  local items = {}
  for rec in (out or ''):gmatch('[^\30]+') do
    local head, patch = rec:match('^([^\n]*)\n?(.*)$')
    local abbrev, parents, subject = head:match('^%x+\31([^\31]*)\31([^\31]*)\31(.*)$')
    if abbrev then
      local files = M.parse_patch(patch)
      local by_old = {}
      for _, f in ipairs(files) do
        if f.old then
          by_old[f.old] = f
        end
      end
      local moved = {}
      for _, it in ipairs(items) do
        local f = by_old[it.path]
        if not f then
          moved[#moved + 1] = it
        elseif f.new then
          it.changed = it.changed or M.touches(it.lnum, it.count, f.hunks)
          it.lnum = (M.map_line(it.lnum, f.hunks))
          it.path = f.new
          moved[#moved + 1] = it
        end
      end
      items = moved
      if not vim.trim(parents):find(' ') then
        local label = abbrev .. ' ' .. short(subject, 40)
        for _, f in ipairs(files) do
          for _, h in ipairs(f.new and f.hunks or {}) do
            items[#items + 1] = { path = f.new, lnum = h[4] == 0 and h[3] + 1 or h[3], count = h[4], text = hunk_text(h), label = label }
          end
        end
      end
    end
  end
  return items
end

-- CHAIN's changes on today's lines, through NOW (HEAD to the worktree), as
-- quickfix items.
local function place(root, chain, now)
  local by_old = {}
  for _, f in ipairs(now) do
    if f.old then
      by_old[f.old] = f
    end
  end
  local items = {}
  for _, it in ipairs(chain or {}) do
    local f, path, lnum, changed = by_old[it.path], it.path, it.lnum, it.changed
    if not f or f.new then
      if f then
        changed = changed or M.touches(lnum, it.count, f.hunks)
        lnum, path = M.map_line(lnum, f.hunks), f.new
      end
      items[#items + 1] = {
        filename = root .. '/' .. path,
        lnum = math.max(1, lnum),
        text = it.label .. ' │ ' .. (changed and '(changed since) ' or '') .. it.text,
      }
    end
  end
  return items
end

local function log_args(base)
  return {
    '-c',
    'core.quotePath=false',
    'log',
    '--reverse',
    '--first-parent',
    '--diff-merges=first-parent',
    '-p',
    '-U0',
    '-M',
    '--no-color',
    '--no-ext-diff',
    '--format=%x1e%H%x1f%h%x1f%P%x1f%s',
    base .. '..HEAD',
  }
end

-- The review's changes, read without waiting: CB(items), or CB(nil) when
-- git could not tell; dropped once the review is off or replaced. A commit
-- (HEAD moved) reads the merge-base and, commit by commit, the commits
-- again; otherwise one diff does.
local function gather(s, cb)
  local root = s.root
  local function live()
    return state == s
  end
  local function lists()
    dv.with_index(root, function(idx)
      if not live() then
        return
      end
      local against = s.mode == 'commits' and 'HEAD' or s.base
      util.git_async(root, vim.list_extend(vim.deepcopy(DIFF), { against }), function(diff)
        if not live() then
          return
        end
        if not diff then
          return cb(nil)
        end
        local now = M.parse_patch(diff)
        if s.mode ~= 'commits' then
          return cb(hunk_items(root, now, ''))
        end
        local items = place(root, s.chain, now)
        cb(vim.list_extend(items, hunk_items(root, now, 'uncommitted │ ')))
      end, { env = idx and { GIT_INDEX_FILE = idx } or nil })
    end)
  end
  util.git_async(root, { 'rev-parse', 'HEAD' }, function(out)
    if not live() then
      return
    end
    local head = util.first_line(out)
    if head == s.head and (s.chain or s.mode ~= 'commits') then
      return lists()
    end
    s.head = head
    util.git_async(root, { 'merge-base', s.base_name, 'HEAD' }, function(mb)
      if not live() then
        return
      end
      mb = util.first_line(mb)
      if mb and mb ~= s.base then
        -- Rebased, or the base branch merged in: a new root.
        s.base = mb
        for buf in pairs(s.bufs) do
          apply(buf)
        end
        refresh_signs()
      end
      if s.mode ~= 'commits' then
        return lists()
      end
      util.git_async(root, log_args(s.base), function(log)
        if not live() then
          return
        end
        s.chain = M.chain(log)
        lists()
      end)
    end)
  end)
end

-- The quickfix list ---------------------------------------------------------

local function title(s)
  return ('in place: %s vs %s%s'):format(fn.fnamemodify(s.root, ':t'), s.base_name, s.mode == 'commits' and ', commit by commit' or '')
end

-- Whether S's list is still there.
local function has_list(s)
  return s.qf ~= nil and fn.getqflist({ id = s.qf }).id == s.qf
end

-- S's list with ITEMS: a new list when it starts; then that list alone,
-- in place, wherever it is in the stack, keeping you on the same change.
local function fill(s, items, fresh)
  if fresh then
    fn.setqflist({}, ' ', { title = title(s), items = items })
    s.qf = fn.getqflist({ id = 0 }).id
    return
  end
  if not has_list(s) then
    return
  end
  local cur = fn.getqflist({ id = s.qf, idx = 0, items = 0 })
  local idx = math.min(math.max(cur.idx or 1, 1), math.max(#items, 1))
  local at = cur.items and cur.items[cur.idx]
  if at then
    local name = api.nvim_buf_get_name(at.bufnr)
    for i, it in ipairs(items) do
      if it.filename == name and it.text == at.text then
        idx = i
        break
      end
    end
  end
  fn.setqflist({}, 'r', { id = s.qf, items = items, idx = idx })
end

local function files_in(items)
  local seen, n = {}, 0
  for _, it in ipairs(items) do
    if not seen[it.filename] then
      seen[it.filename], n = true, n + 1
    end
  end
  return n
end

-- On and off ----------------------------------------------------------------

-- The worktree a review in place is of: the current file's, else the
-- review's (the agent's).
local function root_here()
  local name = api.nvim_buf_get_name(0)
  if name ~= '' and vim.bo.buftype == '' then
    local top = util.toplevel(fn.fnamemodify(name, ':p:h'))
    if top then
      return top
    end
  end
  return require('weaver.review').root()
end

function M.off()
  local s = state
  if not s then
    return
  end
  state = nil
  for buf in pairs(s.bufs) do
    set_base(buf, nil)
  end
  refresh_signs()
  if has_list(s) then
    fn.setqflist({}, 'r', { id = s.qf, title = 'in place: off', items = {} })
  end
  util.msg('in place: off')
end

-- :AgentInPlace [branch|commits|off]: MODE on, or off when it is on.
function M.toggle(mode)
  mode = (mode == nil or mode == '') and 'branch' or mode
  if mode == 'off' or state and state.mode == mode then
    return M.off()
  end
  if mode ~= 'branch' and mode ~= 'commits' then
    util.msg('in place: branch, commits or off', util.WARN)
    return
  end
  local root = root_here()
  if not root then
    util.msg('not in a git repository', util.WARN)
    return
  end
  local base_name = require('weaver.review').base(root)
  local base = base_name and util.first_line(util.git(root, { 'merge-base', base_name, 'HEAD' }))
  if not base then
    util.msg('no base branch found; set WEAVER_BASE or git config branch.<name>.weaverbase', util.WARN)
    return
  end
  local s = { root = root, base = base, base_name = base_name, mode = mode, bufs = state and state.bufs or {} }
  state = s
  for _, buf in ipairs(api.nvim_list_bufs()) do
    apply(buf)
  end
  refresh_signs()
  if not gitsigns() then
    util.msg('in place: gitsigns is not installed, so only the quickfix list shows the changes', util.WARN)
  end
  s.busy = true
  gather(s, function(items)
    s.busy = false
    if not items then
      util.msg('in place: git could not list the changes', util.WARN)
      return
    end
    fill(s, items, true)
    local files = files_in(items)
    util.msg(('%s · %d change%s in %d file%s · ]q steps'):format(title(s), #items, #items == 1 and '' or 's', files, files == 1 and '' or 's'))
  end)
end

-- Something changed (an agent's edit, a write): refresh the list, at most
-- every 500ms, one read at a time.
local queued = false
function M.poke()
  if not state or queued then
    return
  end
  queued = true
  vim.defer_fn(function()
    queued = false
    local s = state
    if not s or s.busy then
      return
    end
    s.busy = true
    gather(s, function(items)
      s.busy = false
      if items and state == s then
        fill(s, items, false)
      end
    end)
  end, 500)
end

function M.active()
  return state ~= nil
end

function M.setup()
  local group = api.nvim_create_augroup('weaver_inplace', { clear = true })
  -- A buffer gitsigns attaches to (opened, reloaded) or updates while on.
  api.nvim_create_autocmd('User', {
    group = group,
    pattern = 'GitSignsUpdate',
    callback = function(ev)
      local buf = type(ev.data) == 'table' and ev.data.buffer
      if state and buf then
        -- After gitsigns has done attaching.
        vim.schedule(function()
          if api.nvim_buf_is_valid(buf) then
            apply(buf)
          end
        end)
      end
    end,
  })
  api.nvim_create_autocmd('BufWritePost', { group = group, callback = function() M.poke() end })
end

function M.teardown()
  M.off()
  pcall(api.nvim_del_augroup_by_name, 'weaver_inplace')
end

return M
