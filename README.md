# 接入认证外内身份绑定

企业网络接入同时存在匿名外层身份与受保护内层身份，代理不能把别的会话内层成功套给当前端口。实现供Go接入宿主调用的EAP式状态包，内层结果绑定隧道导出值、交换摘要及物理端口实例，外层只用于路由不得成为授权主体。用两个并行隧道、同外层名字和端口重接测试成功证明不可跨会话拼接。

## 实现模型

`netsec.Authenticator` 维护物理端口实例、隧道和授权凭据三组状态：

1. 宿主在每次物理连接事件调用 `AttachPort(name)`。同名端口重接会产生新的
   `instance`，并立即作废旧实例上的全部隧道和 `Grant`。
2. `BeginTunnel(port, outerIdentity)` 处理匿名外层身份。外层身份仅可通过
   `Route`/`OuterIdentity` 读取，用于选择代理路由，不会进入 `Principal`。
3. `BindExporter(tunnel, exporter)` 绑定受保护隧道导出的密钥材料。
4. `RecordInnerExchange(tunnel, packet)` 按顺序记录内层 EAP 交换；摘要包含
   消息边界和最终成功报文。
5. `CompleteInner(tunnel, innerIdentity, success)` 只从受保护内层身份生成
   `Grant`。内层身份为空时直接失败，绝不回退到外层身份。
6. `Authorize(grant, port, tunnel)` 校验凭据是否属于当前物理端口实例和当前
   隧道，通过后才返回以受保护内层身份命名的 `Principal`。

`Grant` 的绑定 MAC 覆盖：

- 隧道导出值；
- 内层交换摘要；
- 物理端口名称、随每次连接重新生成的实例值及代数；
- 隧道实例；
- 受保护内层身份。

因此，两条并行隧道即使外层身份和物理端口名称完全相同，也不能互相套用内层
成功结果；同一端口重接后，旧隧道和旧 `Grant` 均不可用于新连接。

## 宿主调用

```go
authenticator := netsec.NewAuthenticator()

// One call per physical connection. Reconnecting "eth0" invalidates the old
// port instance, its tunnels, and its grants.
port, err := authenticator.AttachPort("eth0")

tunnel, err := authenticator.BeginTunnel(port, "anonymous@realm.example")
if route, _ := authenticator.Route(tunnel); route != "anonymous@realm.example" {
    // Outer identity is routing metadata only.
}

err = authenticator.BindExporter(tunnel, exporterFromTLS)
err = authenticator.RecordInnerExchange(tunnel, innerEAPPacket)
grant, err := authenticator.CompleteInner(tunnel, "alice@corp.example", true)

principal, err := authenticator.Authorize(grant, port, tunnel)
// principal.Name is "alice@corp.example"; the outer identity is absent.
```

## 测试

```sh
go test ./...
go test -race ./...
go vet ./...
```

- `TestParallelTunnelsWithSameOuterIdentityCannotBeSpliced`：同一端口、同一外层
  身份的两条并行隧道不能交叉使用 `Grant`。
- `TestPortReconnectWithSameNameAndOuterIdentityRejectsOldSuccess`：同名端口
  重接后，旧连接的内层成功不能用于新端口实例。
- `TestGrantBindsExporterExchangeTunnelAndPortInstance`：逐项改变导出值、
  交换摘要、隧道实例和端口实例，验证绑定摘要随之变化。
- `TestConcurrentParallelTunnelsRemainIsolated`：并发创建多条同外层身份隧道，
  在竞态检测下验证彼此隔离。
