# CBOR签名唯一编码

设备消息签名需要排除同一对象的不同二进制表达。实现供Go签名服务调用的编码包，仅支持整数、字节串、UTF-8文本、数组和映射；整数及长度用最短编码，映射键按编码长度再按字节排序，拒绝重复编码键和不定长项。解码端在发布对象前验证唯一编码与资源预算。用独立编码器往返、非最短长度及乱序键测试，同一消息不能有第二种被接受的编码。

## canon 包

确定性编码实现在 `canon` 子包（CBOR RFC 8949 的 Deterministic Encoding Rules 子集），
只允许整数、字节串、UTF-8 文本、数组和映射五种值。

- `canon.Encode(v)` 输出唯一规范字节串；`canon.AppendEncode(dst, v)` 可复用缓冲区。
- `canon.Decode(data)` 用默认预算严格解码；`canon.DecodeWithLimits(data, limits)`
  自定义 `Limits`（字节数、条目数、嵌套深度、字符串与容器长度）。
- 映射以 `canon.Map`（有序 `MapEntry` 切片）返回，保留已验证的键顺序；
  编码端同时接受 `canon.Map` 与原生 `map[K]V`，结果与 map 迭代顺序无关。
- 整数与所有长度强制最短头部；映射键先按编码长度、再按字节排序；
  重复编码键、不定长项、标签、浮点、布尔/null 与非 UTF-8 文本一律拒绝。
- 解码在发布对象前完成预算检查，并对结果重新编码做字节级比对，
  保证同一逻辑对象不存在第二种被接受的编码。

```go
raw, err := canon.Encode(canon.NewMap(
    canon.MapEntry{Key: "id", Value: int64(42)},
    canon.MapEntry{Key: "payload", Value: []byte{1, 2, 3}},
))
if err != nil { /* ... */ }

obj, err := canon.DecodeWithLimits(raw, canon.DefaultLimits())
if err != nil { /* 非规范或超预算输入 */ }
_ = obj
```

测试 `go test ./...` 包含手工 KAT、与生产代码零共享的独立编码器往返、
非最短长度、乱序/重复键、不定长项、畸形输入与各维度预算拒绝，
另含 `FuzzDecodeRoundTrip`：任何被接受的输入重编码后必须逐字节等于原输入。
