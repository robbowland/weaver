-- weaver.lua: weaver's side of the nvim bound to an agent. `weaver nvim`
-- sources it with `-c "luafile …"`, after the user's own config.
--
-- The modules live beside it in lua/weaver/. Sourcing this file again
-- reloads them.

-- First on the runtimepath, so its modules are the ones `require` finds even
-- when the user's config also loads a copy of weaver's plugin (perhaps an
-- older one).
local dir = vim.fn.fnamemodify(debug.getinfo(1, 'S').source:sub(2), ':p:h')
vim.opt.runtimepath:remove(dir)
vim.opt.runtimepath:prepend(dir)

if package.loaded.weaver then
  pcall(package.loaded.weaver.teardown)
end
for name in pairs(package.loaded) do
  if name == 'weaver' or name:sub(1, 7) == 'weaver.' then
    package.loaded[name] = nil
  end
end

-- The plugins weaver fetches for its review (`wvr` keeps them in
-- $WEAVER_PLUGINS), for a config that has none of its own: diffview-plus.
local function has_plugin(names)
  for _, n in ipairs(names) do
    if #vim.api.nvim_get_runtime_file('lua/' .. n .. '/init.lua', false) > 0 then
      return true
    end
  end
  local ok, lazy = pcall(require, 'lazy.core.config')
  if ok and lazy.plugins then
    for name in pairs(lazy.plugins) do
      if name:match('^diffview') then
        return true
      end
    end
  end
  return false
end
local plugins = vim.env.WEAVER_PLUGINS
if plugins and plugins ~= '' and not has_plugin({ 'diffview' }) then
  local p = plugins .. '/diffview-plus.nvim'
  if vim.uv.fs_stat(p .. '/lua/diffview') then
    vim.opt.runtimepath:append(p)
    pcall(vim.cmd.runtime, 'plugin/diffview.lua')
  end
end

require('weaver').setup()
