# Bootstrap definition. Stable definitions replace this file after the first unified release.
class Wirectl < Formula
  desc "Local APIs and CLI for encrypted connections, downloads and voice"
  homepage "https://github.com/k0ngk0ng/wire-tools"
  head "https://github.com/k0ngk0ng/wire-tools.git", branch: "main"
  license "MIT"
  depends_on "go" => :build

  def install
    system "go", "build", "-trimpath", "-o", bin/"wirectl", "./cmd/wirectl"
    (bin/".wirectl-package-manager").write("homebrew\n")
  end

  test do
    assert_match "download", shell_output("#{bin}/wirectl --help")
  end
end
