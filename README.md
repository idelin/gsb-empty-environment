# 会话票据跨SNI复用

会话票据可被攻击者搬到别的虚拟主机：先在宽松的主机上拿到票据，再拿去恢复到要求客户端证书的主机，借恢复跳过认证。为Go TLS 服务端写供 PSK 处理调用的票据准入包：票据由当前或前一代票据密钥（调用方给出两代）加密，内容绑定 SNI、ALPN、客户端证书 SHA-256（无证书为空）、密码套件和签发时间；恢复时 SNI 须与票据一致；目标虚拟主机要求客户端证书而票据没有证书指纹时拒绝恢复，改走完整握手；票据年龄超过 7 天拒绝；携带早期数据时，由 obfuscated_ticket_age 还原的年龄与服务端计算的相差超过 10 秒则拒绝早期数据（仍可恢复）；无法用两代密钥解密的票据视为未知，不得终止握手。用跨 SNI 搬运、无证书票据恢复到需证书主机、旧一代密钥、三代之前的票据和年龄偏差验证。

## 实现：`ticketadmit`

零外部依赖（仅用标准库 `crypto/aes`、`crypto/cipher`、`crypto/hkdf`、
`crypto/sha256`、`crypto/tls`），票据采用 AES-256-GCM 认证加密。

- `key.go`：`KeyRing` 保存当前与前一代密钥；`Seal` 只用当前代，`Open`
  先试当前代再试前代；`Rotate` 轮换后，再老一代（三代之前）的票据变为未知。
- `claims.go`：绑定声明（SNI、ALPN、客户端证书 SHA-256、密码套件、签发时间）
  的版本化二进制编码；无证书时指纹字段缺省。
- `ticket.go`：信封 `magic|version|generation|age_add|nonce|GCM(claims)`，
  头部整体作为 AEAD 的 AAD；解密失败统一返回 `ErrUnknownTicket`。
- `admit.go`：准入决策 `Reject / Resume / ResumeEarlyData`，含 7 天寿命、
  SNI 绑定、客户端证书姿态、ALPN/套件绑定，以及 10 秒早期数据年龄偏差
  （仅降级 0-RTT，不影响恢复）。
- `tls_adapter.go`：实现 `tls.Config.WrapSession` / `UnwrapSession`；
  未知或被拒票据一律返回 `(nil, nil)`，crypto/tls 据此回退完整握手而不告警。

### 接线

```go
current, _ := ticketadmit.GenerateKey(2)
previous, _ := ticketadmit.GenerateKey(1)
ring, _ := ticketadmit.NewKeyRing(current, previous)
admitter, _ := ticketadmit.NewAdmitter(ring)

policy := func(sni string) ticketadmit.VhostPolicy {
    if sni == "strict.example" {
        return ticketadmit.VhostPolicy{RequireClientCert: true}
    }
    return ticketadmit.VhostPolicy{ExpectNoClientCert: true}
}
adapter, _ := ticketadmit.NewTLSAdapter(admitter, policy)
adapter.Configure(tlsConfig) // 每个虚拟主机的 tls.Config 都接同一 adapter
```

非 crypto/tls 的 PSK 栈（如 QUIC）可直接调用 `Admitter.Issue` 与
`Admitter.Admit`，早期数据判断用 `DeobfuscateTicketAge`。

### 验证

`go test ./...` 覆盖：跨 SNI 搬运拒绝、无证书票据恢复到需证书主机被拒、
旧一代密钥可解密、两次轮换后三代之前票据变未知、7 天过期边界、
10 秒早期数据偏差（含 `ticket_age_add` 回绕）以及真实 TLS 1.3 内存回环
（完整握手、合法恢复、篡改票据不终止握手）。
