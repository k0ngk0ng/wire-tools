#!/usr/bin/env python3
"""Update Scoop manifests from independently checked official GitHub releases."""
import argparse
import hashlib
import json
import os
from pathlib import Path
import re
import urllib.request

ROOT = Path(__file__).resolve().parents[1]
PACKAGES = {
    "wirectl": {"minimum": (0, 2, 3), "targets": ("windows-amd64",)},
    "wire-connect": {"minimum": (1, 2, 2), "targets": ("windows-amd64",)},
}


def fetch(url, api=False):
    headers = {"User-Agent": "k0ngk0ng-scoop-bucket", "Accept": "application/vnd.github+json" if api else "application/octet-stream"}
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
        filename = f"{package}-{version}-{target}.zip"
        asset = assets[filename]
        if asset["browser_download_url"] != base + filename:
            raise ValueError("unexpected archive download origin")
        digest = sha256(asset)
        if digest != sums.get(filename):
            raise ValueError(f"independent checksum mismatch: {filename}")
        result["assets"][target] = {"url": base + filename, "sha256": digest}
    return result


def main():
    parser = argparse.ArgumentParser()
    parser.add_argument("--dry-run", action="store_true")
    args = parser.parse_args()
    # Validate all official releases before changing either manifest.
    releases = {name: release(name, spec) for name, spec in PACKAGES.items()}
    rendered = {}
    for name, item in releases.items():
        path = ROOT / "bucket" / (name + ".json")
        manifest = json.loads(path.read_text())
        asset = item["assets"]["windows-amd64"]
        manifest["version"] = item["version"]
        manifest["architecture"]["64bit"] = {"url": asset["url"], "hash": asset["sha256"]}
        rendered[path] = json.dumps(manifest, indent=4) + "\n"
    for path, content in rendered.items():
        changed = path.read_text() != content
        print(path.name + (": update available" if changed else ": current"))
        if changed and not args.dry_run:
            path.write_text(content)


if __name__ == "__main__":
    main()
