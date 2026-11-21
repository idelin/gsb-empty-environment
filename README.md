# 密钥用途约束组件（keyusage）

为密钥服务实现的用途约束（key usage restriction）组件，在每次密码学调用前统一裁决，并把所有拒绝写入审计。

## 强制执行的规则

1. **用途检查**：密钥按位声明 `Purpose`（sign/verify/encrypt/decrypt/wrap_key/unwrap_key/derive_key），调用必须命中声明用途；签名密钥不能用于加密，加密密钥不能用于签名，否则返回 `ErrPurposeNotAllowed`。
2. **算法白名单**：算法必须出现在策略白名单内，否则返回 `ErrAlgorithmNotAllowed`；白名单可按策略替换。
3. **弱算法拒绝**：3DES、DES、RC4、RSA-1024、SHA-1/MD5 类算法单独标记为弱算法，优先于白名单直接拒绝（即使被加入白名单也拒绝），返回 `ErrWeakAlgorithm` 并附带原因。
4. **派生密钥**：父密钥必须含 `derive_key` 用途才能派生；子密钥用途只能是父用途的子集，否则在派生时返回 `ErrDerivationNotAllowed` / `ErrDerivedPurposeExceedsParent`；调用时对子集关系做二次防御性校验（防绕过构造）。
5. **包装密钥**：wrap/unwrap 只能作用于密钥材料（`TargetKeyMaterial`），包装密钥不得加解密业务数据（`TargetBusinessData`），违规返回 `ErrWrappingKeyMisuse`。
6. **审计**：每次拒绝生成一条 `AuditEvent`（时间、密钥 ID、算法、用途、目标、原因、错误），默认写入线程安全的 `MemoryAuditSink`，也可注入自定义 `AuditSink`。

## 代码结构

- `keyusage/purpose.go`：用途位域与字符串表示
- `keyusage/algorithm.go`：强算法白名单与弱算法清单及原因
- `keyusage/key.go`：密钥模型（派生密钥持有父密钥指针）
- `keyusage/enforcer.go`：`Policy.Check`（调用裁决）与 `Policy.Derive`（派生裁决）
- `keyusage/audit.go`：审计事件、sink 接口与内存实现
- `keyusage/errors.go`：哨兵错误，调用方用 `errors.Is` 匹配
- `keyusage/enforcer_test.go`：错用、弱算法、派生滥用、包装误用测试

## 快速示例

```go
policy := keyusage.NewPolicy()
signingKey := &keyusage.Key{
    ID:        "sig-1",
    Algorithm: keyusage.AlgECDSAP256SHA256,
    Purposes:  keyusage.PurposeSign | keyusage.PurposeVerify,
}

// 允许
_ = policy.Check(signingKey, keyusage.Operation{
    Purpose: keyusage.PurposeSign, Target: keyusage.TargetBusinessData,
})

// 拒绝：签名密钥不能用于加密，并写入审计
if err := policy.Check(signingKey, keyusage.Operation{
    Purpose: keyusage.PurposeEncrypt, Target: keyusage.TargetBusinessData,
}); errors.Is(err, keyusage.ErrPurposeNotAllowed) {
    // 处理拒绝
}

// 派生：子用途必须是父用途子集
parent := &keyusage.Key{ID: "root", Algorithm: keyusage.AlgHKDFSHA256,
    Purposes: keyusage.PurposeDeriveKey | keyusage.PurposeSign}
child, err := policy.Derive(parent, "child-1", keyusage.AlgECDSAP256SHA256,
    keyusage.PurposeSign)
```

## 测试

```sh
go test ./...        # 错用、弱算法、派生滥用、包装误用全覆盖
go test -race ./...  # 含并发安全检查
```
