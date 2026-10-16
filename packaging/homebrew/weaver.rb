# weaver's Homebrew formula, for a tap. Bump `revision` (and `version`) to
# release.
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
    system "go", "build", *std_go_args(output: bin/"wvr", ldflags: "-s -w -X main.version=#{version}"), "./cmd/wvr"
  end

  def caveats
    <<~EOS
      To have Claude Code (and Codex) report to weaver, add its hooks:
        wvr setup
      Then run `wvr` to start its tmux session.
    EOS
  end

  test do
    assert_match version.to_s, shell_output("#{bin}/wvr version")
  end
end
