# Go 包指纹与签名身份存储

包管理器对 registry 包采用首次信任并记录签名身份，攻击者在新版本里换成自己的签名者，或把已签名包降级成未签名。为Go实现可导入的指纹与签名身份存储包：键为小写化的 scope.name@version，首次出现记录 checksum 与签名者；同版本 checksum 变化拒绝；新版本由不同签名者签出时默认拒绝，仅当调用方显式信任该变更才接受并改写记录；从已签名降为未签名一律拒绝；scope 与 name 按 ASCII 大小写不敏感且只允许字母数字及连字符。用换签名者、降级、同版本篡改和大小写别名验证。

## 用法

```go
store := netsec.NewStore() // 零值 netsec.Store 也可直接使用

artifact := netsec.Artifact{
    Scope:    "Acme-Corp",
    Name:     "Widget",
    Version:  "1.0.0",
    Checksum: "sha256:0123",
    Signer:   "alice@example.org", // 空字符串表示未签名
}

_, err := store.Verify(artifact)
if err != nil {
    // 首次出现时记录；否则执行 checksum/签名者策略
}
```

签名者变化必须显式授权；即使显式授权，也不能绕过同版本 checksum 校验或已签名到未签名的降级拒绝：

```go
_, err := store.VerifyTrustingSignerChange(artifact)
```

按 `scope.name@version` 直接校验：

```go
_, err := store.VerifyKey("acme-corp.widget@1.0.0", "sha256:0123", "alice@example.org")
```

可用 `errors.Is` 区分拒绝原因：`ErrChecksumMismatch`、`ErrSignerChanged`、`ErrUnsignedDowngrade`、`ErrInvalidKey` 和 `ErrEmptyChecksum`。

## 验证

```sh
go test ./...
go test -race ./...
```
