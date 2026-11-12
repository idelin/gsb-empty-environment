# MySQL握手TLS剥离

中间人清除服务端握手包里的 CLIENT_SSL 能力位，或在 SSL 请求包后夹带明文字节，诱使客户端用明文认证。为Go MySQL 驱动写可导入的握手准入包：配置要求 TLS 时，服务端能力位缺 CLIENT_SSL 就中止；发出32字节 SSLRequest 后，TLS 握手完成前缓冲区里不得已有任何服务端字节；TLS 建立后才发送认证响应，auth_plugin_data 在明文阶段不得用于任何回复。测能力位剥离、提前字节、乱序包。

## netsec 握手准入包

`netsec` 是仅依赖标准库、可直接导入的握手准入（admission）包，位于
`netsec/`，模块路径 `example.org/netsec/q0605/netsec`。

### 安全保证

- **能力位剥离即中止**：`Config.RequireTLS=true` 时，服务端
  InitialHandshakePacket 未置 `CLIENT_SSL`（`0x800`）立即返回
  `ErrTLSRequired`，且此前不发送任何字节。
- **SSLRequest 后零容忍提前字节**：发出固定 32 字节载荷（36 字节上线）
  的 `Protocol::SSLRequest` 后、TLS ClientHello 之前做窥探窗口检查；
  窗口内出现任何服务端字节即返回 `ErrPrematureBytes` 并中止。初始握手
  包后若已夹带额外明文包，在 `Begin` 阶段就会被拦截。
- **乱序包拒绝**：初始握手包序号必须为 0，否则返回 `ErrOutOfOrderPacket`。
- **认证材料明文封存**：`auth_plugin_data`（scramble）在 TLS 建立前
  通过 `PendingHandshake.AuthPluginData()` 取用时只会得到
  `ErrTLSNotEstablished`；只有 `SecureConn.AuthPluginData()` 才返回
  scramble 副本，供加密通道上的 HandshakeResponse 使用。
- **明文出站唯一**：TLS 握手成功前，连接上唯一的出站报文就是 SSLRequest。

### 用法

```go
import "example.org/netsec/q0605/netsec"

secure, err := netsec.Admit(ctx, rawConn, netsec.Config{
    RequireTLS: true,
    TLSConfig:  &tls.Config{ServerName: host, MinVersion: tls.VersionTLS12},
})
if err != nil {
    return err // 能力位剥离 / 提前字节 / 乱序 / TLS 失败均在此中止
}
scramble := secure.AuthPluginData() // 仅 TLS 建立后可用
// 在 secure（*tls.Conn）上发送加密的 HandshakeResponse
```

也可分步使用 `netsec.Begin` 与 `(*PendingHandshake).Upgrade`，便于在
两步之间插入额外策略。`Config.ProbeTimeout` 控制 SSLRequest 后的窥探
窗口（默认 25ms，负值为非阻塞即时探测）。

### 测试

`go test ./...` 覆盖：能力位剥离、初始包夹带、SSLRequest 后注入明文
字节、乱序/重复包、畸形握手、明文阶段认证材料封存，以及一个真实 TLS
端到端正向用例（校验 SSLRequest 序号与长度、明文零认证泄露、TLS 后
scramble 可用）。测试使用自带的、支持读截止时间唤醒的内存管道，无需
绑定端口或外部依赖。
