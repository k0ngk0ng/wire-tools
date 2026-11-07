# 下载能力

下载已合并到根目录的 `wirectl`，由统一的本地 API 运行时管理。直接提交地址即可创建配置、准备所需引擎并启动下载；无需单独安装插件或管理下载进程。

```sh
wirectl download --detach 'https://example.com/file.zip'
wirectl download list
wirectl download pause TASK_ID
wirectl download resume TASK_ID
wirectl download remove TASK_ID
wirectl download search '关键词'
```

普通 HTTP(S) 下载在没有 aria2 时使用内置 Go 后端。BitTorrent、magnet 和 ED2K 继续使用 aria2、aMule，网站视频使用 yt-dlp、ffmpeg、deno；原生引擎按需启动，某个引擎缺失不会阻止其他功能。Unix 完整安装包包含相应引擎。引擎缺失时，API 返回具体依赖问题。

统一 API 路径包括：

| 方法 | 路径 | 用途 |
|---|---|---|
| `GET` | `/v1/download/status` | 版本、任务和引擎状态 |
| `GET` | `/v1/download/config` | 配置概览，隐藏 RPC secret |
| `POST` | `/v1/download/jobs` | 提交单个下载，JSON 字段为 `source` |
| `POST` | `/v1/download/submissions` | 提交网站视频或批量结果 |
| `POST` | `/v1/download/jobs/{id}/{action}` | `pause`、`resume`、`remove` |
| `GET`、`PUT`、`POST` | `/v1/download/trackers` | 查询、替换或增删 tracker，并应用到运行中的 aria2 |
| `GET`、`POST` | `/v1/download/servers` | 查询原生服务器列表，刷新 HTTPS 来源或导入经过验证的 server.met |
| `POST`、`DELETE` | `/v1/download/auth/sessions` | 保存或删除按网站隔离的登录会话 |
| `POST` | `/v1/download/searches` | 创建搜索 |
| `GET` | `/v1/download/searches/{id}` | 查询搜索结果 |
| `DELETE` | `/v1/download/searches/{id}` | 取消搜索 |
| `GET` | `/v1/download/search-sources` | 查询搜索源 |

API 的地址发现和认证方式见根目录文档。下载状态默认位于 `$WIRECTL_HOME/download`，内置 HTTP 后端在该目录保存任务日志，下载内容位于 `downloads/`。部分文件以私有文件暂存，完成时发布；不会覆盖同名现有文件。

原插件版本的详细协议和操作说明保存在 [历史文档](docs/LEGACY.md)。其中独立安装、daemon 和发布步骤只适用于原仓库版本。

与原插件共用状态目录时，运行时会先取得原 `daemon.lock`。如果旧进程仍占用目录，API 返回状态正在使用的错误，并保持已有配置和数据不变；可等待旧会话结束，或通过 `WIRECTL_HOME` 选择独立目录。
