# 运行时与发布架构

```text
wirectl CLI ── api.Client ── authenticated loopback HTTP
                                      │
                          one runtime per WIRECTL_HOME
                          ├── connect: pairing / WireGuard / helper
                          ├── download: store / HTTP / lazy native engines
                          └── talk: rooms / audio / media / pairing
```

`api` 是客户端库，`internal/host` 是本地运行时，`internal/app` 只组织终端入口和安装操作。组件的公开 Manager 提供 HTTP 资源，不依赖 CLI 存活。已有底层协议、安全检查及其测试保留在各自的 `internal` 包内。全仓库只有根 `go.mod`，不使用相邻仓库的 replace，也不依赖插件可执行文件。

运行时持有操作系统文件锁。启动过程另有短时互斥，避免并发 CLI 创建多个实例。`api.json` 通过临时文件替换发布；仅持有运行时锁的进程可以替换或移除它。关闭时先清理资源，再移除 endpoint 和释放锁。客户端可恢复已经退出进程留下的 endpoint，但不自动重发已经提交的业务请求。

下载任务与传输日志保存在磁盘。下载运行时还持有旧版的状态锁，拒绝与旧进程同时写入同一目录；原生引擎由带独立所有权锁的内置 watchdog 托管，API 意外退出后先完成引擎清理，新实例才接续其状态。下载引擎相互独立，按协议选择并按需启动。Talk 使用现有的房间、直接通话、音频与配对实现。Connect 将需继续运行的连接名称单独持久化；运行时重启后尝试恢复对应的已配对连接。创建虚拟网卡和设置路由仍通过受限特权 helper 完成。

更新器从 `k0ngk0ng/wire-tools` 的统一 Release 取包，在线同时核对 GitHub asset digest 和 `SHA256SUMS`。完整归档在验证路径、类型、大小和文件清单后安装到 `.wirectl/versions/<version>`，再原子替换入口可执行程序。新程序按自身版本选择不可变的运行依赖，避免先覆盖依赖再替换程序造成混装。旧版本目录暂时保留，便于仍在运行的进程退出；当前不提供自动清理或回退命令。

Homebrew 和 Scoop 使用包内标记识别安装来源。`wirectl update` 调用对应包管理器升级整套安装；所有包定义和构建流程均维护在同一个仓库。实际平台包、校验文件、许可证、来源证明及包定义由同一次发布生成。
