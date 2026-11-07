class Wirectl < Formula
  desc "Small command host for independently installed wirectl plugins"
  homepage "https://github.com/k0ngk0ng/wirectl"
  version "0.2.3"

  on_macos do
    on_arm do
      url "https://github.com/k0ngk0ng/wirectl/releases/download/v0.2.3/wirectl-0.2.3-darwin-arm64.tar.gz"
      sha256 "c51c03cafbef54d8191b630542a1312184dd908126d998b8e863192fff822b99"
    end
    on_intel do
      url "https://github.com/k0ngk0ng/wirectl/releases/download/v0.2.3/wirectl-0.2.3-darwin-amd64.tar.gz"
      sha256 "b99835b78710c08a41d566f9f65c0d8616aecbc043fe83c2bb39f40995ad0557"
    end
  end

  on_linux do
    on_arm do
      url "https://github.com/k0ngk0ng/wirectl/releases/download/v0.2.3/wirectl-0.2.3-linux-arm64.tar.gz"
      sha256 "b547fe0692b16d1a509d8c8b834b76653f913abc58d6ea51ea77413f0e7490d8"
    end
    on_intel do
      url "https://github.com/k0ngk0ng/wirectl/releases/download/v0.2.3/wirectl-0.2.3-linux-amd64.tar.gz"
      sha256 "d80fe8036a13f2b861ccf15b9a9796a635f2f48e5482d411cecab4d4ed9170cb"
    end
  end

  def install
    bin.install "bin/wirectl"
  end

  test do
    assert_equal version.to_s, shell_output("#{bin}/wirectl version").strip
    assert_match "Usage: wirectl", shell_output("#{bin}/wirectl --help")
  end
end
