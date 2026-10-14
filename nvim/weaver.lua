-- weaver.lua: weaver's side of the nvim bound to an agent. `weaver nvim`
-- sources it with `-c "luafile …"`, after the user's own config.
--
-- The modules live beside it in lua/weaver/. Sourcing this file again
-- reloads them.

local dir = vim.fn.fnamemodify(debug.getinfo(1, 'S').source:sub(2), ':p:h')
if not vim.tbl_contains(vim.opt.runtimepath:get(), dir) then
  vim.opt.runtimepath:append(dir)
end

if package.loaded.weaver then
  pcall(package.loaded.weaver.teardown)
end
for name in pairs(package.loaded) do
  if name == 'weaver' or name:sub(1, 7) == 'weaver.' then
    package.loaded[name] = nil
  end
end

require('weaver').setup()
