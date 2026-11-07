# 构建与发布

`k0ngk0ng/wire-tools` 统一维护 CLI、本地 API、三个功能模块、Homebrew 和 Scoop 的分发定义。正式版本使用 `vMAJOR.MINOR.PATCH` 标签；所有功能共享版本号。

## 本地验证

需要 Go（版本见 `go.mod`）、C 编译器和 Python 3。所有构建缓存、测试目录和结果留在当前仓库。

```sh
make check
make build
```

`make check` 执行 Go race 测试、vet、独立 CLI 进程与 HTTP API 的 E2E，以及真实归档的打包测试。语音 E2E 使用 `talk_test_audio` 驱动，不代表真实音频设备验证。结果与日志在 `.cache/evidence/`，CI 在每个平台上传相同目录。

仅构建当前平台的完整安装包：

```sh
make package VERSION=1.0.0
```

`--full` Unix 打包需要干净且已提交的 Git 工作区，对应源码只取 Git 跟踪文件；先提交需要发布的改动，再执行完整打包。完整 Unix 包从固定源码构建 aria2、aMule 和 FFmpeg，校验并打包 yt-dlp、Deno、运行库与许可证，同时生成对应源码归档。需要原模块构建脚本要求的本机构建依赖。Windows 包携带经固定 SHA-256 校验的官方 Wintun；原下载模块没有 Windows 原生引擎构建链，Windows 上可用的下载协议以运行时实际报告为准。

开发时也可生成只含 Go 程序的包，或使用已准备好的运行依赖目录：

```sh
python3 scripts/package.py --version 1.0.0
python3 scripts/package.py --version 1.0.0 --binary ./bin/wirectl --runtime-dir ./.cache/runtime
python3 scripts/test-installed.py ./dist/wirectl-1.0.0-darwin-arm64.tar.gz
```

`--binary` 仅用于已经构建好的可执行文件。正式发布工作流从源码构建，不使用该选项。`wirectl-package.json` 记录版本、平台和归档内文件的 SHA-256；`SHA256SUMS` 校验完整归档。

## 正式发布

1. 在待发布提交上运行 `make check`。
2. 创建并推送稳定版本标签，例如 `v1.0.0`。
3. 查看当前仓库的 `Release` Actions。

工作流在 Linux amd64、Linux arm64、macOS arm64 和 Windows amd64 上分别原生验证。Linux 完整包在 Debian Bookworm 环境构建，以保存精确运行库对应源码。每个平台安装并检查自己的真实归档，通过后才汇总校验和、生成两种包定义、生成 GitHub 构建来源证明并发布同一 Release。

发布资产包括四个平台归档、对应源码归档、`SHA256SUMS`、`wirectl.rb` 和 `wirectl.json`。归档内只提供一个 `bin/wirectl`（Windows 为 `bin/wirectl.exe`）；底层工具与运行库放在同一安装树中。

发布完成后，工作流用当前仓库的 `GITHUB_TOKEN` 把 `Formula/wirectl.rb` 和 `bucket/wirectl.json` 提交到默认分支。仓库需要允许 Actions 写入内容；默认分支保护规则也需允许该提交。若分支保护拒绝提交，已发布的归档和包定义资产仍可使用，将工作流生成的两个定义通过 PR 合入即可。无需跨仓库个人令牌，也不再依赖旧包仓库的定时同步。

首次统一正式版本发布前，没有可用的稳定统一 Scoop Manifest。Homebrew 可先使用源码入口：

```sh
brew tap k0ngk0ng/wire-tools https://github.com/k0ngk0ng/wire-tools
brew install --HEAD k0ngk0ng/wire-tools/wirectl
```

HEAD 入口构建 Go 程序，完整原生下载引擎由正式分发包提供。

## 安装与更新

首个统一正式版本发布且包定义合入后：

```sh
brew tap k0ngk0ng/wire-tools https://github.com/k0ngk0ng/wire-tools
brew install k0ngk0ng/wire-tools/wirectl
```

Windows：

```powershell
scoop bucket add wire-tools https://github.com/k0ngk0ng/wire-tools
scoop install wire-tools/wirectl
```

后续统一执行 `wirectl update`。包管理器安装带有 `bin/.wirectl-package-manager` 标记，由同一命令调用对应包管理器；手动安装按统一归档更新整套运行依赖。离线更新所用的校验文件应来自可信渠道。

旧的 `wire-connect`、`wire-download` 和 `wire-talk` 包不再是新版 CLI 的使用前提。迁移时检查 `type -a wirectl`（Windows 使用 `Get-Command wirectl -All`），确认 PATH 中实际运行的是统一程序。原仓库及原安装的用户数据不会因本次代码合并而自动删除。
