# SOPS 密钥组与阈值元数据校验

SOPS 用密钥组把数据密钥分给多组主密钥，要求每组各出一把，并可设阈值。攻击者可能向元数据加入自己的 age/KMS 条目、降低阈值或删去一组，从而获得解密能力。本包在解密前锁定元数据结构，并在解密后校验文件 MAC。

## 使用

```go
policy := netsec.Policy{
    KeyGroups: []netsec.ExpectedKeyGroup{
        {Age: []string{"age1..."}, PGP: []string{"AABB..."}},
        {KMS: []string{"arn:aws:kms:...:key/..."}},
        {Vault: []string{"https://vault.example/v1/transit/keys/..."}},
    },
    ShamirThreshold: 2,
    MinVersion:      "3.8.0",
}

validator, err := netsec.NewValidator(policy)
if err != nil {
    return err
}

metadata, err := netsec.ParseMetadata(encryptedFileOrMetadataJSON)
if err != nil {
    return err
}
if err := validator.Validate(metadata); err != nil {
    return err
}

// Decrypt the file and collect every plaintext value in SOPS document order.
// The string passed here is the decrypted metadata mac, not its ENC[...] form.
if err := netsec.VerifyMAC(decryptedMAC, allPlaintextValues); err != nil {
    return err
}
```

`ExpectedKeyGroup` 的 `PGP`、`KMS`、`Age`、`Vault` 切片分别登记条目的指纹：PGP `fp`、KMS `arn`、age `recipient`、Vault `vault_address`。比较时忽略组顺序和条目顺序，但会保留条目类型与重复次数；多出、缺失、重复或替换类型的条目都会拒绝。

## 校验规则

- 密钥组数量、每组条目类型/指纹/数量必须与登记策略完全一致，组顺序无关。
- `shamir_threshold` 必须不小于策略阈值，且不大于密钥组数量。
- `mac` 必须存在；解密后对全部值按 SOPS 文档顺序重算 SHA-512，并用 `crypto/subtle` 常量时间比较。
- `version` 必须不低于策略中的最低版本；支持常见语义版本及预发布版本比较。
- JSON 解析支持直接传入 `sops` 元数据，也支持传入带 `data` 和 `sops` 的完整加密文件。

测试覆盖合规文件、多出 age 条目、降低阈值、删除组、缺失 mac、版本过低及 MAC 篡改。
