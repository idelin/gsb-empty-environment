# IPNS记录序列回滚与字段一致

IPNS 记录里的值、有效期和序号既出现在 protobuf 字段里，又出现在被 V2 签名覆盖的 CBOR 数据里，攻击者可重放旧记录回滚指向，或让两处字段不一致来蒙混。给Go节点写供 DHT 记录存储调用的 IPNS 校验包，验签由调用方注入：只接受 V2 签名，V1 签名拒绝；签名覆盖的 CBOR 数据里的 Value、Validity、ValidityType、Sequence、TTL 必须与 protobuf 里的对应字段完全一致；ValidityType 只能为 EOL，EOL 是 RFC 3339 时间且须晚于当前时间；替换已有记录时序号必须更大，序号相同则 EOL 更晚者胜出，EOL 也相同则丢弃新记录。用旧序号重放、字段不一致、V1 签名、EOL 已过和同序号决胜验证。

## 包结构

- `record.go`：`Validator.Validate` 入口与全部接收规则，`Verifier` 由调用方注入。
- `pbwire.go`：IPNS protobuf 记录的最小 wire-format 解码器（拒绝重复字段与截断输入）。
- `cbor.go`：V2 签名覆盖的 CBOR payload 严格解码器（固定五个键、规范键序）。
- `store.go`：`Store` 与 `Compare`，实现序号/EOL 替换决胜规则。

## 用法

```go
validator := netsec.NewValidator(func(data, sig []byte) error {
    // 调用方用记录属主公钥验签，data 已含 V2 域分离前缀
    if !ed25519.Verify(pub, data, sig) {
        return errors.New("bad signature")
    }
    return nil
})
store := netsec.NewStore(validator)

rec, err := store.Put(key, rawDHTRecord)
```

`Put` 先校验再按 `Compare` 决胜：序号大者胜；序号相同 EOL 晚者胜；两者皆同则返回 `ErrEqualRecord` 并丢弃新记录。
