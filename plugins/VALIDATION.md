# 硬件插件验收记录

2026-11-14，两端运行版本为 `1.0.5-dev.plugins.20261114`。本记录区分自动化测试、真实 SDK 操作和人工按键确认；音频质量与双向语音不在本轮验收范围内。

## 部署与最终状态

| 环境 | 启用插件 | 通话设备 | 铃声设备 | 最终状态 |
|---|---|---|---|---|
| Mac mini | `spacetouch` | SPACETOUCH，CoreAudio 名称为 `USB Audio` | `Mac mini Speakers` | 插件在线，通话空闲 |
| 树莓派 `10.250.0.7` | `sakshat` | `USB PnP Audio Device` | 使用同一个 USB PnP 输出 | 插件在线，通话空闲，SDK 已完成数码管熄灭写入 |

两端均只启用对应插件。更新前后的房间密钥、对端、模式、监听地址及音频设备设置逐项比较一致，结果只保留布尔值。最终检查未发现测试进程或测试通话遗留。

树莓派保持测试前的输出静音状态。USB PnP 输出在线，输入仍报告 `device stopped delivering audio; retrying`。按用户要求停止音频优化，本轮结果不能视为完整双向语音已通过。

## 验证结果

| 检查 | 结果 | 证据 |
|---|---|---|
| 测试先行 | 旧版本不识别 `plugin`，新增实现前已保留失败结果 | `.cache/evidence/swiss-plugin/plugins-before.log` |
| 插件进程 E2E | 4 项通过，覆盖真实 CLI、API、配对、UDP 通话及 SDK 子进程；GPIO 和 USB 按键由测试构建模拟 | `.cache/evidence/plugins-process-e2e.json` |
| 插件 race 构建 E2E | 4 项通过 | `.cache/evidence/swiss-plugin/plugins-race.log` |
| Talk 提取后的 race 构建 E2E | 8 项通过 | `.cache/evidence/swiss-plugin/talk-after-extraction.log` |
| 完整检查 | `make check` 通过，退出码为 0 | `.cache/evidence/swiss-plugin/make-check-final.log` |
| 树莓派原生 SDK | 加载已安装的 `sakshat` SDK `version 0.0.2`，读取真实 GPIO 电平，4 次显示／熄灭写入获得 ACK，退出码为 0 | `.cache/evidence/swiss-plugin/sdk-native.json` |
| Mac 实体播放／暂停键接听 | 来电时按键，动作数由 1 增至 2，两端从来电／拨号转为已接通；用户确认接听 | `.cache/evidence/swiss-plugin/live-incoming.json` |
| Mac 同键挂断 | 再次按键，动作数由 2 增至 3，两端回到空闲；用户确认「已按，已经挂断」 | `.cache/evidence/swiss-plugin/live-incoming.json` |
| 通话状态驱动树莓派显示 | 拨号时交替显示／熄灭 `6702`，接通后常亮，挂断后熄灭；依据真实 SDK 写入 ACK | `.cache/evidence/swiss-plugin/live-incoming.json` |

首轮人工拨号监听超时，保留为失败记录：`.cache/evidence/swiss-plugin/live-plugins.json`。后续来电测试开始前，插件动作数已为 1；这证明额外按键动作已被接收，但该轮没有留下完整的拨号状态转换记录。拨号逻辑通过进程 E2E 验证，人工接听、挂断由后续成功记录确认。

## 尚未现场确认

树莓派 SDK 和 GPIO 已实测，但无人现场按压套件左、右按钮，也无人观察数码管外观。因此，实体按钮操作和屏幕显示效果仍待现场确认。模拟 GPIO 的 E2E 已覆盖左键拨号／接听、右键取消／挂断，以及启动按住、按住不重复、同时按键和驱动重连。

显示状态中的 `visible` 表示 SDK 完成对应写入，不能替代人工或相机观察。Mac 插件的实体接听、挂断结果不代表树莓派实体按钮已通过验收。

## 重复执行

在仓库根目录运行：

```sh
make check
```

完整检查会构建测试程序并串行运行各进程 E2E，结果写入 `.cache/evidence/`。插件测试产物位于 `.cache/e2e/plugins-process/`，包括 SDK 显示轨迹及进程日志；测试构建使用模拟硬件，正式部署构建不启用模拟回退。

已有测试程序时，可分别重跑插件与 Talk 验证：

```sh
WIRECTL_TEST_BINARY="$PWD/bin/wirectl-e2e" PYTHONDONTWRITEBYTECODE=1 python3 tests/e2e/test_plugins_api.py
WIRECTL_TEST_BINARY="$PWD/bin/wirectl-e2e" PYTHONDONTWRITEBYTECODE=1 python3 tests/e2e/test_talk_api.py
```

真实硬件测试需要独立安排观察人员。本轮原始记录、最终状态及汇总位于 `.cache/evidence/swiss-plugin/`，汇总文件为 `results.json`。部署备份含私有配置，仅保存在本地缓存，不提交到仓库。
