# k0ngk0ng Homebrew Tap

Install `wirectl connect` on Apple Silicon macOS or Linux amd64/arm64:

```sh
brew install k0ngk0ng/tap/wire-connect
wirectl connect setup
wirectl connect login connect.ichenj.com
```

`wire-connect` automatically installs the `wirectl` command host. `setup` requests administrator authorization for the network helper; run it as your ordinary user. The package installer itself does not request elevation or start connections. Server authorization for `login` is separate from OS authorization.

Pair two devices using the [wire-connect instructions](https://github.com/k0ngk0ng/wire-connect#第一次使用). For an existing pair:

```sh
wirectl connect resume
wirectl connect status --watch
```

## Upgrade

```sh
brew update
brew upgrade k0ngk0ng/tap/wirectl k0ngk0ng/tap/wire-connect
wirectl connect setup
wirectl connect resume
```

The last two commands refresh the privileged helper and the saved background connection. Run `resume --name NAME` for other profiles you want active. Stopped profiles do not need to be resumed.

Brew owns these executables. The built-in `wirectl connect update` reports the correct Brew command instead of modifying the Cellar. Ordinary archive installations retain their built-in updater.

If you previously installed binaries in `~/.local/bin`, inspect `type -a wirectl wirectl-connect`. Put Homebrew's `bin` directory first on `PATH`, or remove the old manually installed command files after confirming your new installation. Credentials are stored separately and should not be deleted. To verify the Brew copy explicitly:

```sh
"$(brew --prefix)/bin/wirectl" connect version
```

Bash and Zsh completions are installed in Homebrew's standard completion directories. Enable Homebrew shell completion in your shell as usual.

## Downloads

Install the download plugin alongside `wire-connect`, or on its own:

```sh
brew install k0ngk0ng/tap/wire-download
wirectl download init
wirectl download daemon start
```

`wire-download` depends on the shared `wirectl` host and includes its own aria2/aMule engines. It requires macOS 13+ on Apple Silicon, or Linux with glibc 2.36+ on AMD64/ARM64. The package does not start a daemon during installation. Skip `init` when an existing configuration is present.

Stop the daemon before upgrades so its running engine files remain available:

```sh
wirectl download daemon stop
brew update
brew upgrade k0ngk0ng/tap/wirectl k0ngk0ng/tap/wire-download
wirectl download daemon start
```

Alternatively, after `init`, use `brew services start k0ngk0ng/tap/wire-download`. For this mode, use `brew services stop` before upgrading and `brew services start` afterward instead of the daemon commands. Configuration and downloads stay in the existing user state/download directories.

Bash, Zsh and Fish completion files use the standalone `wirectl-download` name, keeping the `wire-connect` completion files intact. See the [download README](https://github.com/k0ngk0ng/wire-download#shell-补全) for manual completion loading. If migrating from a manual install, check `type -a wirectl wirectl-download` and put Homebrew's `bin` first on `PATH`.

## Voice conversations

```sh
brew install k0ngk0ng/tap/wire-talk
wirectl talk invite
```

The package is `wire-talk`; the command remains `wirectl talk`. `talk` is kept as
an alias. For an existing `talk` installation, stop active audio, run
`brew update`, `brew migrate talk`, then `brew upgrade k0ngk0ng/tap/wire-talk`.
Room configuration stays in the user configuration directory. Reinstall native
login services with `wirectl talk daemon install` after migration.

## Supported platforms

| Package | macOS ARM64 | macOS Intel | Linux AMD64 | Linux ARM64 |
|---|---|---|---|---|
| wirectl | Yes | Yes | Yes | Yes |
| wire-connect | Yes | No | Yes | Yes |
| wire-download | Yes | No | Yes | Yes |
| wire-talk | Yes | Yes | Yes | Yes |

Windows users can use the [Scoop bucket](https://github.com/k0ngk0ng/scoop-bucket).

## Automated maintenance

GitHub Actions checks official stable releases hourly and on manual dispatch. It verifies GitHub asset SHA-256 digests against the separately published `SHA256SUMS`, renders formulas from `release-lock.json`, installs and tests all packages together on macOS ARM64 and Linux AMD64/ARM64, then commits the update only after all jobs pass. No cross-repository personal access token is required. GitHub schedules may be delayed; the manual **Sync official releases** workflow can trigger an immediate check.

To prepare the same update locally:

```sh
python3 scripts/sync.py
python3 scripts/sync.py --check
```

`GH_TOKEN` is optional for public API rate limits. Binary archives remain hosted in each project's official GitHub Releases. The Tap contains no private keys, pairing credentials, network state or bundled binaries.
