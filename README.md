# 导出认证器连接绑定

本包实现 [RFC 9261 Exported Authenticators in TLS](https://www.rfc-editor.org/rfc/rfc9261)，并强制把认证器绑定到生成它的 TLS 连接。

## 安全边界

- `CertificateVerify` 输入严格为 `64*0x20 || "Exported Authenticator" || 0x00 || Hash(Handshake Context || authenticator request || Certificate)`。
- `Handshake Context` 与 `Finished MAC Key` 都由调用方注入的 `Exporter` 导出，导出标签按客户端/服务器角色选择；默认哈希为 SHA-256。
- 若连接协商的不是 SHA-256，调用方必须用 `WithHash` 传入该密码套件的认证器哈希；导出密钥、转录哈希与 Finished HMAC 会同步使用该哈希。
- `Finished` 为 `HMAC(Finished MAC Key, Hash(Handshake Context || authenticator request || Certificate || CertificateVerify))`。
- `IssueRequest` 生成不可预测的 `certificate_request_context`。`Verify` 在解析任何攻击者可控数据前原子消费该上下文；缺失、过期或重复使用时直接拒绝。
- 验证器应每个 TLS 连接一个，或至少每次验证都传入该连接的 `Exporter`。跨连接搬运会因连接导出的上下文及 Finished 密钥不同而失败。
- 证书链必须由调用方显式提供的根证书池验证；生产代码应通过 `WithKeyUsages` 收紧 EKU。

## 用法

```go
roots := x509.NewCertPool()
roots.AddCert(trustedCA)

verifier, err := netsec.NewVerifier(
    roots,
    netsec.WithRole(netsec.RoleServer),
    netsec.WithKeyUsages(x509.ExtKeyUsageServerAuth),
)
if err != nil {
    return err
}

request, err := verifier.IssueRequest()
if err != nil {
    return err
}
// 把 request.Message() 发给将要生成认证器的一端。

authenticator, err := netsec.BuildAuthenticator(request, connectionState, netsec.SignOptions{
    Certificate: leaf,
    Chain:       intermediates,
    PrivateKey:  leafKey,
    Scheme:      netsec.SignatureEd25519,
})
if err != nil {
    return err
}

// connectionState 必须是接收认证器这条连接的 *tls.ConnectionState。
identity, err := verifier.Verify(authenticator, request, connectionState)
```

测试覆盖：

- 从连接 A 搬到连接 B（不同导出器、不同请求上下文）
- 请求缺失或属于另一个验证器
- 同一请求重放（含并发重放）
- 篡改 `CertificateVerify` 签名、`Finished`、请求上下文
- 请求过期
