# 语音本地 API

语音功能已并入根目录的 `wirectl` 程序。CLI 与 UI 通过同一个本地 API 使用房间、通话、设备、媒体输入和录音。首次操作会自动创建音频会话，无需手动启动后台进程。

```sh
wirectl talk init --input none
wirectl talk join
wirectl talk invite
wirectl talk status
wirectl talk mute
wirectl talk unmute
wirectl talk leave
```

`--input none` 适用于无麦克风或只发送文件音频的机器。正常使用麦克风时省略该参数。`join` 连接后即可关闭终端；`leave` 断开本地语音会话，保留配置。`status` 只检查状态；`mute`、`call`、`group` 和 `media` 操作按需就绪。创建配置使用独占写入，重复执行 `init` 不会覆盖已有房间。

Direct 模式可以将来电铃声与通话输出分开。先用 `wirectl talk devices` 查询设备 ID，再配置 USB 听筒的输入、输出，以及另一只 USB 扬声器的铃声输出：

```sh
wirectl talk init --mode direct --input HANDSET_INPUT_ID --output HANDSET_OUTPUT_ID --ring-output SPEAKER_ID
```

`pair` 和带地址的 `join` 也接受 `--ring-output`。同一个播放／暂停键在待机时拨号、来电时接听、通话中挂断；拨号中再按一次可取消。来电只在铃声扬声器播放；接听后，双方语音使用听筒的输入、输出，铃声扬声器保持安静。主动拨号时的回铃音仍在听筒播放。铃声设备掉线不会切换通话设备，也不会阻塞通话；`status --json` 的 `ring_output_device` 报告独立铃声设备的状态和电平。通话音量键只调整通话音量，`mute output` 同时静音通话和铃声。

省略 `--ring-output` 或选择与通话相同的输出时，继续使用原有单输出方式。USB 播放／音量键由可选 `spacetouch` 插件负责，树莓派板卡按钮及数码管由 `sakshat` 插件负责。Talk 不再自动抢占 USB 按键设备；需要时执行 `wirectl plugin enable spacetouch` 或 `wirectl plugin enable sakshat`，详见[硬件插件](../../plugins/README.md)。

Linux USB 设备 ID 使用 ALSA 声卡名称，声卡编号改变后仍能保持通话和铃声角色。旧编号 ID 保留兼容；已有配置可用 `devices set` 保存当前枚举出的稳定 ID。

已有房间无需重新配对。先断开本地会话，再保存音频选择并重连；省略的选项保留原值，`--ring-output ''` 恢复共享输出：

```sh
wirectl talk leave
wirectl talk devices set --input HANDSET_INPUT_ID --output HANDSET_OUTPUT_ID --ring-output SPEAKER_ID
wirectl talk join
```

## 资源

以下路径相对于统一入口 `/v1/talk`。认证、监听地址和 endpoint 文件由根目录 API 管理。

| 方法 | 路径 | 行为 |
|---|---|---|
| GET | `/config` | 查询本机配置；不返回房间密钥 |
| POST | `/config` | 创建配置，接受 `mode`、`listen`、`input`、`output`、`ring_output`、`name`、`peers`、`headphones` |
| PATCH | `/config` | 会话断开后修改本机 `input`、`output`、`ring_output`；只更新传入字段，保留房间和配对 |
| POST | `/session` | 连接已保存房间；重复调用复用同一会话 |
| DELETE | `/session` | 断开本地会话，保留配置 |
| GET | `/status` | 查询状态、成员、设备、音量、电平和媒体状态；不会主动打开音频设备 |
| GET | `/devices` | 枚举本地音频设备 |
| POST | `/invitations` | 创建一次性邀请，接受可选 `room`；返回 `address`、`code`、`expires` |
| POST | `/pair` | 接受 `address`、`code` 和本地配置字段，通过 PAKE 加入并保存房间 |
| GET | `/groups` | 获取所有房间和成员状态 |
| POST | `/groups` | 修改房间、选中发言房间、监听状态或播放增益 |
| POST | `/call` | 接受 `action` 和可选 `group`，控制 `dial`、`answer`、`hangup`、`button` |
| POST | `/media?group=ROOM` | 控制录音或文件音频；省略 group 时选择当前房间 |
| POST | `/mute`、`/unmute` | 设置本机会话麦克风静音 |
| POST | `/mute/output`、`/unmute/output` | 设置本机会话播放静音 |

`POST /groups` 复用 `GroupChange` 结构，`action` 支持 `add`、`use`、`mute`、`unmute`、`rename`、`leave`、`mode`、`output-gain`、`peer-gain`。`mode` 只能在会话断开时修改；先调用 `DELETE /session`。其他房间操作会自动就绪会话。

`POST /media` 的 `action` 支持 `record-start`、`record-stop`、`input-start`、`input-stop`、`input-pause`、`input-resume`。可选字段为 `file`、`peer`、`mode`、`loop`。文件路径由本机运行时访问，UI 应传绝对路径。

未配置时，需要会话的操作返回 HTTP 409 并提示创建或加入房间。配置校验和配对失败返回 HTTP 422。重复创建配置返回 HTTP 409。其他控制错误保留底层 HTTP 状态，不会要求用户启动 daemon。

## 安全与验证

保留原有 PAKE 配对、AES-GCM、重放检查、私有状态目录、单会话文件锁和设备重连机制。本地 API 不改变语音协议，也不代表协议通过了独立安全审计。房间成员共享密钥；移除受信任成员仍需重新创建房间密钥。录音由显式操作开启，不会自动记录音频。

新增 API 验证先于实现，覆盖配置拒绝覆盖、自动启动的并发请求、静音与恢复、断开后重连、房间模式修改、真实 TCP 配对及配置落盘。执行根目录 `make check`；测试驱动 `talk_test_audio` 不代表实机音频验证。

迁入前的功能细节和协议限制保存在 [原说明](docs/LEGACY.md)。
