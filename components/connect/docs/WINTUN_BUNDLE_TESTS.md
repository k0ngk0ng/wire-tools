# Wintun 更新包验证

新增实现前列出的失败场景：

- 统一入口旁有旧 DLL，更新后的 generation 有新 DLL：必须选择 generation。
- generation 指向相对路径、安装根目录之外或多级目录：必须拒绝，不能搜索当前目录或 PATH。
- 已选择的 generation 缺少 DLL 或许可证：失败并保留错误，不能回退到旧 DLL。
- helper 以独立程序副本运行且继承了 generation 环境变量：必须继续使用其受保护目录内的相邻 DLL 和许可证。
- helper 安装来源为更新后的统一入口：必须把同一个 generation 的 DLL、许可证复制到受保护目录。
- 进程已加载其他路径的 Wintun：必须拒绝混用版本。
- 显式完整路径加载成功：必须保持加载，随后 WireGuard 的 Wintun 懒加载器仍应使用同一个模块；实际模块路径通过 Windows API 核验。

路径选择测试可在所有平台执行。Windows 的实际模块验证使用 `WIRECTL_TEST_WINTUN` 指定官方、已校验的 amd64 DLL；只加载库和查询版本，不创建网卡或安装驱动。测试在独立子进程执行并输出加载路径，CI 日志保留该证据。
