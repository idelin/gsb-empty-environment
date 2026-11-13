# 指定撤销者指纹

一个可导入的 Go 库，用于在密钥环中校验 OpenPGP 主密钥撤销签名。指定撤销者必须按完整指纹匹配，撤销签名还必须能由该公钥验证；仅匹配 64 位密钥 ID（包括密钥 ID 碰撞）不会获得撤销权限。

## 安全规则

- 指定撤销者来自有效自签名中的 hashed `Revocation Key` 子包（类型 12）；unhashed 子包中的声明一律忽略。
- 自签名必须由目标主密钥验证通过，否则其中的指定撤销者声明无效。
- 指定撤销者按完整指纹（20 或 32 字节）精确匹配，不使用密钥 ID 或指纹前缀。
- 撤销签名必须由目标密钥本人或该指纹对应的公钥验证通过。
- 即使另一把密钥与指定撤销者共享同一 64 位密钥 ID，只要指纹不同就不能撤销。
- 已声明且指纹匹配的指定撤销者可以撤销。

## 用法

```go
ring, err := netsec.ParseKeyring(keyringData) // 二进制或 ASCII armor
if err != nil {
    return err
}

target := ring.LookupByFingerprint(targetFingerprint)
if target == nil {
    return netsec.ErrKeyNotFound
}

sig, err := netsec.ParseSignature(revocationPacket)
if err != nil {
    return err
}

if err := netsec.VerifyKeyRevocation(target, sig, ring); err != nil {
    return err
}
```

如果密钥环已经解析出签名者，也可以显式传入签名者公钥：

```go
err := netsec.VerifyKeyRevocationWithKey(target, signerKey, sig)
```

公开 API 还包括 `Entity.DesignatedRevokers`、`Entity.IsDesignatedRevoker`、`VerifyDesignatedRevocation`、`VerifyRevocationPacket` 和 `VerifyKeyRevocationBytes`。

## 支持范围

支持 v4、v5、v6 公钥和签名包，主密钥撤销（`0x20`）以及 RSA、DSA、ECDSA、Ed25519 签名验证。SHA-1、SHA-224、SHA-256、SHA-384、SHA-512 可用于验签；不支持或已弃用的算法会返回 `ErrUnsupportedAlgorithm`。
