# 扩展OID重复

同一扩展出现两次，路径校验取了较宽的那一次。重复OID直接失败，不合并两次的内容。每个OID只出现一次的证书继续后续检查。可导入的Go库供证书路径调用。写入两条名称约束再做路径验证。

## 行为

- 重复的扩展 OID 会返回 `ErrDuplicateExtension`（或 `*DuplicateExtensionError`），不会合并、取并集或采用其中较宽的约束。
- 所有 OID 都只出现一次的证书会通过重复检查，并继续执行正常的 `crypto/x509` 路径验证（包括签名、有效期、名称约束等）。
- 路径中的每一张证书都会执行重复检查；证书路径、信任锚和中间证书均可传入。

## 用法

```go
import (
    "crypto/x509"

    "example.org/netsec/q0265"
)

if err := netsec.CheckCertificate(cert); err != nil {
    // *DuplicateExtensionError
}

// 先检查整条路径的重复 OID，再执行正常的证书路径验证。
err := netsec.ValidateCertificatePath([]*x509.Certificate{leaf, intermediate, root})
```
