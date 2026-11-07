class WireConnect < Formula
  desc "Encrypted peer-to-peer networking with NAT traversal and relay fallback"
  homepage "https://github.com/k0ngk0ng/wire-connect"
  version "1.4.2"
  license "MIT"

  on_macos do
    depends_on arch: :arm64

    url "https://github.com/k0ngk0ng/wire-connect/releases/download/v1.4.2/wire-connect-1.4.2-darwin-arm64.tar.gz"
    sha256 "c6e2d0b8c53274c976867cc19d1f7ba9f4cdf9c0e1f3c8fbcd7f3451c312e840"
  end

  on_linux do
    on_arm do
      url "https://github.com/k0ngk0ng/wire-connect/releases/download/v1.4.2/wire-connect-1.4.2-linux-arm64.tar.gz"
      sha256 "d71a16bbd2859bff12e57c2a5df8bab62dcb290f6b4c292c0877c739b4221033"
    end
    on_intel do
      url "https://github.com/k0ngk0ng/wire-connect/releases/download/v1.4.2/wire-connect-1.4.2-linux-amd64.tar.gz"
      sha256 "fde8bad9e586831a225cbc8ca30867e89d23f1bfe47d73aabc6a2bc47b905da9"
    end
  end

  depends_on "k0ngk0ng/tap/wirectl"

  def install
    libexec.install "bin/wirectl-connect"
    (libexec/".wire-connect-package-manager").write "homebrew\n"
    bin.install_symlink libexec/"wirectl-connect"
    generate_completions_from_executable(bin/"wirectl-connect", "completion",
                                         base_name: "wirectl", shells: [:bash])
    bash_completion.install_symlink bash_completion/"wirectl" => "wirectl-connect"
    completion = Utils.safe_popen_read(bin/"wirectl-connect", "completion", "zsh")
    (zsh_completion/"_wirectl").write "#compdef wirectl wirectl-connect\n#{completion}\n_wirectl_connect_completion \"$@\"\n"
  end

  def caveats
    <<~EOS
      Run as your regular user after install or upgrade:
        wirectl connect setup
        wirectl connect resume
      setup requests administrator authorization for the network helper.
      resume uses an existing pair. Pair new devices before using resume.
    EOS
  end

  test do
    assert_equal version.to_s, shell_output("#{bin}/wirectl-connect version").strip
    output = shell_output("#{bin}/wirectl-connect update 2>&1", 1)
    assert_match "brew upgrade k0ngk0ng/tap/wire-connect", output
  end
end
