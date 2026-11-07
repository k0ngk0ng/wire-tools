class WireTalk < Formula
  desc "Direct encrypted microphone and speaker conversations"
  homepage "https://github.com/k0ngk0ng/wire-talk"
  version "0.3.1"
  license "MIT"
  depends_on "k0ngk0ng/tap/wirectl"
  on_macos do
    on_arm do
      url "https://github.com/k0ngk0ng/wire-talk/releases/download/v0.3.1/wire-talk-0.3.1-darwin-arm64.tar.gz"
      sha256 "bb327e21cf471ae48057de1a5af44b566dd3f956b4d4620e0b8e999e39661467"
    end
    on_intel do
      url "https://github.com/k0ngk0ng/wire-talk/releases/download/v0.3.1/wire-talk-0.3.1-darwin-amd64.tar.gz"
      sha256 "c51640fdadedc54c8cabd2e34018bfb4c9d23d1a8aeb1d170ae31dddf1065624"
    end
  end
  on_linux do
    on_arm do
      url "https://github.com/k0ngk0ng/wire-talk/releases/download/v0.3.1/wire-talk-0.3.1-linux-arm64.tar.gz"
      sha256 "a25da87b4399637d6be8dcc5e532df74c9671ca7650ff35b4c63aa3618cf9c73"
    end
    on_intel do
      url "https://github.com/k0ngk0ng/wire-talk/releases/download/v0.3.1/wire-talk-0.3.1-linux-amd64.tar.gz"
      sha256 "347ff402fa733d7245fb6d6094474d1c22726990260d9b40510768998206aae7"
    end
  end
  def install
    bin.install "bin/wirectl-talk"
    (bash_completion/"wirectl-talk").write Utils.safe_popen_read(bin/"wirectl-talk", "completion", "bash")
    (zsh_completion/"_wirectl-talk").write Utils.safe_popen_read(bin/"wirectl-talk", "completion", "zsh")
    (bin/".wire-talk-package-manager").write "homebrew\n"
    doc.install "README.md", "THIRD_PARTY_NOTICES.md"
    doc.install "licenses"
  end
  test do
    assert_match version.to_s, shell_output("#{bin}/wirectl-talk version")
    assert_path_exists bash_completion/"wirectl-talk"
    assert_path_exists zsh_completion/"_wirectl-talk"
    system bin/"wirectl-talk", "init", "--state-dir", testpath/"state"
    assert_path_exists testpath/"state/config.json"
  end
end
