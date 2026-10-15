# weaver's Homebrew formula, for a tap (robbowland/homebrew-kegs, beside
# gh-dash, gitui and serie). Bump `revision` (and `version`) to release.
# Built and tested from a local tap on 2026-10-15; brew clones without your
# git credentials, so the repository must be public for a tap to fetch it.
class Weaver < Formula
  desc "Coding agents side by side in tmux: pick up whichever thread needs you"
  homepage "https://github.com/robbowland/weaver"
  url "https://github.com/robbowland/weaver.git",
      branch: "main",
      revision: "2774982d55c5ea6a98e01b037b0631add9083ccd"
  version "0.1.0"

  depends_on "go" => :build
  depends_on "fzf"
  depends_on "neovim"
  depends_on "tmux"

  def install
    system "go", "build", *std_go_args(ldflags: "-s -w"), "./cmd/weaver"
  end

  def caveats
    <<~EOS
      To have Claude Code (and Codex) report to weaver, add its hooks:
        weaver setup
      Then run `weaver` to start its tmux session.
    EOS
  end

  test do
    assert_match "usage: weaver", shell_output("#{bin}/weaver help")
  end
end
