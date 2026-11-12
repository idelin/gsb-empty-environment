# NTS扩展字段与Cookie一次性

NTP服务器应用NTS时，攻击者可重放旧请求或篡改扩展字段拨回时钟。为Go时间服务实现扩展字段校验包，供UDP请求处理调用：Unique Identifier至少32字节且响应原样回显，NTS Cookie只用一次并随响应补发，NTS Authenticator覆盖完整报文头及其前所有扩展，认证失败不回复时间。用重放请求、复用Cookie、改UID和合法请求验证。

## 实现（RFC 8915，仅依赖标准库）

- `server.go`：`Server.ProcessPacket` 是 UDP 请求处理的入口，按“结构校验 →
  Cookie 解封装与 Authenticator 验证 → UID/Cookie 原子化一次性登记”顺序执行，
  任一失败都返回错误且不产生响应字节；`ServeUDP` 直接在 `net.UDPConn` 上循环调用。
- `extension.go`：RFC 5905 §7.5 扩展字段编解码，强制 UID 至少 32 字节、
  UID 位于首个扩展、Cookie 位于 Authenticator 之前、Authenticator 收尾。
- `authenticator.go`：AEAD 封签/验证，AAD 为完整 48 字节 NTP 头加上
  Authenticator 之前的全部扩展原始字节，头部或 UID/Cookie 被改一比特即失败。
- `cookie.go`：不透明 Cookie 封装会话流量密钥（C2S/S2C），密钥环支持轮换，
  旧密钥仍可解封存量 Cookie；每条成功响应用当前密钥补发一枚新 Cookie。
- `cache.go`：带 TTL 与 LRU 上限的一次性缓存，分别记录已消费的 UID 与 Cookie，
  检查与登记在同一把锁内完成，避免并发下的重放竞态。
- `aead.go`：RFC 8915 §5.1.3 的 AES-128-GCM / AES-256-GCM 注册表。

## UDP 服务接入示例

```go
srv, err := netsec.NewServer(netsec.AEADAESGCM128, cookieProtectionKey)
if err != nil {
    log.Fatal(err)
}
conn, _ := net.ListenUDP("udp", &net.UDPAddr{Port: 123})
log.Fatal(srv.ServeUDP(conn)) // 认证/重放/复用失败的请求静默丢弃，不回复时间
```

## 验证

`go test ./...` 覆盖：合法请求（UID 原样回显、补发新 Cookie、客户端 S2C 验签）、
重放请求（同 UID 二次拒绝、TTL 过期后放行）、Cookie 复用（跨请求同 Cookie 拒绝）、
篡改 UID/NTP 头/Authenticator（统一认证失败且无响应）、短 UID 与畸形报文、
Cookie 密钥轮换，以及 `ServeUDP` 的端到端回环（无 socket 权限的沙箱中自动跳过）。
