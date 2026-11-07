class WireDownload < Formula
  desc "HTTP, BitTorrent, eD2k, X and YouTube downloads with bundled engines"
  homepage "https://github.com/k0ngk0ng/wire-download"
  version "0.3.2"

  on_macos do
    depends_on macos: :ventura
    depends_on arch: :arm64

    url "https://github.com/k0ngk0ng/wire-download/releases/download/v0.3.2/wire-download-0.3.2-darwin-arm64.tar.gz"
    sha256 "3081eea1ab73e2490c41065cf93f8b49f2bb415e9463baaa0e660a99104b2f75"
  end

  on_linux do
    on_arm do
      url "https://github.com/k0ngk0ng/wire-download/releases/download/v0.3.2/wire-download-0.3.2-linux-arm64.tar.gz"
      sha256 "4dc5a74d7afed2342f99e1136c4a68da0411f89599d53de6ea42af7abd60d3f6"
    end
    on_intel do
      url "https://github.com/k0ngk0ng/wire-download/releases/download/v0.3.2/wire-download-0.3.2-linux-amd64.tar.gz"
      sha256 "51694491f156b41a53331e5357429f0a2729272a488ba1634a7d598aa9f41272"
    end
  end

  depends_on "k0ngk0ng/tap/wirectl"

  def install
    bin.install "bin/wirectl-download"
    libexec.install "libexec/wirectl-download"
    pkgshare.install "README.md", "deploy", "licenses"
    bash_script = Utils.safe_popen_read(bin/"wirectl-download", "completion", "bash")
    bash_script = bash_script.lines.reject { |line| line.start_with?("# bash completion", "# Install with:") || line == "complete -o filenames -F _wirectl_download_completion wirectl\n" }.join
    (bash_completion/"wirectl-download").write bash_script
    zsh_script = Utils.safe_popen_read(bin/"wirectl-download", "completion", "zsh")
    zsh_script = zsh_script.lines.reject { |line| line.start_with?("#compdef ") || line == "compdef _wirectl_download_completion wirectl\n" }.join
    (zsh_completion/"_wirectl-download").write "#compdef wirectl-download\n#{zsh_script}\n_wirectl_download_completion \"$@\"\n"
    fish_script = Utils.safe_popen_read(bin/"wirectl-download", "completion", "fish")
    fish_script = fish_script.lines.reject { |line| line.start_with?("# fish completion") || line == "complete -c wirectl -f -a '(__wirectl_download_complete)'\n" }.join
    (fish_completion/"wirectl-download.fish").write fish_script
  end

  service do
    run [opt_bin/"wirectl-download", "daemon", "run"]
    keep_alive true
  end

  def caveats
    <<~EOS
      Initialize the download state before starting the daemon:
        wirectl download init
      Stop the service before upgrading this formula:
        brew services stop wire-download
      If started manually, use wirectl download daemon stop instead.
      Manage the background daemon with:
        brew services start wire-download
        brew services stop wire-download
    EOS
  end

  test do
    assert_equal version.to_s, shell_output("#{bin}/wirectl-download version").strip
    assert_match "Usage:", shell_output("#{bin}/wirectl-download --help")
    state = testpath/"state"
    downloads = testpath/"downloads"
    mkdir_p downloads
    system bin/"wirectl-download", "--data-dir", state, "init", "--downloads", downloads
    doctor = shell_output("#{bin}/wirectl-download --data-dir #{state} doctor")
    %w[aria2c amuled amulecmd yt-dlp ffmpeg deno].each do |engine|
      assert_match (prefix/"libexec/wirectl-download/bin/#{engine}").to_s, doctor
    end
  end
end
