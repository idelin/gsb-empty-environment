# SealedSecret 作用域与注解篡改

Sealed Secrets 用控制器公钥加密 Secret，作用域有 strict（命名空间与名称都进入加密标签）、namespace-wide（只含命名空间）与 cluster-wide（无标签）。攻击者可以拿到别的命名空间的密文后改名重放，或把 strict 的密文改注解成 cluster-wide。本包只负责解封前的安全校验，不实现加解密算法。

## 校验规则

- 作用域只由当前对象的注解推导：没有作用域注解为 strict，`sealedsecrets.bitnami.com/namespace-wide=true` 为 namespace-wide，`sealedsecrets.bitnami.com/cluster-wide=true` 为 cluster-wide。
- cluster-wide 与 namespace-wide 两个注解同时为真时直接拒绝。
- 解封所用的标签必须等于当前对象推导出的标签：strict 为 `namespace/name`，namespace-wide 为 `namespace`，cluster-wide 为空字符串。
- 改名重放、跨命名空间重放以及 strict 改成更宽作用域，都会因标签不一致而失败；真实解密中标签还会作为 RSA-OAEP 标签参与运算，密文与该标签绑定。
- 轮换多个控制器私钥时逐把尝试。全部失败时只返回 `ErrDecryptionFailed`，不暴露是标签不匹配还是密文损坏。

## 主要 API

```go
secret := netsec.SealedSecret{
    Namespace:     "team-a",
    Name:          "db-creds",
    Annotations:   map[string]string{},
    EncryptedData: map[string]string{"password": "..."},
}

label, err := secret.ExpectedLabel() // "team-a/db-creds"
err = secret.ValidateDecryption(label, netsec.DecryptionResult{Plaintext: data})

plaintext, err := secret.Unseal(
    netsec.ControllerKey{Name: "old", Decrypt: oldKey.Decrypt},
    netsec.ControllerKey{Name: "rotated", Decrypt: newKey.Decrypt},
)
```

已经完成多次解密、只想校验结果时，使用 `ValidateAttempts`。每个 `DecryptionAttempt` 同时记录该密钥实际使用的标签和结果，只有标签与结果都通过的第一项会被接受。

为了兼容值类型和指针类型，包级 `ValidateDecryption`、`ValidateDecrypted`、`ValidateAttempts` 与同名方法均可使用。

## 测试

```sh
go test ./...
```

测试覆盖合规解封、strict 改名/跨命名空间重放、namespace-wide 与 cluster-wide 的合法移动范围、注解放宽、双注解拒绝、密钥轮换以及两类失败的不可区分性。
