# wire-tools

`wire-tools` 把设备连接、下载和语音整合到一个仓库。安装后的工具仍叫 `wirectl`：一个可执行程序、一个版本、一套本地 API 和发布流程。

```sh
wirectl download 'https://example.com/file.zip'
wirectl talk init
wirectl talk join
wirectl update
```

首次调用自动准备本地运行时，无需执行 `daemon start`。下载和语音会话独立于 CLI 存活；关闭查询或监控命令不会停止其他任务。CLI 负责参数、输出和终端交互，实际操作通过资源 API 执行。原生下载引擎按需启动；HTTP(S) 下载也有内置后端。

## 使用

```sh
# 下载；在交互式终端中直接打开进度面板
wirectl download 'https://example.com/file.zip'
wirectl download --detach 'magnet:?xt=urn:btih:...'
wirectl download list --json
wirectl download watch

# 语音；配对、房间和直接通话见组件文档
wirectl talk init
wirectl talk invite
wirectl talk join
wirectl talk mute
wirectl talk leave

# 设备连接；setup 安装特权网络 helper，需要管理员授权
wirectl connect setup
wirectl connect login vpn.example.com
wirectl connect vpn.example.com
wirectl connect status
wirectl connect stop

# 全部能力统一更新
wirectl update
```

Connect 需要自行部署公网会合及中继服务器，并完成服务器授权。Talk 需要可达的对端地址；原生构建使用实际音频设备。两者的连接协议、安全限制和详细命令分别见 [Connect](components/connect/README.md)、[Talk](components/talk/README.md)。下载协议、视频和搜索见 [Download](components/download/README.md)。

## 安装与平台

支持 macOS arm64、Linux amd64/arm64、Windows amd64。各平台共用同一工具和 API。Talk 使用原生音频后端；Connect 的网络 helper 仍遵守操作系统的权限边界。Unix 完整发布包包含 aria2、aMule、yt-dlp、ffmpeg 和 deno。Windows 包包含 Wintun；内置 HTTP(S) 下载可直接使用，其他下载协议需要可用的对应引擎，当前发布工作流不打包 Windows 版 aria2/aMule/视频工具。

首个统一正式版本发布后，可从 [Releases](https://github.com/k0ngk0ng/wire-tools/releases) 获取对应平台归档，核对 `SHA256SUMS` 后解压完整安装树。`bin/wirectl` 需要和包内的运行依赖一起保留。构建来源可使用 `gh attestation verify <archive> --repo k0ngk0ng/wire-tools` 验证。

Homebrew 和 Scoop 的包定义也维护在此仓库，首个正式版本会由发布工作流生成实际 URL 和校验值。安装命令及首版前的源码安装方式见 [发布与安装](docs/RELEASING.md)。现有的 `wire-connect`、`wire-download`、`wire-talk`、`wirectl`、`scoop-bucket` 和 `homebrew-tap` 仓库均保留，不会自动重命名、归档或删除。

## 本地 API

```sh
wirectl api status --json
wirectl api request GET /v1/download/status
wirectl api request POST /v1/download/jobs '{"source":"https://example.com/file.zip"}'
wirectl api request POST /v1/talk/session '{}'
wirectl api request GET /v1/connect/connections
```

API 只监听 `127.0.0.1` 的动态端口，使用私有目录中的随机令牌认证。CLI 和 Go 客户端会自动发现、启动和恢复运行时。未来 UI 可以使用同一 Go 客户端，或直接调用 HTTP 资源；无需依赖终端输出或模拟 CLI。接口及认证方式见 [API 文档](docs/API.md)。

所有能力的状态默认位于 `${XDG_STATE_HOME:-$HOME/.local/state}/wirectl`。可通过 `WIRECTL_HOME` 或根命令的 `--state-dir` 选择独立目录：

```sh
wirectl --state-dir /absolute/path/to/private-state download --detach 'https://example.com/file.zip'
```

旧安装的状态不会自动移动，也不会启动或停止旧进程。迁移和更新限制见 [迁移说明](docs/MIGRATION.md)。

## 开发与验证

需要 `go.mod` 指定的 Go 工具链和 C 编译器。所有缓存、测试状态和报告保存在仓库内。

```sh
make build
make check
```

`make check` 执行 Go 竞态检查、静态检查、真实 CLI/API 进程 E2E 和打包验证。覆盖 HTTP 下载、暂停和恢复、并发提交、进程崩溃恢复、双端配对与 UDP 语音传输、整包更新、校验拒绝，以及终端面板交互。E2E 语音使用显式的测试音频驱动；普通构建始终使用原生音频后端。测试日志与 JSON 报告位于 `.cache/evidence/`，CI 保留同样的产物。

源码按 `components/connect`、`components/download`、`components/talk` 分组，但共用根模块、运行时和发布。导入来源及提交见 [imports.json](docs/imports.json) 和 [包定义来源](packaging/ORIGINS.json)。架构见 [ARCHITECTURE.md](docs/ARCHITECTURE.md)。
