# IPNS记录序列回滚与字段一致

IPNS 记录里的值、有效期和序号既出现在 protobuf 字段里，又出现在被 V2 签名覆盖的 CBOR 数据里，攻击者可重放旧记录回滚指向，或让两处字段不一致来蒙混。给Go节点写供 DHT 记录存储调用的 IPNS 校验包，验签由调用方注入：只接受 V2 签名，V1 签名拒绝；签名覆盖的 CBOR 数据里的 Value、Validity、ValidityType、Sequence、TTL 必须与 protobuf 里的对应字段完全一致；ValidityType 只能为 EOL，EOL 是 RFC 3339 时间且须晚于当前时间；替换已有记录时序号必须更大，序号相同则 EOL 更晚者胜出，EOL 也相同则丢弃新记录。用旧序号重放、字段不一致、V1 签名、EOL 已过和同序号决胜验证。

## 实现

纯标准库实现（离线环境无外部模块）：自带最小 protobuf wire 解析器与严格
CBOR 解码器，密码学验签通过接口注入，包本身不依赖任何密钥体系。

- `wire.go` — `ipns.IpnsEntry` protobuf 解析，重复字段号直接拒绝
- `cbor.go` — 签名覆盖的 CBOR map 解码，拒绝 indefinite-length、tag、
  float/simple、重复键和缺键
- `validator.go` — 校验流水线与 `V2Verifier` 注入接口
- `store.go` — 线程安全的内存 DHT 记录存储、替换决胜与 `Select`
- `errors.go` — 可通过 `errors.Is` 分类的哨兵错误

## 校验流水线

`Validator.Validate(key, raw)` 按顺序执行：

1. 解析 protobuf，字段缺失、截断、重复字段号、未知 wire type 一律拒绝；
2. 存在 V1 签名（field 2）即拒绝；无 `signatureV2`（field 8）即拒绝；
3. 用 `V2SignaturePrefix + data` 构造被签名节，交给注入的 `V2Verifier`；
4. 解码 CBOR，要求 `Value`、`Validity`、`ValidityType`、`Sequence`、
   `TTL` 五个键恰好出现一次，并与 protobuf 对应字段逐字节/逐值相等；
5. `ValidityType` 必须为 EOL(0)，`Validity` 必须能按 RFC 3339 解析；
6. EOL 必须严格晚于当前时间（相等也算过期）。

## 替换决胜

`Store.Put` 与 `ShouldReplace` 实现：

- 新记录 Sequence 更大 → 替换；
- Sequence 相同 → EOL 更晚者替换；
- Sequence 与 EOL 都相同 → 丢弃新记录，返回 `ErrRecordSuperseded`，
  旧记录保持不变；
- 任何校验失败都不会触碰已存储记录。

## 调用方接入

```go
validator := netsec.NewValidator(netsec.Config{
    Verify: func(key string, signed, signature []byte) error {
        // 按 key 解析出公钥（嵌入公钥或 peer ID），
        // 用节点自身的 libp2p 签名库验签 signed/signature。
        return pubKey.Verify(signed, signature)
    },
    // Now 可省略，默认 time.Now；测试时注入固定时钟。
})

store := netsec.NewStore(validator)
record, err := store.Put(dhtKey, rawRecord)
```

## 测试

`go test ./... -race` 覆盖：旧序号重放、五字段 CBOR/protobuf 不一致、
V1 与双签名拒绝、错误 V2 签名、EOL 过期/非 RFC 3339、非 EOL 类型、
同序号 EOL 决胜与完全平局丢弃、CBOR 严格性（重复键、缺键、tag、
indefinite-length、text/byte 两种 Validity 编码）及畸形 protobuf。
