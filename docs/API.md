# 本地 API

API 使用 HTTP JSON，资源版本前缀为 `/v1`。一个运行时负责同一状态目录中的连接、下载和语音。未使用的引擎和音频设备不会因查询全局状态而启动。

## 发现与认证

调用 `wirectl api endpoint` 会使 API 就绪，并输出私有 `api.json` 文件的路径。文件包含 `address`、`token`、`pid` 和 `version`。Unix 文件权限为 `0600`；Windows 状态目录使用当前用户及 SYSTEM 的私有 ACL。令牌是本地能力凭据，不应出现在日志、网页、命令参数或共享目录中。

HTTP 请求使用 `http://<address>`，并设置 `Authorization: Bearer <token>`。服务只监听 IPv4 回环地址。浏览器 Origin 请求被拒绝；桌面 UI 应由本机后端调用，不能把令牌暴露给任意网页。客户端不经过环境代理、不接受重定向。运行时重启会轮换地址及令牌，客户端需要重新发现。

Go 客户端可供 CLI 和 UI 共用：

```go
client := api.New(privateStateDirectory)
client.Executable = "/absolute/path/to/wirectl"
var status json.RawMessage
err := client.Request(ctx, "GET", "/v1/status", nil, &status)
```

导入路径为 `github.com/k0ngk0ng/wire-tools/api`。`Request` 和 `Do` 自动使运行时就绪；`RequestExisting` 只调用已存在的运行时。客户端在发送业务请求后不会自动重发，避免连接中断时重复执行写操作。

## 资源

| 方法 | 路径 | 请求与结果 |
|---|---|---|
| `POST` | `/v1/update` | 安装统一最新版本；可传 `version`，或绝对路径 `archive`、`checksums`，成功后旧运行时退出 |
| `GET` | `/v1/status` | 统一版本、PID、API 版本和能力列表 |
| `GET` | `/v1/plugins` | 可选硬件插件列表及启用、在线状态 |
| `GET` | `/v1/plugins/{sakshat\|spacetouch}` | 插件状态、当前通话状态、最近操作及硬件错误；SAKSHAT 还返回显示与按钮状态 |
| `POST` | `/v1/plugins/{sakshat\|spacetouch}` | `{"enabled":true}` 或 `{"enabled":false}`，独立启停并保存；不修改 Talk 配对和音频选择 |
| `GET` | `/v1/download/status` | 下载任务及引擎状态 |
| `GET` | `/v1/download/config` | 隐藏 RPC secret 的配置概览 |
| `POST` | `/v1/download/jobs` | `{"source":"HTTP(S)/magnet/ED2K/absolute torrent path"}`，返回任务 |
| `POST` | `/v1/download/submissions` | `{"source":"URL"}`，支持网站视频及批量结果 |
| `POST` | `/v1/download/jobs/{id}/{action}` | `pause`、`resume` 或 `remove` |
| `GET`、`PUT`、`POST` | `/v1/download/trackers` | 查询、替换或编辑 tracker；`PUT` 传 `trackers` 数组，`POST` 加 `operation: add/remove`，对运行中的 aria2 生效 |
| `GET`、`POST` | `/v1/download/servers` | 查询运行中 aMule 的服务器；刷新传 HTTPS `urls` 数组，或传经过验证的 server.met base64 `data` |
| `POST`、`DELETE` | `/v1/download/auth/sessions` | 保存登录会话或按 `?origin=` 删除；返回值不包含 cookie |
| `POST` | `/v1/download/searches` | 创建搜索；详细字段见 Download 文档 |
| `GET`、`DELETE` | `/v1/download/searches/{id}` | 查询或取消搜索 |
| `GET` | `/v1/connect/connections` | 当前和保存的连接，不包含私钥或授权令牌 |
| `POST` | `/v1/connect/connections` | `server` 必填；可传 `name`、`code`、`network`、`relay_only` 等 |
| `GET`、`DELETE` | `/v1/connect/connections/{name}` | 查询或停止连接，停止保留配对资料 |
| `POST` | `/v1/connect/connections/{name}/resume` | 恢复已保存连接 |
| `POST` | `/v1/connect/credentials` | 通过 `server`、`token` 授权；令牌放在请求体 |
| `GET` | `/v1/talk/config` | 配置概览，不返回房间密钥 |
| `POST` | `/v1/talk/config` | 创建配置；可传 `mode`、`listen`、`input`、`output`、`peers`，不覆盖已有配置 |
| `POST` | `/v1/talk/invitations` | 生成邀请，返回 `address`、`code` 和 `expires`，邀请由运行时持有 |
| `POST` | `/v1/talk/pair` | `address`、`code` 加本机设备选项，配对并保存配置 |
| `POST`、`DELETE` | `/v1/talk/session` | 加入或离开语音会话；无需单独启动进程 |
| `GET` | `/v1/talk/status` | 查询音频、房间和媒体状态，不因查询打开音频设备 |
| `GET` | `/v1/talk/devices` | 原生音频设备列表 |
| `POST` | `/v1/talk/mute`、`/v1/talk/unmute` | 麦克风静音或恢复，自动准备会话 |
| `POST` | `/v1/talk/mute/output`、`/v1/talk/unmute/output` | 本地播放静音或恢复 |
| `GET`、`POST` | `/v1/talk/groups`、`/v1/talk/call`、`/v1/talk/media` | 房间、直接通话、输入文件及录音，字段沿用组件资源协议 |

Connect 的创建及恢复是异步操作；按连接名称查询状态及 `pairing_code`。删除保存的配对资料使用 Connect 文档中的明确删除操作，与停止连接区分。

操作失败时返回非 2xx HTTP 状态，资源错误通常为 `{"error":"具体原因"}`。参数错误、未配置、配对失败或缺少原生引擎不会变成「请先启动 daemon」。Talk 的首次会话操作可能返回设备或配置问题；Connect 首次联网仍可能需要 `wirectl connect setup` 的系统授权。

## 终端兼容接口

`POST /v1/commands/{connect|download|talk}` 接受 `args`、调用者绝对工作目录 `directory` 和可选 `input`，以 NDJSON 流返回 `stream`/`data`，最终返回 `exit`。这个接口保留旧 CLI 的参数解析与文字输出，只调用同一可执行程序内的入口，不执行 PATH 插件或 shell。

UI 应使用资源 API 获取状态和执行常见操作。部分维护命令和终端演示通过兼容接口保留；全屏面板在 CLI 端绘制，并通过资源 API 读写状态。

硬件插件只通过资源 API 管理，不经终端兼容接口执行 PATH 插件或任意命令。插件默认禁用，启用状态保存在同一私有目录的 `plugins/NAME/config.json`。已启用的插件随运行时恢复；没有配置 Talk 时等待配置，不自动创建房间或拨号。插件停止和硬件失联不结束已有通话。详细的硬件依赖与按钮／显示行为见[硬件插件](../plugins/README.md)。

`POST /v1/runtime/shutdown` 用于安装器和测试的有序退出，不是日常使用前提。`wirectl update` 是安装事务：校验并安装新程序及其依赖后，结束旧运行时；后续调用自动使用新版本。
