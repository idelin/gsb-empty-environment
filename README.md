# TLS委派凭据验证

边缘TLS终止器需要接受RFC 9345委派凭据，攻击者可替换凭据字段、签名方案和证书用途。实现供Go握手服务调用的验证包：解析凭据有效时间、预期CertificateVerify算法和公钥，限制有效期不超过证书及七天；父证书须含DelegationUsage扩展且其签名算法允许签发该凭据。验证凭据签名和随后握手签名时使用各自规定的上下文串，任何一步失败都不得发布连接身份。用算法错配、超期、错误上下文与合法凭据交叉验证。

## 使用方式

```go
verifier := netsec.NewVerifier()

// CertificateVerify 阶段使用握手 transcript 的哈希，而不是原始 transcript。
identity, err := verifier.VerifyConnection(
    leafCert, delegatedCredentialDER, netsec.RoleServer,
    transcriptHash, certVerifyAlgorithm, certVerifySignature,
)
if err != nil {
    return err // 不发布 identity
}
useIdentity(identity)
```

如果握手服务需要把 Certificate 和 CertificateVerify 分两阶段处理，可先调用
`VerifyDelegatedCredential`，再调用返回值的 `VerifyCertificateVerify`。前者不会
返回连接身份，只有后者成功后才返回 `ConnectionIdentity`。

验证器执行以下失败关闭检查：

- 严格解析 RFC 9345 `Credential`/`DelegatedCredential` 字段，包括 24 位公钥长度和 `dc_cert_verify_algorithm`。
- `valid_time` 不超过七天，凭据到期时间不晚于父证书 `NotAfter`，且当前时间不晚于到期时间。
- 父证书当前有效、含非关键 `DelegationUsage`（OID `1.3.6.1.4.1.44363.44`，ASN.1 NULL）和 `digitalSignature` KeyUsage，并匹配对应角色 EKU。
- 凭据签名方案与父证书公钥类型/曲线兼容；委派 RSA 公钥必须使用 RSASSA-PSS OID，不能使用 `rsaEncryption`。
- 凭据签名使用 `TLS, server delegated credentials` 或 `TLS, client delegated credentials`。
- CertificateVerify 方案必须等于 `dc_cert_verify_algorithm`，并使用 `TLS 1.3, server CertificateVerify` 或 `TLS 1.3, client CertificateVerify`。
