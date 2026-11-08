# 从独立仓库迁移

新仓库是 `k0ngk0ng/wire-tools`，工具仍叫 `wirectl`。旧仓库和已安装程序不会因源码导入而改变。导入保留了源提交标识及原测试；这是新的整合仓库，不改写旧仓库的 Git 历史。

## 安装切换

使用完整统一安装包，或首个正式版本生成后的 Homebrew/Scoop 定义。检查 `type -a wirectl`；Windows 使用 `Get-Command wirectl -All`，确认运行的是新安装的入口。旧 `wirectl-connect`、`wirectl-download` 和 `wirectl-talk` 不再是新工具的插件，也不会被新入口自动执行。

新版本不接管旧进程。需要迁移运行中的数据时，先通过旧安装自己的命令结束对应任务或会话，确认其状态已写盘，再备份并复制需要的数据。不要让新旧运行时同时写入同一组件状态目录。新安装全部能力使用同一个 `WIRECTL_HOME`，其中 `connect/`、`download/`、`talk/` 分别存放组件状态。

旧状态格式沿用各组件的格式，但目录发现方式已经统一。配对凭据、下载任务和音频配置均属于私有数据；不要提交到 Git 或上传测试报告。Connect 可继续使用协议兼容且健康的系统网络 helper；缺失或不兼容时，使用新入口执行 `connect setup`，该操作会请求操作系统授权。Linux 的 root 进程可直接创建 TUN。

新版本没有公开的 `download daemon` 或 `talk daemon` 管理入口。直接提交下载、加入语音或执行通话操作即可；语音离开使用 `talk leave`，连接停止使用 `connect stop`。下载监控面板退出只结束监控。

## 更新

```sh
wirectl update
wirectl update --version 1.2.3
wirectl update --archive ./wirectl-1.2.3-linux-amd64.tar.gz --checksums ./SHA256SUMS
```

指定版本和离线更新仅适用于手动安装。包管理器安装由同一命令调用 Homebrew 或 Scoop。在线更新只接受官方 Release 及可信 GitHub 下载域名，并同时校验两份摘要。离线校验只证明归档与提供的校验文件一致，校验文件需要来自可信来源。

整包更新保留不可变的旧运行依赖目录。新入口的版本决定其依赖目录；不要仅复制更新器生成的 `bin/wirectl` 而遗漏 `.wirectl/`。安装目录需要当前用户有写入权限；更新器不自动提权。更新完成后，旧运行时结束，下一次调用自动启动新版本。多个 `WIRECTL_HOME` 的运行时相互独立，需要分别退出或在下一次运行前停止旧实例。

不要把旧仓库的安装包传给新版整包更新器。旧仓库保留历史 Release；统一版本及更新入口来自 `k0ngk0ng/wire-tools`。`sactl` 的 Homebrew 与 Scoop 安装入口已迁入其源码仓库 `k0ngk0ng/stoneage`，不随 `wirectl` 安装或更新。
