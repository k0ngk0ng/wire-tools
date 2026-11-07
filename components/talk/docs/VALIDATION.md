# 验收记录

## 一对一直连电话与 USB 按键

`python3 scripts/test-direct.py` 先于实现编写，并确认旧版本因不支持 `init --mode direct` 而失败。测试启动真实 CLI 进程、加密 UDP 连接和 TCP 配对；音频及按键输入使用仅在 `talk_test_audio` 构建标签中提供的测试驱动。结果、进程日志和各阶段 WAV 保留在 `.cache/direct-e2e/时间戳/`，可重复执行。`results.json` 保存场景状态，`network.json` 保存实际丢弃与乱序的数据包数量。

覆盖待机、响铃时不发送音频，响铃不进入收到音频的录音，接听后双向传输和可测量 PCM，按下／松开及重复事件过滤，音量调整，双方拨号／接听／挂断，取消与重拨，双方同时拨号，掉线与重启恢复，第三成员与房间模式隔离，未接听超时，以及停止后的模式迁移。UDP 中转主动丢弃每 5 个数据包中的 1 个，并延迟每 7 个中的 1 个，验证状态重传和序号保护。

真实 `AB17X USB Audio` 在 macOS 完成原生按键接管、物理功能键拨号和接听、本机回铃／来电铃声，以及话筒经 UDP 发送到本地模拟对端的验证。用户确认听到铃声，并在回放模拟对端收到的录音后确认能听到讲话。证据保存在 `.cache/handset-e2e/20261108-005248/`，包含 `results.json`、进程日志和 `microphone.wav`。`python3 scripts/test-handset.py` 可重复执行交互式验收。真实跨机器、双话筒听感尚未验证。

实机检查还发现 IPv4 seed 与发现地址采用不同表示时会重复发送同一数据包。新增 E2E 对原始数据包去重计数，修正为统一地址后要求重复数为零。原有房间模式也使用此修正。

Linux 原生按键使用 `evdev`，只打开匹配 VID/PID 的 USB 输入节点。事件解码的隔离测试先列出截断、无关键值、松开、自动重复及非法值，再编写实现；ARM64 测试程序已在 `10.250.0.7` 执行通过。真实远端按键接入状态在升级后检查。

代码和发布链路已经实现。按用户确认，使用时会接入麦克风并具备音频输出；当前
未插入麦克风不作为交付阻塞。真实麦克风/扬声器双端对话尚未实测，不能将下面的
模拟音频结果解释为已验证真实设备的声音质量、系统麦克风权限或声学回声表现。

## 0.1.3 临时数字码配对

新增房主本机 `invite` 与参与者 `pair IP:PORT --code CODE` / `join IP:PORT --code CODE`。
不依赖独立配对服务；同一端口号分别使用 TCP 配对与 UDP 语音。房间、地址和设备
选项保存在本地，后续启动无需数字码。旧的密钥文件配置与语音协议保持兼容。

本地 `make check` 及[五平台 CI](https://github.com/k0ngk0ng/wire-talk/actions/runs/35689133055)
已通过竞态测试、静态检查和真实进程测试。配对握手另外完成约 68 万次模糊测试，
本机 0.1.3 安装包通过安装及离线更新检查。新增覆盖：

- 正确/错误数字码、成功后不可重用、5 次失败关闭、到期及 Ctrl+C 取消释放监听。
- 非法/超长/截断报文、缺失曲线坐标；篡改双方确认值或加密房间密钥均被拒绝。
- 配对流量不包含明文房间密钥；短码仅用于 PAKE，语音仍使用随机 256 位密钥。
- 后台语音运行时发出邀请；并发邀请拒绝，不覆盖已有房间；失败配对不创建配置。
- 配对后保存房主地址、免码重启、第三成员配对及真实 UDP 音频帧收发（Null 驱动）。

[0.1.3 发布与安装验证](https://github.com/k0ngk0ng/wire-talk/actions/runs/35689413006)
已通过：五个平台正式构建、原生服务与安装包测试，以及四个平台 Homebrew 和
Windows Scoop 的实际安装验证。[发行包](https://github.com/k0ngk0ng/wire-talk/releases/tag/v0.1.3)
已发布。

- [Homebrew 同步、四平台验证与发布](https://github.com/k0ngk0ng/homebrew-tap/actions/runs/35689825662)：通过。
- [Scoop 同步、验证与发布](https://github.com/k0ngk0ng/scoop-bucket/actions/runs/35689829156)：通过。

以下 0.1.2 的链接保留为原有构建与安装链路证据。

## 原生构建、协议与服务

以下 GitHub Actions job 均完成竞态测试、静态检查、完整进程生命周期、原生用户
服务注册/启动/停止，以及发行压缩包安装和离线自更新测试。音频设备部分使用
只在 `talk_test_audio` 构建标签下启用的 miniaudio Null 驱动；发行包不启用该标签，
也禁止在缺少真实音频设备时自动回退到 Null 驱动。

| 平台 | 架构 | 证据 |
|---|---|---|
| macOS | arm64 | [通过](https://github.com/k0ngk0ng/wire-talk/actions/runs/35682072296/job/106600948672) |
| macOS | amd64 | [通过](https://github.com/k0ngk0ng/wire-talk/actions/runs/35682072296/job/106600948641) |
| Linux | amd64 | [通过](https://github.com/k0ngk0ng/wire-talk/actions/runs/35682072296/job/106600948691) |
| Linux | arm64 | [通过](https://github.com/k0ngk0ng/wire-talk/actions/runs/35682072296/job/106600948772) |
| Windows | amd64 | [通过](https://github.com/k0ngk0ng/wire-talk/actions/runs/35682072296/job/106600948626) |

测试覆盖：

- 三个真实 UDP socket 的成员发现和直接音频帧传输；入口节点关闭后其余节点继续互通。
- 篡改、错误密钥、重放、过期/未来时间戳和短包拒绝。
- 多路混音限幅、乱序旧帧丢弃、队列上限和欠载静音。
- Native C/Go 双工回调；关闭设备后不再采集。
- 前台 SIGINT（Unix）、控制停止（所有平台）、后台启动/停止/重启、静音、退出 watch 不影响会话。
- 暂时占用 UDP 端口后释放，持久服务自动恢复在线。
- systemd user、LaunchAgent 和 Windows 计划任务的真实注册、启动、状态查询、停止及清理。
- Windows 私有配置 ACL、Scoop junction 路径、校验失败不改写程序、包管理器安装保护。

## 安装与更新

[0.1.2 发行包](https://github.com/k0ngk0ng/wire-talk/releases/tag/v0.1.2) 已生成五个平台
的压缩包、SHA256SUMS、构建来源证明、Homebrew Formula 和 Scoop Manifest。

[发行包安装复验](https://github.com/k0ngk0ng/wire-talk/actions/runs/35682672389) 已在
四个 Homebrew 平台及 Windows Scoop 全部通过：实际下载、安装、wirectl 插件分发、
初始化配置，以及确认包管理器安装拒绝内置自更新。最初发布任务中的 Scoop 检查曾
把预期的非零退出码误传为任务失败；复验已修正脚本，测试的是同一份 0.1.2 发行包。

本机另外实测手动安装的 `wirectl-talk update` 从 0.1.0 在线升级至 0.1.2，随后执行
`version` 返回 0.1.2。所有下载/安装测试文件都放在工作目录 `.cache` 内。

- Homebrew 自动同步与发布：[通过](https://github.com/k0ngk0ng/homebrew-tap/actions/runs/35682519922)。
- Scoop 自动同步与发布：[通过](https://github.com/k0ngk0ng/scoop-bucket/actions/runs/35682800814)。

## 接入硬件后的实机检查建议

记录时开发机只枚举到 DELL U2717D 和 Mac mini Speakers 两个输出，麦克风尚未插入。
用户已确认按使用时会有麦克风的前提完成交付。以下是接入硬件后建议进行的部署
检查，不作为本次交付的前置条件，也不代表已经完成实测：

1. 各自选择指定输入/输出，前台互相说话并确认双方实际听到声音。
2. 前台 Ctrl+C 后确认麦克风、扬声器和网络活动停止。
3. 启动原生后台服务，关闭终端后持续对话；另开 watch 观察，再退出 watch 确认语音继续。
4. 分别验证耳机全双工与扬声器半双工保护的延迟、回声、噪声和可理解性。
5. 验证真实音频设备拔插、系统权限、登录恢复，以及第三台终端加入群聊。

还未做真实跨机器 IPv6、32 成员负载及长期稳定性实测。当前只承诺需要可达 UDP
地址的直连方式，不提供 NAT 自动打洞或语音中继；跨 NAT 可先使用 wire-connect。
