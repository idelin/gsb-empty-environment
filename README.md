# ticketkeys

TLS服务吊销客户端证书后，客户端仍能用旧会话票据恢复连接。给Go TLS服务实现可挂到tls.Config的WrapSession与UnwrapSession的会话恢复组件：票据用可轮换密钥加密，解密接受新旧两代密钥；票据绑定SNI、客户端证书指纹和签发时的策略代次，恢复前重新检查吊销和期限，不满足时回落完整握手。用真实握手测试密钥轮换、证书吊销和跨SNI复用。

## 用法

```go
policy := ticketkeys.NewPolicy()
issuer, _ := ticketkeys.NewIssuer(policy, ticketkeys.WithLifetime(2*time.Hour))
issuer.Install(serverTLSConfig) // 设置 WrapSession / UnwrapSession

// 定期轮换密钥：旧票据仍可用一代，再早的票据强制完整握手
go func() {
    for range time.Tick(24 * time.Hour) {
        issuer.Rotate()
    }
}()

// 吊销客户端证书：已签发的票据立即失效，恢复被拒绝并回落完整握手
policy.Revoke(ticketkeys.Fingerprint(clientCert))

// 策略整体变化时，使所有已签发票据失效
policy.BumpGeneration()
```

要点：

- 票据明文包含 SNI、客户端证书 SHA-256 指纹、签发时的策略代次和过期时间，
  用 AES-256-GCM 加密，密钥头部带密钥代次 ID，`UnwrapSession` 接受当前与上一代密钥。
- TLS 1.3 恢复握手不会重新出示客户端证书，因此吊销检查必须在 `UnwrapSession`
  内完成；任何校验失败都返回 `(nil, nil)`，由 crypto/tls 自动回落完整握手。
- 如需在完整握手中同样拦截被吊销证书，可将 `policy.VerifyPeerCertificate`
  赋给 `tls.Config.VerifyPeerCertificate`。
- 测试（`ticketkeys_test.go`）用真实 TLS 1.3 握手覆盖：密钥轮换（一代可恢复、
  两代拒绝）、证书吊销（回落完整握手 / 配合 VerifyPeerCertificate 彻底拒绝）、
  跨 SNI 复用拒绝、票据过期、策略代次失效。
