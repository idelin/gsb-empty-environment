# CBOR 标签嵌套放大限制

攻击者用多层tag、不定长字符串分块和虚高数组长度，让网关的CBOR解码放大。给Go网关写供请求解码调用的受限读取器：tag嵌套、tag 24内嵌CBOR递归、不定长字符串分块总数、声明长度相对剩余字节的比例都有调用方上限，重复map键拒绝，bignum字节数有上限。用tag链、24号递归、虚高数组和正常载荷验证。

## 用法

```go
limits := netsec.Limits{
	MaxTagDepth:               16,
	MaxTag24Depth:             4,
	MaxIndefiniteStringChunks: 64,
	MaxDeclaredLengthRatio:    1.0,
	MaxBigNumBytes:            256,
	MaxDepth:                  64,
	MaxInputBytes:             4 << 20,
}

value, err := netsec.Decode(payload, limits)
if err != nil {
	// 统一用 errors.Is(err, netsec.ErrLimitExceeded) 判断预算越界；
	// 也可匹配 ErrTagDepthExceeded、ErrTag24DepthExceeded、
	// ErrIndefiniteStringChunksExceeded、ErrDeclaredLengthExceeded、
	// ErrBigNumTooLarge、ErrDuplicateMapKey 等具体哨兵。
}
```

`Decode` 和 `NewReader(...).Decode()` 要求输入恰好是一个 CBOR 数据项并拒绝尾随字节。`NewReader(...).ReadValue()` 可用于流式读取。`DecodeReader` 额外用 `MaxInputBytes` 限制从 `io.Reader` 读入的总字节数。

解码结果为常见 Go 值：无符号整数为 `uint64`，负整数为 `int64`（超出范围时用 `*big.Int`），字节串为 `[]byte`，文本串为 `string`，数组为 `[]any`，map 为 `map[any]any`。tag 2/3 返回 `*big.Int`，tag 24 返回包含已递归解码内容的 `Tag`，其他 tag 返回 `Tag`。map 中 Go 不可比较的键会归一化为 `ByteStringKey`、`CompositeKey` 或 `BigIntKey`。

所有字段为零值时使用保守默认值。关键默认值：tag 链 16 层、tag 24 递归 8 层、分块总数 256、声明长度不超过剩余字节的 1 倍、bignum 1024 字节、总嵌套 128 层。

## 已验证的攻击面

- 多层 `tag(tag(...))` 超过 `MaxTagDepth` 时拒绝。
- 嵌套 tag 24 超过 `MaxTag24Depth` 时拒绝；每层内嵌项必须恰好是一个 CBOR 数据项。
- 声明超大数组/映射长度或字符串长度时，在分配前按剩余字节比例拒绝。
- 不定长字节串/文本串的 chunk 总数在所有字符串和 tag 24 内嵌项之间共享预算。
- 重复 map 键（包括 `1` 与 `0x18 0x01`、定长与不定长同值文本串等不同编码）拒绝。
- tag 2/3 bignum 的字节串总长度超过 `MaxBigNumBytes` 时拒绝。
