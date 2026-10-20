-- The demo's Neovim: the tests' hermetic config, without the tab line (a
-- review opens in a tab of its own).
dofile(os.getenv('WEAVER_TEST_INIT'))
vim.o.showtabline = 0
