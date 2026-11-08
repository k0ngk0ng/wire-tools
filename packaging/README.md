# 统一分发

当前有效的构建、校验和包定义生成入口为根目录的 `scripts/package.py`、`scripts/manifests.py` 和 `.github/workflows/release.yml`。Homebrew Formula 与 Scoop Manifest 由同一组已校验归档生成。

`legacy/` 保存原 `homebrew-tap` 和 `scoop-bucket` 中与 wirectl 相关的定义、校验逻辑和工作流快照，来源提交记录在 `ORIGINS.json`。这些快照用于追溯；根仓库不运行它们的定时同步工作流。独立产品 `sactl` 的包定义与同步流程已迁入其源码仓库 [stoneage](https://github.com/k0ngk0ng/stoneage)，不在此仓库维护。

首次统一正式发布前，`Formula/wirectl.rb` 仅提供 `--HEAD` 源码构建入口，`bucket/wirectl.json` 尚不生成。发布时使用真实归档的 SHA-256 生成稳定包定义，并在发布成功后提交回当前仓库。不会把旧插件包的哈希用作统一程序的哈希。
