#!/usr/bin/env python3
"""Offline unit tests for the release metadata checks in sync.py."""
import hashlib
import importlib.util
import json
from pathlib import Path
import unittest
from unittest.mock import patch


SCRIPT = Path(__file__).with_name("update-manifests.py")
MODULE_SPEC = importlib.util.spec_from_file_location("homebrew_sync", SCRIPT)
sync = importlib.util.module_from_spec(MODULE_SPEC)
assert MODULE_SPEC.loader is not None
MODULE_SPEC.loader.exec_module(sync)


class ReleaseFixture:
    """Build a complete release response without contacting GitHub."""

    def __init__(self, package="wirectl", version=None):
        self.package = package
        specification = sync.PACKAGES[package]
        if version is None:
            version = ".".join(map(str, specification["minimum"]))
        self.version = version
        self.tag = "v" + version
        self.base = f"https://github.com/k0ngk0ng/{package}/releases/download/{self.tag}/"
        self.api_url = f"https://api.github.com/repos/k0ngk0ng/{package}/releases/latest"

        self.archive_digests = {}
        assets = []
        checksum_lines = []
        for target in specification["targets"]:
            filename = f"{package}-{version}-{target}.zip"
            archive_bytes = f"archive:{filename}".encode("ascii")
            digest = hashlib.sha256(archive_bytes).hexdigest()
            self.archive_digests[target] = digest
            checksum_lines.append(f"{digest}  {filename}")
            assets.append(
                {
                    "name": filename,
                    "browser_download_url": self.base + filename,
                    "digest": "sha256:" + digest,
                }
            )

        self.sums_bytes = ("\n".join(checksum_lines) + "\n").encode("ascii")
        assets.append(
            {
                "name": "SHA256SUMS",
                "browser_download_url": self.base + "SHA256SUMS",
                "digest": "sha256:" + hashlib.sha256(self.sums_bytes).hexdigest(),
            }
        )
        self.metadata = {
            "tag_name": self.tag,
            "draft": False,
            "prerelease": False,
            "assets": assets,
        }

    def fetch(self, url, api=False):
        expected_api = url == self.api_url
        if api != expected_api:
            raise AssertionError(f"unexpected API flag for {url}: {api}")
        if url == self.api_url:
            return json.dumps(self.metadata).encode("utf-8")
        if url == self.base + "SHA256SUMS":
            return self.sums_bytes
        raise AssertionError(f"unexpected fetch URL: {url}")

    def run(self):
        with patch.object(sync, "fetch", side_effect=self.fetch):
            return sync.release(self.package, sync.PACKAGES[self.package])

    def asset(self, name):
        return next(asset for asset in self.metadata["assets"] if asset["name"] == name)


class ReleaseValidationTests(unittest.TestCase):
    def test_accepts_checked_metadata_for_each_package(self):
        for package in sync.PACKAGES:
            with self.subTest(package=package):
                fixture = ReleaseFixture(package)
                result = fixture.run()
                self.assertEqual(result["version"], fixture.version)
                self.assertEqual(set(result["assets"]), set(sync.PACKAGES[package]["targets"]))
                for target, digest in fixture.archive_digests.items():
                    self.assertEqual(
                        result["assets"][target],
                        {
                            "url": fixture.base
                            + f"{package}-{fixture.version}-{target}.zip",
                            "sha256": digest,
                        },
                    )

    def test_rejects_wrong_sha256sums_digest(self):
        fixture = ReleaseFixture()
        fixture.asset("SHA256SUMS")["digest"] = "sha256:" + "0" * 64
        with self.assertRaisesRegex(ValueError, "SHA256SUMS differs from its GitHub digest"):
            fixture.run()

    def test_rejects_wrong_archive_checksum_entry(self):
        fixture = ReleaseFixture()
        first_line = fixture.sums_bytes.decode("ascii").splitlines()[0]
        _, filename = first_line.split(maxsplit=1)
        bad_line = "0" * 64 + "  " + filename
        remaining_lines = fixture.sums_bytes.decode("ascii").splitlines()[1:]
        fixture.sums_bytes = ("\n".join([bad_line] + remaining_lines) + "\n").encode("ascii")
        fixture.asset("SHA256SUMS")["digest"] = "sha256:" + hashlib.sha256(fixture.sums_bytes).hexdigest()
        with self.assertRaisesRegex(ValueError, "independent checksum mismatch"):
            fixture.run()

    def test_rejects_wrong_archive_asset_digest(self):
        fixture = ReleaseFixture()
        target = sync.PACKAGES[fixture.package]["targets"][0]
        archive_name = f"{fixture.package}-{fixture.version}-{target}.zip"
        fixture.asset(archive_name)["digest"] = "sha256:" + "f" * 64
        with self.assertRaisesRegex(ValueError, "independent checksum mismatch"):
            fixture.run()

    def test_rejects_malformed_archive_asset_digest(self):
        fixture = ReleaseFixture()
        target = sync.PACKAGES[fixture.package]["targets"][0]
        archive_name = f"{fixture.package}-{fixture.version}-{target}.zip"
        fixture.asset(archive_name)["digest"] = "sha256:" + "A" * 64
        with self.assertRaisesRegex(ValueError, "GitHub asset must have a SHA-256 digest"):
            fixture.run()

    def test_rejects_unexpected_sha256sums_url(self):
        fixture = ReleaseFixture()
        fixture.asset("SHA256SUMS")["browser_download_url"] = "https://example.invalid/SHA256SUMS"
        with self.assertRaisesRegex(ValueError, "unexpected checksums download origin"):
            fixture.run()

    def test_rejects_unexpected_archive_url(self):
        fixture = ReleaseFixture()
        target = sync.PACKAGES[fixture.package]["targets"][0]
        archive_name = f"{fixture.package}-{fixture.version}-{target}.zip"
        fixture.asset(archive_name)["browser_download_url"] = "https://example.invalid/" + archive_name
        with self.assertRaisesRegex(ValueError, "unexpected archive download origin"):
            fixture.run()

    def test_rejects_version_below_minimum(self):
        for package, specification in sync.PACKAGES.items():
            minimum = specification["minimum"]
            if minimum[2]:
                below_minimum = (minimum[0], minimum[1], minimum[2] - 1)
            else:
                below_minimum = (minimum[0], minimum[1] - 1, 99)
            version = ".".join(map(str, below_minimum))
            with self.subTest(package=package, version=version):
                fixture = ReleaseFixture(package, version)
                with self.assertRaisesRegex(ValueError, f"{package} requires at least"):
                    fixture.run()

    def test_rejects_draft_and_prerelease(self):
        for field in ("draft", "prerelease"):
            with self.subTest(field=field):
                fixture = ReleaseFixture()
                fixture.metadata[field] = True
                with self.assertRaisesRegex(ValueError, "only stable versioned releases are packaged"):
                    fixture.run()

    def test_rejects_duplicate_release_asset_names(self):
        fixture = ReleaseFixture()
        fixture.metadata["assets"].append(dict(fixture.metadata["assets"][0]))
        with self.assertRaisesRegex(ValueError, "duplicate release asset names"):
            fixture.run()

    def test_rejects_duplicate_checksum_entries(self):
        fixture = ReleaseFixture()
        fixture.sums_bytes += fixture.sums_bytes.splitlines(keepends=True)[0]
        fixture.asset("SHA256SUMS")["digest"] = "sha256:" + hashlib.sha256(fixture.sums_bytes).hexdigest()
        with self.assertRaisesRegex(ValueError, "invalid or duplicate checksum entry"):
            fixture.run()


if __name__ == "__main__":
    unittest.main()
