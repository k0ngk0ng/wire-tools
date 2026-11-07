#!/usr/bin/env python3
"""Render formulas from independently checked official GitHub release metadata."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
PACKAGES = {
    "wirectl": {"minimum": (0, 2, 3), "targets": ("darwin-arm64", "darwin-amd64", "linux-arm64", "linux-amd64")},
    "wire-connect": {"minimum": (1, 2, 1), "targets": ("darwin-arm64", "linux-arm64", "linux-amd64")},
    "wire-download": {"minimum": (0, 2, 2), "targets": ("darwin-arm64", "linux-arm64", "linux-amd64")},
}


def fetch(url, api=False):
    headers = {"User-Agent": "k0ngk0ng-homebrew-tap", "Accept": "application/vnd.github+json" if api else "application/octet-stream"}
    if api and os.environ.get("GH_TOKEN"):
        headers["Authorization"] = "Bearer " + os.environ["GH_TOKEN"]
    with urllib.request.urlopen(urllib.request.Request(url, headers=headers), timeout=30) as response:
        data = response.read(1024 * 1024 + 1)
    if len(data) > 1024 * 1024:
        raise ValueError("release metadata is too large")
    return data


def sha256(asset):
    digest = asset.get("digest", "")
    if not re.fullmatch(r"sha256:[0-9a-f]{64}", digest):
        raise ValueError("GitHub asset must have a SHA-256 digest")
    return digest[7:]


def release(package, specification):
    metadata = json.loads(fetch(f"https://api.github.com/repos/k0ngk0ng/{package}/releases/latest", api=True))
    tag = metadata["tag_name"]
    if metadata.get("draft") or metadata.get("prerelease") or not re.fullmatch(r"v\d+\.\d+\.\d+", tag):
        raise ValueError("only stable versioned releases are packaged")
    version = tag[1:]
    if tuple(map(int, version.split("."))) < specification["minimum"]:
        raise ValueError(f"{package} requires at least {specification['minimum']}")
    assets = {asset["name"]: asset for asset in metadata["assets"]}
    if len(assets) != len(metadata["assets"]):
        raise ValueError("duplicate release asset names")
    base = f"https://github.com/k0ngk0ng/{package}/releases/download/{tag}/"
    sums_asset = assets["SHA256SUMS"]
    if sums_asset["browser_download_url"] != base + "SHA256SUMS":
        raise ValueError("unexpected checksums download origin")
    sums_bytes = fetch(base + "SHA256SUMS")
    if hashlib.sha256(sums_bytes).hexdigest() != sha256(sums_asset):
        raise ValueError("SHA256SUMS differs from its GitHub digest")
    sums = {}
    for line in sums_bytes.decode("utf-8").splitlines():
        match = re.fullmatch(r"([0-9a-f]{64})\s+\*?(?:\./)?([^/\\]+)", line)
        if not match or match[2] in sums:
            raise ValueError("invalid or duplicate checksum entry")
        sums[match[2]] = match[1]
    result = {"version": version, "assets": {}}
    for target in specification["targets"]:
        filename = f"{package}-{version}-{target}.tar.gz"
        asset = assets[filename]
        if asset["browser_download_url"] != base + filename:
            raise ValueError("unexpected archive download origin")
        digest = sha256(asset)
        if digest != sums.get(filename):
            raise ValueError(f"independent checksum mismatch: {filename}")
        result["assets"][target] = {"url": base + filename, "sha256": digest}
    return result


def render(package, item):
    video_downloads = package == "wire-download" and tuple(map(int, item["version"].split("."))) >= (0, 3, 0)
    download_engines = "aria2c amuled amulecmd" + (" yt-dlp ffmpeg deno" if video_downloads else "")
    klass = {"wirectl": "Wirectl", "wire-connect": "WireConnect", "wire-download": "WireDownload"}[package]
    description = {
        "wirectl": "Small command host for independently installed wirectl plugins",
        "wire-connect": "Encrypted peer-to-peer networking with NAT traversal and relay fallback",
        "wire-download": "HTTP(S), eD2k and BitTorrent download daemon with bundled engines",
    }[package]
    if video_downloads:
        description = "HTTP, BitTorrent, eD2k, X and YouTube downloads with bundled engines"
    lines = [f"class {klass} < Formula", f'  desc "{description}"', f'  homepage "https://github.com/k0ngk0ng/{package}"', f'  version "{item["version"]}"']
    if package == "wire-connect":
        lines.append('  license "MIT"')
    lines += ["", "  on_macos do"]
    if package in ("wire-connect", "wire-download"):
        asset = item["assets"]["darwin-arm64"]
        if package == "wire-download":
            lines.append("    depends_on macos: :ventura")
        lines += ["    depends_on arch: :arm64", "", f'    url "{asset["url"]}"', f'    sha256 "{asset["sha256"]}"']
    else:
        for arch, target in (("arm", "darwin-arm64"), ("intel", "darwin-amd64")):
            asset = item["assets"][target]
            lines += [f"    on_{arch} do", f'      url "{asset["url"]}"', f'      sha256 "{asset["sha256"]}"', "    end"]
    lines += ["  end", "", "  on_linux do"]
    for arch, target in (("arm", "linux-arm64"), ("intel", "linux-amd64")):
        asset = item["assets"][target]
        lines += [f"    on_{arch} do", f'      url "{asset["url"]}"', f'      sha256 "{asset["sha256"]}"', "    end"]
    lines += ["  end", ""]
    if package == "wire-connect":
        lines += ['  depends_on "k0ngk0ng/tap/wirectl"', "", "  def install", '    libexec.install "bin/wirectl-connect"', '    (libexec/".wire-connect-package-manager").write "homebrew\\n"', '    bin.install_symlink libexec/"wirectl-connect"', '    generate_completions_from_executable(bin/"wirectl-connect", "completion",', '                                         base_name: "wirectl", shells: [:bash])', '    bash_completion.install_symlink bash_completion/"wirectl" => "wirectl-connect"', '    completion = Utils.safe_popen_read(bin/"wirectl-connect", "completion", "zsh")', '    (zsh_completion/"_wirectl").write "#compdef wirectl wirectl-connect\\n#{completion}\\n_wirectl_connect_completion \\\"$@\\\"\\n"', "  end", "", "  def caveats", '    <<~EOS', '      Run as your regular user after install or upgrade:', '        wirectl connect setup', '        wirectl connect resume', '      setup requests administrator authorization for the network helper.', '      resume uses an existing pair. Pair new devices before using resume.', '    EOS', "  end", "", "  test do", '    assert_equal version.to_s, shell_output("#{bin}/wirectl-connect version").strip', '    output = shell_output("#{bin}/wirectl-connect update 2>&1", 1)', '    assert_match "brew upgrade k0ngk0ng/tap/wire-connect", output', "  end"]
    elif package == "wire-download":
        lines += [
            '  depends_on "k0ngk0ng/tap/wirectl"',
            "",
            "  def install",
            '    bin.install "bin/wirectl-download"',
            '    libexec.install "libexec/wirectl-download"',
            '    pkgshare.install "README.md", "deploy", "licenses"',
            '    bash_script = Utils.safe_popen_read(bin/"wirectl-download", "completion", "bash")',
            '    bash_script = bash_script.lines.reject { |line| line.start_with?("# bash completion", "# Install with:") || line == "complete -o filenames -F _wirectl_download_completion wirectl\\n" }.join',
            '    (bash_completion/"wirectl-download").write bash_script',
            '    zsh_script = Utils.safe_popen_read(bin/"wirectl-download", "completion", "zsh")',
            '    zsh_script = zsh_script.lines.reject { |line| line.start_with?("#compdef ") || line == "compdef _wirectl_download_completion wirectl\\n" }.join',
            '    (zsh_completion/"_wirectl-download").write "#compdef wirectl-download\\n#{zsh_script}\\n_wirectl_download_completion \\"$@\\"\\n"',
            '    fish_script = Utils.safe_popen_read(bin/"wirectl-download", "completion", "fish")',
            '    fish_script = fish_script.lines.reject { |line| line.start_with?("# fish completion") || line == "complete -c wirectl -f -a \'(__wirectl_download_complete)\'\\n" }.join',
            '    (fish_completion/"wirectl-download.fish").write fish_script',
            "  end",
            "",
            "  service do",
            '    run [opt_bin/"wirectl-download", "daemon", "run"]',
            "    keep_alive true",
            "  end",
            "",
            "  def caveats",
            "    <<~EOS",
            "      Initialize the download state before starting the daemon:",
            "        wirectl download init",
            "      Stop the service before upgrading this formula:",
            "        brew services stop wire-download",
            "      If started manually, use wirectl download daemon stop instead.",
            "      Manage the background daemon with:",
            "        brew services start wire-download",
            "        brew services stop wire-download",
            "    EOS",
            "  end",
            "",
            "  test do",
            '    assert_equal version.to_s, shell_output("#{bin}/wirectl-download version").strip',
            '    assert_match "Usage:", shell_output("#{bin}/wirectl-download --help")',
            '    state = testpath/"state"',
            '    downloads = testpath/"downloads"',
            '    mkdir_p downloads',
            '    system bin/"wirectl-download", "--data-dir", state, "init", "--downloads", downloads',
            '    doctor = shell_output("#{bin}/wirectl-download --data-dir #{state} doctor")',
            f'    %w[{download_engines}].each do |engine|',
            '      assert_match (prefix/"libexec/wirectl-download/bin/#{engine}").to_s, doctor',
            '    end',
            "  end",
        ]
    else:
        lines += ["  def install", '    bin.install "bin/wirectl"', "  end", "", "  test do", '    assert_equal version.to_s, shell_output("#{bin}/wirectl version").strip', '    assert_match "Usage: wirectl", shell_output("#{bin}/wirectl --help")', "  end"]
    return "\n".join(lines + ["end", ""])


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--offline", action="store_true", help="render the checked-in lock without network access")
    parser.add_argument("--check", action="store_true", help="verify the formulas exactly match the lock")
    args = parser.parse_args()
    lock = ROOT / "release-lock.json"
    packages = json.loads(lock.read_text()) if args.offline or args.check else {name: release(name, spec) for name, spec in PACKAGES.items()}
    rendered = {ROOT / "Formula" / (name + ".rb"): render(name, packages[name]) for name in PACKAGES}
    if args.check:
        for filename, content in rendered.items():
            if filename.read_text() != content:
                raise SystemExit(f"formula differs from release lock: {filename.name}")
        return
    # Fetch and validate every package before changing any tracked file.
    for filename, content in rendered.items():
        filename.write_text(content)
    lock.write_text(json.dumps(packages, indent=2, sort_keys=True) + "\n")


if __name__ == "__main__":
    main()
