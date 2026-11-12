# IPNS记录序列回滚与字段一致

IPNS 记录里的值、有效期和序号既出现在 protobuf 字段里，又出现在被 V2 签名覆盖的 CBOR 数据里，攻击者可重放旧记录回滚指向，或让两处字段不一致来蒙混。给Go节点写供 DHT 记录存储调用的 IPNS 校验包，验签由调用方注入：只接受 V2 签名，V1 签名拒绝；签名覆盖的 CBOR 数据里的 Value、Validity、ValidityType、Sequence、TTL 必须与 protobuf 里的对应字段完全一致；ValidityType 只能为 EOL，EOL 是 RFC 3339 时间且须晚于当前时间；替换已有记录时序号必须更大，序号相同则 EOL 更晚者胜出，EOL 也相同则丢弃新记录。用旧序号重放、字段不一致、V1 签名、EOL 已过和同序号决胜验证。

## 用法

```go
rec, err := netsec.Validate(raw, netsec.VerifyOptions{
    Verifier: func(pubKey, message, signature []byte) error {
        // message 已按规范加上 "ipns-signature:" 前缀。
        // 调用方在此完成 V2 验签，并应确认 pubKey 属于预期的 IPNS 名称。
        return verify(pubKey, message, signature)
    },
    Now: time.Now(),
})
if err != nil {
    // 丢弃记录：V1-only、字段不一致、非 EOL、已过期或验签失败等。
    return
}

replace, err := netsec.ShouldReplace(existing, rec)
if err != nil {
    return
}
if replace {
    store(rec)
}
```

`Record.Signature` 是已废弃的 V1 签名字段；包不会用它验签。只有 V1
签名而没有 `SignatureV2` 的记录会被拒绝，带 V2 签名的记录即使同时保留了
兼容性的 V1 字段也仍按 V2 验证。
