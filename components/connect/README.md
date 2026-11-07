# Connect

Connect 已并入 [wirectl](../../README.md)。源码、测试、版本和发布均由当前仓库维护；安装及更新统一使用 `wirectl`。

```sh
wirectl connect setup
wirectl connect login vpn.example.com
wirectl connect vpn.example.com
# 另一台设备输入首台设备显示的短码
wirectl connect vpn.example.com 7k3m-f8q2-h6tw
```

`setup` 只负责安装创建 TUN、地址和主机路由所需的特权 helper，需要系统管理员授权。服务器登录、配对凭据、WireGuard 私钥和连接运行时归当前账号所有。

连接操作通过本地 API 执行。首次调用自动使运行时就绪；后台连接由统一运行时持有，关闭 CLI 不会停止连接。运行时重启后会恢复此前需要运行的已配对连接；显式停止的连接保持停止。`--foreground` 表示 CLI 等待并在取消时停止当前连接，不安装独立的用户服务。

```sh
wirectl connect status
wirectl connect status --watch
wirectl connect stop --name default
wirectl connect resume --name default
wirectl connect delete --name default
wirectl update
```

`stop` 保留配对资料。`delete` 停止连接并删除当前名称的配对资料，服务器授权和其他连接保留。需要隔离多个运行时时，设置 `WIRECTL_HOME`；凭据存放在其 `connect/` 目录。

## 本地 API

以下资源挂载于统一运行时的 `/v1/connect`，认证与回环监听由运行时提供。

| 方法 | 路径 | 用途 |
|---|---|---|
| `GET` | `/connections` | 列出已保存及正在配对、运行的连接 |
| `GET` | `/connections/{name}` | 查询连接状态及创建过程中的配对码 |
| `POST` | `/connections` | 创建或加入配对，完成后运行连接 |
| `POST` | `/connections/{name}/resume` | 恢复已保存的配对 |
| `DELETE` | `/connections/{name}` | 停止连接，保留配对资料 |
| `DELETE` | `/connections/{name}?forget=true` | 停止连接并删除配对资料 |
| `POST` | `/credentials` | 使用服务器授权令牌注册设备 |
| `GET` | `/doctor` | 检查网络前置条件 |

创建请求示例：

```json
{"name":"default","server":"https://vpn.example.com"}
```

加入请求示例：

```json
{"name":"default","server":"https://vpn.example.com","code":"7k3m-f8q2-h6tw"}
```

可选字段包括 `host`、`network`、`interface`、`mtu`、`stun_url`、`relay_only` 和 `replace`。缺省地址范围为 `100.64.0.0/10`，MTU 为 `1420`。`replace` 只允许在完成新的认证配对后替换同名资料。

创建和恢复返回 `202`，随后查询连接资源。`state` 可能为 `pairing`、`connecting`、`running`、`stopped` 或 `failed`。配对码只在配对期间返回；凭据和私钥不出现在状态响应中。服务器未登录、helper 未就绪或参数有误时，API 返回具体原因。

## 公网服务与协议

Linux 公网服务器继续使用 `wirectl connect serve`。详细协议与部署资料见 [PROTOCOL.md](docs/PROTOCOL.md) 和 [deploy/README.md](deploy/README.md)。直连与中继继续复用原有 ICE/STUN、HTTPS/DERP 和 WireGuard 实现。

验证区分本地真实协议传输、操作系统 helper、以及公网 NAT 场景。本地使用内存 TUN 的协议测试不会修改主机路由，也不代表公网 NAT 或物理网卡已经验证。
