# 密钥属性互斥与封装信任

HSM 若允许同一把密钥既能封装又能解密，攻击者可让它封装敏感密钥再解密得到明文。为Go的 PKCS#11 访问层写可导入的属性策略包：同一密钥不得同时有 CKA_WRAP 与 CKA_DECRYPT（UNWRAP 与 ENCRYPT 同理）；CKA_SENSITIVE 变真后不可改回，CKA_EXTRACTABLE 变假后不可改回；被封装密钥带 CKA_WRAP_WITH_TRUSTED 时，封装密钥须带 CKA_TRUSTED。

## 使用

模块路径为 `example.org/netsec/q0839`，包名为 `netsec`。包不依赖具体
PKCS#11 实现；`Attribute` 与常见 Go PKCS#11 绑定中的属性结构一致。

```go
import netsec "example.org/netsec/q0839"

attrs := []netsec.Attribute{
    netsec.BoolAttribute(netsec.CKA_WRAP, true),
    netsec.BoolAttribute(netsec.CKA_DECRYPT, false),
    netsec.BoolAttribute(netsec.CKA_SENSITIVE, true),
    netsec.BoolAttribute(netsec.CKA_EXTRACTABLE, false),
}

// 创建或接受一个完整属性快照。
if err := netsec.ValidateAttributes(attrs); err != nil {
    // errors.Is(err, netsec.ErrWrapDecryptConflict) 等
}

// 增量更新只比较 changes 中出现的属性，其余属性保持旧值。
if err := netsec.ValidateUpdate(oldAttrs, changes); err != nil {
    // errors.Is(err, netsec.ErrSensitiveIrreversible) 等
}

// newAttrs 是完整替换快照；缺失的安全属性按 false 处理。
if err := netsec.ValidateTransition(oldAttrs, newAttrs); err != nil {
    // ...
}

// 被封装密钥带 CKA_WRAP_WITH_TRUSTED=true 时，封装密钥必须有
// CKA_TRUSTED=true。参数顺序按 wrappingKey, wrappedKey。
if err := netsec.ValidateWrap(wrappingKeyAttrs, wrappedKeyAttrs); err != nil {
    // errors.Is(err, netsec.ErrUntrustedWrappingKey)
}
```

传入的属性值支持 `bool`、值为 0/1 的整数类型以及单字节 `[]byte`。
重复出现且值冲突的同一属性会被拒绝。所有校验错误都包装了可比较的
哨兵错误，调用方可用 `errors.Is` 做稳定判断。
