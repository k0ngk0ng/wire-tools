> 迁入前的使用说明，仅用于追溯原功能和安全边界。安装、更新和运行方式以根目录 README 及本目录 README 为准。原文中的独立插件和 daemon 命令不再是当前入口。

# wire-talk

通过本地麦克风和扬声器进行端到端加密的实时对话。终端命令为
`wirectl talk`，也可以直接运行 `wirectl-talk`。不转录、不保存音频，
不使用云端语音服务。

## 安装

0.1.4 起命令默认输出易读信息，脚本使用 `--json`。数字配对码从 0.1.3 起提供：

```sh
# macOS / Linux，Apple Silicon / Intel，amd64 / arm64
brew install k0ngk0ng/tap/wire-talk
```

新版 Homebrew 若提示尚未信任 `wirectl` 依赖，先执行下面的信任命令，再重试安装：

```sh
brew trust --formula k0ngk0ng/tap/wirectl
brew install k0ngk0ng/tap/wire-talk
```

```powershell
# Windows amd64
scoop bucket add k0ngk0ng https://github.com/k0ngk0ng/scoop-bucket
scoop install k0ngk0ng/wire-talk
```

包名为 `wire-talk`，运行命令仍为 `wirectl talk`。旧的 Homebrew 名称 `talk` 保留为
别名；已安装旧包时，停止语音后运行 `brew update`、`brew migrate talk`，再使用
`brew upgrade k0ngk0ng/tap/wire-talk`。Scoop 的旧安装先 `scoop uninstall talk`，再
`scoop install k0ngk0ng/wire-talk`；房间配置保存在独立的用户配置目录，不随卸载删除。
迁移完成后重新启动；使用原生自启服务的用户重新执行 `daemon install`。

手动安装：下载 [Releases](https://github.com/k0ngk0ng/wire-talk/releases)
中对应平台的包，核对 `SHA256SUMS`，将 `bin` 中两个可执行文件放入同一 PATH
目录。已安装 wirectl 时可只添加 wirectl-talk 插件。Windows 使用 `.exe`。
无需另装 ffmpeg、Python 或音频编解码器；Linux 需要可用的 ALSA/PulseAudio
音频环境，macOS/Windows 需要允许麦克风权限。

### Shell 补全

`wirectl talk` 提供 bash 和 zsh 补全脚本。Homebrew 安装规则会将脚本安装到标准
补全目录；zsh 需在 `fpath` 包含 Homebrew 的 `share/zsh/site-functions` 后运行
`compinit`，bash 需启用 bash-completion。升级后打开新终端加载。

v0.1.5 的 Homebrew 包遗漏了补全安装步骤。该版本或手动安装时，可加入当前 shell 会话：

```sh
# bash
source <(wirectl talk completion bash)

# zsh（如果尚未启用补全系统）
autoload -Uz compinit && compinit
source <(wirectl talk completion zsh)
```

脚本同时支持直接运行的 `wirectl-talk` 命令。

## 用临时数字码加入房间

房主在自己电脑上生成配对码，对方通过 **IP:端口 + 6 位数字码** 加入。不需要部署
独立配对服务，也不用复制密钥文件。房间仍使用随机 256 位密钥，由程序在配对时
自动交换并保存；数字码不是语音加密密钥。

机器 A（房主）：

```sh
wirectl talk invite
# Pairing code: 482731（示例，以实际输出为准）
```

首次 `invite` 会自动创建默认房间，使用默认音频设备和端口 `51830`。已有配置时
沿用现有房间。保持该命令运行，将自己的可达 IP、端口和临时码告诉对方。
如果要自选设备，先执行 `devices`，再用 `init --input ID --output ID` 创建房间；
自选端口可加 `--listen 0.0.0.0:端口`。

机器 B（假设 A 的可达 IP 为 `192.168.1.10`，B 尚未初始化房间）：

```sh
wirectl talk join 192.168.1.10:51830 --code 482731
```

配对后 B 自动保存房间和房主地址，并启动前台音频。A 的 `invite` 随后退出，A 执行：

```sh
wirectl talk join
```

现在双方直接说话即可。首次启动需允许麦克风权限。B 的首次 `join` 也可以添加
`--input ID --output ID --headphones` 等选项。若只想完成配对、稍后再启动音频：

```sh
wirectl talk pair 192.168.1.10:51830 --code 482731
wirectl talk daemon start
```

以后两端直接 `join` 或 `daemon start`，不需要再次输入地址或数字码。
已有房间配置不会被配对覆盖；加入另一个房间时用 `wirectl talk group join HOST:PORT --code CODE`。
配对后若音频启动失败，修复设备/权限后直接运行 `join`，无需重新配对。

数字码随机生成，**5 分钟有效、一次使用**；5 次失败连接后关闭邀请，需重新运行
`invite`。Ctrl+C 立即取消邀请，不影响已运行的语音会话。认证成功即消耗邀请，
即使随后网络中断或保存失败，也需要生成新码。码到期不会踢出已经配对的成员。

群聊时，任一已有成员运行 `invite` 邀请下一个成员；后台对讲可以继续运行。
其他成员使用该邀请者的地址和新码加入。发现成员后音频直接在各参与者之间传送，
邀请者不负责转发语音。前台 `join` 按 Ctrl+C 会关闭本次麦克风、扬声器、网络和
状态接口；远端成员不受影响。

配对使用房主监听地址上的 **TCP**，语音使用同一端口号的 **UDP**（默认均为
`51830`）。TCP 只在 `invite` 等待期间监听；防火墙/路由器端口映射需放行对应协议。
需要所有成员之间 UDP 地址可达；支持局域网、公网地址、VPN，或 `wirectl connect`
建立的虚拟 IP。数字码不会解决 NAT 连通问题，当前不提供自动打洞/中继。
`0.0.0.0:51830` 默认接收 IPv4；IPv6 可使用 `[::]:51830`，对端地址写为 `[IPv6]:51830`。

旧的 `init --key-file ./room.key --peers IP:PORT` 方式仍可使用，与数字配对加入的成员
使用同一种语音协议；日常使用优先选择临时数字码。

## 一对一直连电话

`direct` 模式把连接与通话分开：两端上线后只交换加密心跳；拨号后对方响铃，接听后才双向传输语音。挂断后回到待机，连接继续保活。原有 `room` 模式仍为加入后直接通话，已有配置默认保持此行为。

首次创建电话连接：

```sh
# 机器 A
wirectl talk devices
wirectl talk init --mode direct --input INPUT_ID --output OUTPUT_ID
wirectl talk invite

# 机器 B：自动保存邀请中的 direct 模式
wirectl talk pair HOST:51830 --code CODE --input INPUT_ID --output OUTPUT_ID
wirectl talk daemon start

# 机器 A：邀请完成后启动
wirectl talk daemon start
```

两个可执行文件都需更新到支持电话模式的版本。旧客户端会拒绝电话邀请，避免退回加入即通话。每个电话连接只接纳一个在线对端，不发现或混入第三个成员。

已有配对可在**两端停止服务后**切换，无需重新配对：

```sh
wirectl talk daemon stop
wirectl talk group mode default direct
wirectl talk daemon start
```

`default` 可替换为连接名称或房间 ID。两端都要执行；恢复群聊使用 `group mode default room`。也可使用 `group create 电话 --mode direct` 创建独立连接，再邀请另一端，通过 `group use 电话` 选中。通话过程中需先挂断，才能切换讲话目标。

macOS 支持 `AB17X USB Audio`（USB VID `001f`、PID `0b23`）的话筒按键；Linux 支持同款设备，以及 `USB PnP Audio Device`（VID `0c76`、PID `1203`）。仅在当前选中 `direct` 连接且程序运行时接管匹配设备的消费控制键，不接管普通键盘：

| 状态或按键 | 行为 |
|---|---|
| 待机时按播放／暂停 | 拨号；本机播放回铃音，对端播放铃声 |
| 来电响铃时按播放／暂停 | 接听，开始双向通话 |
| 拨号中或通话中按播放／暂停 | 取消拨号或挂断 |
| 音量加／减 | 调整本程序播放增益，每次 2 dB，并保存 |

按住功能键不会反复拨号或立即挂断；松开不触发操作。音量增益范围为 −60 到 +24 dB，不改动对方音量或麦克风增益。按键接管避免同时触发系统播放器或重复调整系统音量。设备拔出后等待重连；`status` 显示按键设备状态。权限不足时，macOS 检查「隐私与安全性 → 输入监控」；Linux 需要对匹配设备的 `/dev/input/event*` 节点有读取权限。

话筒输入与铃声／语音输出独立选择。如果 `AB17X USB Audio` 没有接扬声器或耳机，输出可选择 `Mac mini Speakers`，不要仅因设备名称相同就将它选作输出。`--input`、`--output` 使用 `devices` 中的 ID。

没有硬件按键、其他系统或按键暂时不可用时，可使用命令操作：

```sh
wirectl talk call dial
wirectl talk call answer
wirectl talk call hangup
wirectl talk call button          # 与功能键相同的状态切换
wirectl talk call status --json
```

`status`、`watch` 和 `group status` 显示 `idle`、`dialing`、`ringing`、`active`。未接听的呼叫在 45 秒后结束；6 秒没有收到对端数据时结束通话。重连或重启后保持待机，需要再次拨号、接听。铃声仅在本机播放，不进入传输或收到音频的录音。待机仍打开本机音频设备供电平观察，但不发送音频帧；配置静音仍然生效。

## 音频设备和扬声器防啸叫

`devices` 默认以表格显示设备名、稳定 ID、输入/输出方向和默认设备标记；没有输入
或输出设备时会直接提示。脚本读取使用 `--json`，保留原有 JSON 字段：

```sh
wirectl talk devices          # 便于人阅读的表格（0.1.4 起）
wirectl talk devices --json   # JSON 数组，供脚本使用
```

初始化时绑定一套设备：

```sh
wirectl talk init --input INPUT_ID --output OUTPUT_ID
```

已有配置可在停止服务后编辑 `config.json` 中的 `input`、`output`，空字符串表示
启动时选择默认设备。明确指定的设备不存在时会报错，不会悄悄切换到其他设备。
每个运行实例只打开一套输入和输出；同一配置不能同时启动前台和后台实例。

默认全双工：播放对方声音不会暂停本地麦克风发送。旧版按播放电平静音麦克风的
门控已移除，避免远端持续底噪或接收增益导致本地一直无法讲话。
`--headphones` 和配置中的 `headphones` 为兼容保留，不再改变收发行为。
当前没有声学回声消除；外放可能产生回声，佩戴耳机或降低音量可减少回声。

格式为 16kHz 单声道 16-bit PCM，每帧 20ms；单个远端约 282kbit/s（另加 IP/UDP
开销），群聊发送带宽随成员数线性增长，最多 32 个远端。不做语音转录和有损压缩。
接收端保留有界短队列，多人同时说话时混音并限幅，丢失的帧用静音填充。

## 多房间与在线成员

同一后台会话可同时连接最多 16 个房间，共用一套麦克风和扬声器。
**麦克风和文件输入只发送到当前讲话房间**；各房间默认开启收听，可单独静音。
原有配置会作为 `default` 房间继续使用，配对和音频设备设置均保留。

```sh
wirectl talk group list                 # 房间 ID、名称、当前讲话目标和在线人数
wirectl talk group status               # 当前房间的其他在线成员
wirectl talk group levels               # 每个成员各自的实时音量条
wirectl talk group watch                # 成员加入、离线时更新列表
wirectl talk group status ROOM_ID       # 查看指定房间（也可用名称）
wirectl talk group watch ROOM_ID
wirectl talk group use ROOM_ID          # 切换麦克风发送目标
wirectl talk group mute ROOM_ID         # 不再收听该房间，连接保持
wirectl talk group unmute ROOM_ID       # 恢复收听
```

房间 ID 在同一房间的所有节点上一致，重启或重命名不会改变；它不是配对码，不能
单凭 ID 加入。名称是本机标签，可用 `group rename ROOM_ID 新名称` 修改。
`group levels [ROOM_ID|NAME]` 每 100ms 显示自己和各成员的话筒音量条、dBFS。
远端电平来自本机收到的音频，在混音和本地收听静音之前测量；并非远端扬声器电平。
自己显示送入该房间的音频（含文件输入），停止收到音频后电平最多 400ms 归零。
自己的状态会显示 `audio`（有音频）、`silent`（无音频）、`mic muted`、
`mic offline`、`mic disabled` 或文件输入状态，不再将选中房间标为“发送中”。
`audio` 表示近期送入此房间的音频非零，不保证对方扬声器已播放。
终端内原地刷新，并限制到窗口大小；非终端／重定向默认只打印一次，避免重复刷屏。
`--json`（含 `self_state`）明确启用连续 JSON 快照。退出视图不会停止对讲。

列表的 `OTHERS` 和状态中的成员不包含自己；Node ID 标识当前会话中的节点，重启会改变。
`group list/status/watch --json` 提供机器可读输出。

创建或加入另一个房间，无需停止正在运行的 daemon：

```sh
wirectl talk group create 家人 --listen 0.0.0.0:51831
wirectl talk group invite 家人
# 另一台机器加入，使用邀请者的可达地址和临时数字码：
wirectl talk group join HOST:51831 --code CODE --name 家人
wirectl talk group use 家人
```

省略 `--listen` 时自动选择可用端口。额外房间需要各自可达的 UDP 音频端口，
邀请时还使用同端口的 TCP。新房间不会自动抢占当前讲话目标；首次创建／加入
且尚无房间时会成为当前房间，然后用 `daemon start` 启动。
离开非当前房间用 `group leave ROOM_ID`；离开当前房间前先 `group use` 另一个房间。
当前讲话房间、名称和各房间的收听静音状态会保存，重启后恢复。

录音和文件输入默认针对当前房间。可用 `record ... --group ROOM_ID` 指定录音所属房间；
录音不受该房间收听静音影响。切换讲话房间会停止旧房间的文件输入，避免意外续播到另一房间；
其他房间的录音会继续。只有当前讲话房间允许开始或恢复文件输入。

## 接收声音放大

系统音量最大仍听不清时，可以增加本机软件播放增益：

```sh
wirectl talk volume                         # 查看增益
wirectl talk volume output +6               # 所有收到的声音放大约 2 倍
wirectl talk volume peer 100.113.200.13:51830 +6  # 只放大此成员
wirectl talk volume peer NODE_ID +6 --group default
wirectl talk volume output 0                # 恢复整体默认增益
wirectl talk volume peer 100.113.200.13:51830 0   # 清除此成员的增益
```

范围为 −60 到 +24 dB，支持小数。整体和成员增益叠加，在线立即生效并保存，
也可在后台停止时设置。成员设置按房间和 IP:端口保存；同地址重启仍生效，
地址变化需重新设置。Node ID 只用于查找当前在线成员。
`volume --json [--group ROOM]` 输出 JSON，普通输出为可读文本。

仅影响自己听到的声音，不改变发送音频、录音或 `group levels` 的原始接收电平。
峰值限幅器在转换为播放 PCM 前压低过大的混音，避免样本溢出；声音很大时会压缩动态。
增益也会放大底噪，建议从 +6 dB 开始。输出静音和房间收听静音仍然生效。

## 后台在线与状态

```sh
wirectl talk daemon start
wirectl talk watch
wirectl talk levels
wirectl talk mute
wirectl talk unmute
wirectl talk daemon status
wirectl talk daemon stop
```

`daemon start` 分离为后台进程，确认音频设备成功启动后才报告在线。可关闭终端；
`status` 和 `daemon status` 默认显示易读的状态摘要；`watch` 先显示摘要，再每秒输出
在线状态、静音、成员数和收发计数。`levels` 每 100ms 更新输入和输出的音量条、
RMS dBFS、峰值、静音和设备离线状态；输入电平取自麦克风采集（静音时仍可观察），
输出电平取自实际交给播放设备的声音（输出静音后为零）。Ctrl+C 只退出观察，
不停止后台音频。缺少设备
或尚未在线时显示启动提示及最近日志，不再只提示缺少 `control.json`。
需要原有机器可读格式时，显式添加 `--json`（从 0.1.4 起）：

```sh
wirectl talk status --json
wirectl talk daemon status --json
wirectl talk watch --json     # 每行一个 JSON 对象
```

JSON 保留输入/输出设备、成员、最近消息时间、发送/接收帧、丢帧、拒绝报文计数和
静音状态。错误写入 stderr，不混入 JSON 输出。`mute` / `unmute` 成功后显示确认信息。
无音频设备、权限拒绝或端口冲突时不会假报启动成功，详情在状态目录 `daemon.log`。

需要登录后自动启动及异常恢复时，先停止手动会话，再注册原生用户服务：

```sh
wirectl talk daemon stop
wirectl talk daemon install
wirectl talk watch
```

macOS 使用 LaunchAgent，Linux 使用 systemd user service，Windows 使用登录计划
任务（交互用户身份，避免 session 0 无法访问音频）。必须先在前台授予麦克风权限。
注册成功表示系统服务已安装，实际设备在线状态用 `watch` 确认。设备不可用或端口
暂时占用时，服务每 3 秒重试；整个进程异常退出时由系统服务管理器重启。退出登录期间不承诺
音频可用；重新登录后自动启动。`daemon stop` 同时移除当前配置的服务注册，保留
房间配置。再次 `daemon install` 恢复自启。系统服务实测情况见 [验收记录](docs/VALIDATION.md)。

默认状态目录：macOS 为 `~/Library/Application Support/wirectl/talk`，Linux 为
`$XDG_CONFIG_HOME/wirectl/talk` 或 `~/.config/wirectl/talk`，Windows 为
`%AppData%\wirectl\talk`。可用 `--state-dir DIR` 或 `WIRE_TALK_HOME` 指定。
配置目录限制为当前用户：Unix 使用 0700，Windows 使用受保护的用户/SYSTEM ACL。

## 更新

停止当前实例后，手动安装可自更新：

```sh
wirectl talk daemon stop
wirectl talk update
wirectl talk daemon start
```

`update --version 0.1.0` 可选具体版本；在线下载只接受官方 GitHub Release 及可信
下载域名，同时核对 GitHub asset SHA-256 和独立 `SHA256SUMS` 后替换插件。
包管理器安装使用 `brew upgrade k0ngk0ng/tap/wire-talk` / `scoop update wire-talk`。
内置更新不会改写带包管理标记的安装。wirectl 主程序单独由其包管理器更新。
原生服务绑定启动时的程序路径；包管理器升级前先 `daemon stop`，升级后重新
`daemon install`，以免旧安装目录被清理后服务仍引用旧版本。

离线更新：

```sh
wirectl talk update --archive ./wire-talk-0.1.0-linux-amd64.tar.gz --checksums ./SHA256SUMS
```

离线校验只能证明包与提供的校验文件一致，校验文件应从可信渠道获取。Windows
更新时会先重命名旧程序；仍在使用的旧 `.talk-old-*.exe` 可在所有进程退出后清理。
不同 `--state-dir` 下运行的其他实例需要分别停止、重启才能使用新版本。

## 安全边界

临时数字码通过 P-256 PAKE、双方密钥确认和 AES-256-GCM 传递房间密钥；数字码与
房间密钥不会明文发送。配对接口限制报文大小、连接时长和总尝试次数。知道有效码
且能连接邀请者的人可以抢先加入，因此只把码交给预期成员。对配对端口的恶意连接
可以耗尽次数，届时生成新码即可。配对协议及其集成尚未经过独立安全审计。

随机房间密钥用于 AES-256-GCM 加密和认证；随机报文 nonce、防重放窗口与时间戳
阻止重放。各机器时间须同步，最大时差约 15 秒。持有房间密钥者均为受信任成员，
可听到房间语音并邀请其他人；不提供成员级身份、撤销或前向保密。移除成员需要
停止所有实例并创建新密钥。元数据（IP、流量、时序）不隐藏。

控制接口只监听本机回环地址，并使用独立随机令牌认证。音频不写盘；`daemon.log`
只记运行信息。密码协议尚未经过独立审计。

## 开发和发布

需要 Go 1.25+ 与 C 编译器；CI 使用 Go 1.26.2。音频通过 malgo/miniaudio 访问
CoreAudio、WASAPI、ALSA/PulseAudio。`third_party/wirectl` 固定了 CLI 基座快照，
因此构建无需修改相邻项目。

```sh
make check
make build
./bin/wirectl talk --help
python3 scripts/package.py --version 0.1.0
python3 scripts/test-package.py
```

缓存和测试临时文件都留在当前目录 `.cache`。测试覆盖加密与重放、三节点真实 UDP
通信、混音、实例锁、状态接口权限、更新校验与安装包实际升级，以及数字码配对、
错误码/过期/重用/篡改拒绝、配对后保存与重启、多成员配对后收发音频帧。

推送 `vX.Y.Z` 标签触发 GitHub Actions：五个平台分别原生测试、构建、安装包
自更新测试，通过后发布二进制包、校验和、构建证明、Homebrew Formula 和 Scoop
Manifest，并执行实际 Homebrew/Scoop 安装检查。

Homebrew tap 与 Scoop bucket 各自的 `Sync talk releases` Actions 每小时检查正式
Release，独立核对 GitHub 摘要和 SHA256SUMS，再做原生安装测试；通过才提交包定义。
可手动触发同步。这个流程使用包仓库自己的 GITHUB_TOKEN，不需要跨仓库个人令牌。
同步可能受 GitHub 调度延迟影响。维护用工作流源文件在 `packaging/`。

### 本机会话静音

```sh
wirectl talk mute input      # 麦克风禁音，对方听不到你（也可简写 mute）
wirectl talk unmute input    # 恢复麦克风（也可简写 unmute）
wirectl talk mute output     # 本地播放静音，你听不到对方
wirectl talk unmute output   # 恢复本地播放
wirectl talk status          # 查看输入、输出是否静音
```

输入和输出独立控制，只作用于当前 talk 会话，不修改系统音量或其他应用。
输出静音时仍接收并消耗音频，恢复播放不会重播静音期间的内容。
重启会话后恢复非静音状态。升级后需重启后台进程才能使用新的输出静音功能。

## 录制收到的声音

会话运行后，使用绝对路径或相对于当前命令目录的路径开始录音（目录需已存在）：

```sh
wirectl talk record start ./meeting.wav
wirectl talk record status
wirectl talk record stop
```

默认录制所有对端的混音，不包含自己的麦克风。`mute output` 不影响录音。
文件为 16 kHz、单声道、16-bit PCM WAV，静音／丢包时保留静音时间，已有文件不会覆盖。
每次会话仅允许一个录音。磁盘写入失败、写入速度不足或达到 WAV 4 GB 上限会报告错误，
不会静默丢弃录音。正常停止会话时会完成 WAV 文件头；强制杀进程或断电不保证文件完整。

只录一个节点：

```sh
wirectl talk status
wirectl talk record start ./one-peer.wav --peer NODE_ID
# 也可使用 status 中的 IP:端口
```

`status` 显示节点的完整会话 ID 和地址。选择地址时会解析为当前 ID；ID 在该节点重启后变化，
需要停止并重新选择。目标暂时离线期间记录静音，不会自动录入另一个节点。
`record status --json` 可供脚本读取状态、文件路径、累计字节和错误。

## 音频文件作为输入源

```sh
wirectl talk input start ./music.mp3                 # 替代麦克风
wirectl talk input start ./music.flac --mode mix     # 与麦克风混音，先停止已有文件输入
wirectl talk input start ./notice.wav --loop         # 循环发送
wirectl talk input pause
wirectl talk input resume
wirectl talk input status
wirectl talk input stop
```

支持本地 WAV、MP3、FLAC（单／双声道，采样率最高 384 kHz），自动转换为通话所用的
16 kHz 单声道音频；无需 ffmpeg。WAV 使用解码库支持的 PCM 格式。
默认 `replace` 替代麦克风，`mix` 饱和混音以免整数溢出。
文件结束／停止后恢复麦克风；替代模式暂停期间发送静音，混音模式暂停期间保留麦克风。
麦克风禁音只影响麦克风，不影响文件发送。输入文件不额外在本地播放。

无麦克风的机器也能保持房间连接和接收播放。输入、输出设备独立管理；设备掉线时状态显示 `offline; reconnecting`，重新接入后自动恢复，房间连接、节点 ID 和静音设置保持不变。
首次 `init`／`pair` 时指定 `--input none` 可禁用麦克风，仅发送文件音频；媒体时钟不依赖硬件设备。
现有配置可把 `input` 改为 `none` 后重启会话。`input status --json` 提供状态、发送帧数和缓冲不足计数。

## 本地音频设备测试

```sh
wirectl talk devices
wirectl talk test input --device INPUT_ID --seconds 10
wirectl talk test output --device OUTPUT_ID --seconds 5
```

不传 `--device` 时优先使用当前房间配置的输入／输出设备，未配置时使用系统默认设备。
`--state-dir` 可选择房间；也可以不配置房间，直接指定 `--device` 测试。不发送网络音频。
输入测试实时显示 RMS 电平条（dBFS）、峰值和削波提示，可边说话边观察。
输出测试以较低音量播放带淡入淡出的 440 Hz 间歇测试音；显示的是生成信号电平，
是否真正从喇叭发声需要听音确认。默认 10 秒，可设置 1–300 秒，Ctrl+C 提前停止。
设备被其他进程独占时先停止占用它的音频会话再测试。
