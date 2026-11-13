# DoIP路由激活与源地址占用

DoIP 网关在诊断仪建立 TCP 连接后要先做路由激活，攻击者用未登记的源地址、重复激活，或跳过激活直接发诊断报文，访问任意 ECU。为Go车载网关写可导入的 DoIP 准入包，供 TCP 会话循环调用：头 8 字节含协议版本及其按位取反（两者须互为反码）、载荷类型 2 字节、载荷长度 4 字节；载荷长度超过调用方上限拒绝；路由激活请求（0x0005）里的源地址须在登记表内，激活类型只认 0x00 与 0x01，其余拒绝；每个源地址同时只能占用一个 TCP 连接，已被别的连接占用时返回拒绝而不是替换，除非旧连接已空闲超时；诊断消息（0x8001）在连接未激活前一律丢弃并关闭连接；诊断消息的源地址须等于该连接激活时登记的，目标地址须在该源地址被授权的 ECU 集合内。用反码错误、未登记源地址、占用冲突、未激活发诊断和越权目标验证。

## 接入方式

模块路径为 `example.org/netsec/q0712`，包名为 `netsec`。

```go
gateway := netsec.NewGateway(netsec.GatewayConfig{
    EntityAddress:    0x0e00,
    MaxPayloadLength: 4096,
    IdleTimeout:      2 * time.Minute,
    SourceAuthorizations: map[uint16][]uint16{
        0x0e00: {0x0e01, 0x0e02}, // 诊断仪源地址 -> 可访问 ECU
    },
})

session := gateway.NewSession(remoteAddress) // 每个 TCP 连接一个 Session
defer session.Close()

for {
    header, payload, err := netsec.ReadFrame(conn, gateway.MaxPayloadLength())
    if err != nil {
        return
    }

    decision := session.HandleParts(header, payload)
    if decision.Response != nil {
        if _, err := conn.Write(decision.Response); err != nil {
            return
        }
    }
    if decision.Close {
        return
    }
    if decision.Deliver {
        route(decision.Diagnostic) // 已通过源/目标授权的 0x8001
    }
}
```

如果上层已经完整读取一帧，也可直接调用 `session.Handle(frame)`。为了在测试或调度器中注入时间，使用 `session.HandleAt(frame, now)` 或 `session.HandlePartsAt(header, payload, now)`。

## 准入语义

- 头部的版本字节与反码字节必须异或为 `0xff`；载荷长度严格按 4 字节大端读取，超过 `MaxPayloadLength` 时在分配/读取载荷前拒绝。
- 路由激活只接受类型 `0x00`、`0x01`。未知源地址返回 `0x0006` 拒绝帧（响应码 `0x01`），占用冲突返回 `0x03`，非法激活类型返回 `0x07`。
- 同一源地址只保留一个活动租约。冲突连接被拒绝，旧连接状态不变；仅当旧连接空闲达到 `IdleTimeout` 时才回收，并撤销旧 `Session`。
- 未激活连接收到 `0x8001`、诊断源地址与激活源不一致、或目标 ECU 未授权时，`Deliver` 为 `false` 且 `Close` 为 `true`。
- 激活成功和已交付的诊断会刷新租约的空闲计时。TCP 循环退出时应调用 `Session.Close()` 立即释放源地址。
- `MaxPayloadLength == 0` 使用默认 1 MiB；`IdleTimeout == 0` 使用默认 5 分钟，负值关闭超时回收。

## 验证

```sh
go test ./...
go test -race ./...
```
