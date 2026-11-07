# k0ngk0ng Scoop bucket

This bucket distributes the Windows amd64 builds of `wirectl` and
`wirectl-connect` from their official GitHub Releases. Windows ARM is not
included in the support matrix. `wire-connect` 1.2.2 or newer is required for
resolving Scoop installation junctions when updating or installing the helper.

Add the bucket and install both commands from PowerShell:

```powershell
scoop bucket add k0ngk0ng https://github.com/k0ngk0ng/scoop-bucket
scoop install k0ngk0ng/wire-connect
```

`wire-connect` automatically installs the `wirectl` command host.

Scoop only unpacks the official release runtime and creates command shims. It
does not configure a tunnel or request administrator access. Authorize the
network helper once, then use the normal account for daily commands:

```powershell
wirectl connect setup
wirectl connect login connect.example.com
wirectl connect connect.example.com
```

The `wire-connect` manifest writes the package marker next to
`wirectl-connect.exe`. The client uses it to direct self-update requests back
to Scoop. Keep updates under Scoop's control:

```powershell
scoop update
scoop update wirectl wire-connect
wirectl connect setup
wirectl connect resume
```

Run setup as your regular user to refresh the helper. Resume each profile you want active with `resume --name NAME`.

Each manifest pins the archive's SHA-256 from the matching release;
the scheduled updater obtains that value from the independent `SHA256SUMS`
asset before committing a version change. Scoop therefore verifies the
archive against the release checksum instead of trusting a mutable download.
The release archive also contains the official Wintun DLL and its license;
the installer does not download a driver from a third-party site.

## Voice conversations

```powershell
scoop install k0ngk0ng/wire-talk
wirectl talk invite
```

The package is `wire-talk`; the command remains `wirectl talk`. The legacy
`talk` manifest only depends on `wire-talk` and installs no second executable.
If you installed the old standalone `talk` package, stop audio, run `scoop update`,
`scoop uninstall talk`, then `scoop install k0ngk0ng/wire-talk`. Room configuration
in AppData is preserved. Reinstall login services with `wirectl talk daemon install`
after migration. Future upgrades use `scoop update wire-talk`.

## Maintainer checks

The Windows workflow installs both manifests in a disposable Scoop profile,
runs version/help commands that do not create a TUN, and verifies that the
marker contains exactly `scoop`. It never runs `connect setup`, starts a
service, or opens a tunnel.

GitHub Actions checks stable releases hourly and on manual dispatch. It validates GitHub asset digests against the separate `SHA256SUMS`, prepares pinned manifests, installs and tests the staged update on Windows, and commits only after validation passes. It uses the repository's normal `GITHUB_TOKEN`; no cross-repository personal access token is required. Schedules may be delayed; manually run **Sync official releases** for an immediate check. Select `verify_current` to exercise installation even when versions are unchanged.

If you have previous standalone binaries, use `Get-Command wirectl,wirectl-connect -All` to check which copies your PATH resolves. Put Scoop shims first or remove old binaries after verifying the new installation; keep credentials and connection state.

macOS and Linux users can use the [Homebrew Tap](https://github.com/k0ngk0ng/homebrew-tap).
