# 拒绝显式曲线参数

EC 密钥用显式参数编码时，参数里可能藏着一条弱曲线。本库只接受用命名曲线
OID 编码的 EC 公钥；显式参数（ASN.1 `SEQUENCE`）直接失败，即使参数描述的
是 P-256，或公钥点的大小看起来像 P-256。命名曲线 P-256 的证书正常继续。

```go
import netsec "example.org/netsec/q0268"

cert, err := netsec.ParseCertificate(der)
if err != nil {
    // errors.Is(err, netsec.ErrExplicitECParameters) 表示使用了显式参数。
    return err
}

// 证书路径中也可以先解析、再逐张检查。
if err := netsec.CheckCertificate(cert); err != nil {
    return err
}
```

主要入口：

- `ParseCertificate` / `ParseCertificatePEM`：解析证书并执行命名曲线策略。
- `ValidateCertificateDER` / `ValidateSubjectPublicKeyInfo`：直接检查 DER。
- `ValidateECParameters`：只接受命名曲线 OID，拒绝显式参数。
- `VerifyCertificateChain`：验证证书路径前检查路径中的每张证书。
