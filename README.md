# 密钥目录标识绑定组件

公钥查询按短标识返回结果，攻击者上传碰撞标识的公钥，让客户端加密给错误的接收者。
本包提供供 Go 密钥目录服务调用的可导入标识绑定组件：

- 查询必须同时提供**完整指纹**与**用户标识**，两者都匹配才返回密钥。
- 短标识只作为展示/一致性提示，永远不会单独解析出可加密的密钥。
- 短标识碰撞、同一用户对应多把密钥、未验证绑定一律拒绝。
- 绑定必须由该公钥对应的私钥签名，导入时和查询时都会重新校验。
- 撤销证书必须由被撤销的密钥签名；撤销状态不可被旧记录重新导入覆盖。
- 完整指纹由公钥材料派生，声明他人指纹但携带自己的公钥会被拒绝。

## 用法

```go
directory := netsec.NewDirectory()

record, err := netsec.NewBoundKey(publicKey, privateKey, "alice@example.com")
if err != nil {
    return err
}
if err := directory.Import(record); err != nil {
    return err
}

got, err := directory.Lookup(netsec.Query{
    Fingerprint: record.Fingerprint, // 完整指纹，必需
    UserID:      "alice@example.com", // 用户标识，必需
    ShortID:     record.ShortID,      // 仅提示，可选
})
if err != nil {
    return err
}
// 只有 got.PublicKey 才是可以加密的目标公钥。
```

已有绑定记录也可以直接导入：

```go
record := netsec.KeyRecord{
    Fingerprint: fingerprint,
    UserID:      "alice@example.com",
    PublicKey:   publicKey,
    Bindings: []netsec.Binding{{
        UserID:      "alice@example.com",
        Fingerprint: fingerprint,
        Algorithm:   netsec.AlgorithmEd25519,
        Signature:   detachedSignature,
    }},
}
if err := directory.Import(record); err != nil { /* ... */ }
```

签名覆盖的规范字节由 `BindingStatement(fingerprint, userID)` 生成，撤销证书覆盖
`RevocationStatement(fingerprint, userID)`（`userID` 为空表示整把密钥）。

## 错误分类

`Lookup` 返回的 `ErrFingerprintRequired`、`ErrUserMismatch`、`ErrNotFound`、
`ErrShortIDCollision`、`ErrMultipleResults`、`ErrUnverifiedBinding` 和
`ErrRevoked` 都可以用 `errors.Is` 判断。任何错误都意味着调用方不得使用查询结果加密。

## 测试

```sh
go test ./...
```

测试覆盖短标识碰撞、指纹替换、未验证绑定、合法双因子查询、撤销后不可复活，
以及 RSA/ECDSA 密钥的签名校验。
