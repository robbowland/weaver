-- Hermetic Neovim config for weaver's tests: none of your own config, only
-- the plugins weaver works with, taken from lazy.nvim's plugin directory when
-- they are there (or from $WEAVER_TEST_PLUGINS). test/run.sh points VIMINIT
-- here unless WEAVER_TEST_USER_NVIM=1.

vim.g.mapleader = ' '
vim.o.shortmess = vim.o.shortmess .. 'I'
vim.o.more = false
vim.o.updatetime = 300

local dirs = {}
local env = os.getenv('WEAVER_TEST_PLUGINS')
if env and env ~= '' then
  dirs[#dirs + 1] = env
end
dirs[#dirs + 1] = vim.fn.stdpath('data') .. '/lazy'
dirs[#dirs + 1] = (os.getenv('HOME') or '') .. '/.local/share/nvim/lazy'

local function find(names)
  for _, dir in ipairs(dirs) do
    for _, name in ipairs(names) do
      local path = dir .. '/' .. name
      if vim.fn.isdirectory(path .. '/lua') == 1 then
        return path
      end
    end
  end
end

-- diffview-plus first: weaver's review surface needs its APIs.
local plugins = {
  diffview = find({ 'diffview-plus.nvim', 'diffview.nvim' }),
  gitsigns = find({ 'gitsigns.nvim' }),
}
vim.g.weaver_test_plugins = plugins
for _, path in pairs(plugins) do
  vim.opt.runtimepath:append(path)
end

if plugins.gitsigns then
  pcall(function() require('gitsigns').setup({}) end)
end
