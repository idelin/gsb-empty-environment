# 凭证发行绑定密钥证明

证明JWT签名通过，但其中的密钥不是即将写入凭证的确认密钥。证明所签的密钥指纹必须等于凭证cnf将写入的值，两者不同则不发行。指纹一致的证明可以发行。可导入的Go库供发行服务调用。证明一把密钥，凭证写入另一把。

## 安全规则

- 仅接受带公钥 `jwk` 的非对称 JWT，拒绝 `none`、对称算法和私钥字段。
- 先使用 JWT Header 中的公钥验证签名，再按 RFC 7638 计算 SHA-256 JWK Thumbprint。
- `cnf.jkt` 只能来自已验签公钥，或与已验签公钥的指纹严格相等。
- JWT 签名有效但 `cnf.jkt` 属于另一把密钥时，返回 `ErrMismatchedConfirmationKey`，发行服务必须拒绝发行。

## 发行服务用法

推荐让库直接生成要写入凭证的 `cnf`，避免服务自行拼装到错误指纹：

```go
cnf, err := netsec.ConfirmationKeyFromVerifiedProof(proofJWT)
if err != nil {
    // 证明无效：拒绝发行
    return err
}

credential.Cnf = cnf
```

如果发行服务已经构造了待写入的 `cnf.jkt`，发行前必须调用强绑定检查：

```go
err := netsec.VerifyProofBinding(proofJWT, netsec.ConfirmationKey{JKT: requestedJKT})
if errors.Is(err, netsec.ErrMismatchedConfirmationKey) {
    // 证明的是 A 密钥，凭证准备写 B 密钥：拒绝发行
    return err
}
if err != nil {
    // JWT、JWK 或签名无效：拒绝发行
    return err
}
```

支持的签名算法为 `RS256`、`RS384`、`RS512`、`PS256`、`PS384`、`PS512`、`ES256`、`ES384`、`ES512` 和 `EdDSA`。库仅依赖 Go 标准库。
