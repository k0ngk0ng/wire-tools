# 硬件插件

`sakshat` 和 `spacetouch` 是同一个 `wirectl` 中的可选硬件插件，共用版本和发布流程，独立启停及保存状态。Talk 负责语音与通话资源，插件通过本地 API 控制通话，未启用时不打开硬件。

```sh
wirectl plugin list
wirectl plugin enable sakshat
wirectl plugin status sakshat --json
wirectl plugin disable sakshat
```

启用状态位于 `WIRECTL_HOME/plugins/NAME/config.json`，随运行时自动恢复。启用插件不会创建房间、自动拨号或启动音频会话；按钮操作按需调用 Talk。禁用插件不挂断已有通话。

## 树莓派：sakshat

适用于 NXEZ SAKS 瑞士军刀扩展板的 TM1637 版本。需要树莓派已安装 Python 3、`sakshat` SDK 和 `RPi.GPIO`，以及当前运行时能访问 GPIO。SDK 资料：[板卡介绍](https://shumeipai.nxez.com/swiss-army-knife-shield-for-raspberry-pi)、[SAKS SDK](https://github.com/nxez/SAKS-SDK)。

| 控件 | 行为 |
|---|---|
| 左键，SDK `TACT_LEFT`，BCM 16 | 待机拨号、来电接听；通话中不重复拨号 |
| 右键，SDK `TACT_RIGHT`，BCM 20 | 取消拨号、拒接来电或挂断；待机时不操作 |
| 同时按下两键 | 优先处理挂断，不拨号 |
| 拨号、来电 | 数码管闪烁 `6702` |
| 已接通 | 常亮 `6702` |
| 空闲、插件停止 | 数码管熄灭 |

启动时已经按下的按钮不会触发通话，需先松开再按。按钮有消抖和按住过滤。SDK 驱动在独立进程中运行，只使用数码管 DIO/CLK（BCM 25/5）和两个按钮；清理时仅释放这四个引脚。显示写入与按钮采样分开执行，SDK 显示事务不会阻塞按键读取。驱动失联会报告错误并重试，不结束 Talk 通话。

插件使用系统已安装的 SDK 子组件，不修改 SDK 仓库，也不调用整板初始化中的全局 `GPIO.cleanup()`。插件状态中的显示可见性表示 SDK 已完成相应写入，不代表相机或人工确认了实际显示外观。

## USB 听筒：spacetouch

```sh
wirectl plugin enable spacetouch
wirectl plugin status spacetouch --json
```

在 macOS 和 Linux 读取 Talk 当前所选 USB 听筒的媒体键：同一个播放／暂停键在待机时拨号、来电时接听、通话中挂断，拨号中可取消；音量键每次调整 2 dB。当前支持 `001f:0b23`、`0c76:1203`、`12d1:3a07` 的兼容设备。

Linux 按 ALSA 声卡所属 USB 节点匹配，macOS 按 CoreAudio 型号和序列号匹配。macOS 需要允许读取输入事件。接口在线表示设备已匹配并打开，实体按钮是否发出事件仍需实机确认。

音频输入、输出仍使用 `wirectl talk devices set` 的通用设备设置。插件不会自动改动音频设备、房间密钥或对端地址。

## 验证

`make check` 包含 `tests/e2e/test_plugins_api.py`，使用实际 CLI、HTTP、TCP 配对、UDP 通话和 SDK 子进程。只有显式测试构建才注入模拟 SDK/GPIO 与 USB 按键；发行构建没有硬件模拟回退。

结果在 `.cache/evidence/plugins-process-e2e.json`，进程及显示轨迹在 `.cache/e2e/plugins-process/`。真实 SDK 加载、显示写入与按钮电平检查另存 `.cache/evidence/swiss-plugin/`。无人观察时不将 SDK 写入成功解释为数码管外观或实体按钮已经验收。

两端部署、人工按键结果及尚未现场确认的项目见[验收记录](VALIDATION.md)。
