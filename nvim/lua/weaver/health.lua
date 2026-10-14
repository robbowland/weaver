-- :checkhealth weaver
local M = {}

local health = vim.health
local util = require('weaver.util')

local function git_version()
  local out = vim.fn.system({ 'git', '--version' })
  local major, minor = out:match('(%d+)%.(%d+)')
  return tonumber(major), tonumber(minor), vim.trim(out)
end

function M.check()
  health.start('weaver')

  local major, minor, version = git_version()
  if major and (major > 2 or (major == 2 and minor >= 31)) then
    health.ok(version)
  else
    health.error(version .. ': git 2.31 or later is needed (rev-parse --path-format)')
  end

  local ok_dv = pcall(require, 'diffview')
  if not ok_dv then
    health.warn('diffview is not installed: reviews fall back to quickfix', {
      'Install dlyongemallo/diffview-plus.nvim',
    })
  elseif require('diffview.scene.views.diff.diff_view').set_revs then
    health.ok('diffview-plus')
  else
    health.warn('diffview is the unmaintained upstream: reviews work, but diffview-plus is what weaver is tested with')
  end

  if type(_G.Snacks) == 'table' and _G.Snacks.picker then
    health.ok('snacks picker for :AgentTurns')
  else
    health.info(':AgentTurns uses vim.ui.select (no snacks picker)')
  end

  local bin = util.weaver_bin()
  if bin then
    health.ok('weaver script: ' .. bin)
  else
    health.warn('the weaver script is not found: :AgentReviewed cannot mark reviewed')
  end

  if not package.loaded['weaver.status'] then
    health.info('weaver is not loaded in this nvim (only nvims weaver opens load it)')
    return
  end
  local state = vim.env.WEAVER_STATE
  if not state or state == '' then
    health.info('not bound to an agent (no WEAVER_STATE)')
  elseif vim.fn.filereadable(state) == 0 then
    health.warn('the agent state file is missing: ' .. state)
  else
    local agent = require('weaver.status').agent
    health.ok(('bound to an agent: session %s, %s'):format(agent.sid or '?', agent.state or 'no state yet'))
  end
  if vim.v.servername ~= '' then
    health.ok('listening on ' .. vim.v.servername)
  else
    health.error('not listening: the agent cannot reach this nvim')
  end
end

return M
