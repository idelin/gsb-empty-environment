# 动态表更新位置

HPACK动态表大小更新放在头中间，或把表扩到巨大，会打乱后续索引。大小更新只能出现在头块最前，可以连续多条，但不得大于SETTINGS允许的上限；越界索引失败。更新在最前且变小后，后续字面头仍可解码。协议网关调用可导入的Go解码器做这次裁决。把更新放在第一个头之后，并试一次超上限。

## 用法

```go
decoder := netsec.NewDecoder(4096) // SETTINGS_HEADER_TABLE_SIZE
fields, err := decoder.Decode(headerBlock)
if err != nil {
	// errors.Is(err, netsec.ErrSizeUpdateNotAtStart)
	// errors.Is(err, netsec.ErrSizeUpdateTooLarge)
	// errors.Is(err, netsec.ErrIndexOutOfRange)
}
```

`Decoder` 在头块之间保留动态表。表大小更新位于首个头字段之前时，可以连续出现多条，但每条都必须不超过 `SETTINGS_HEADER_TABLE_SIZE`；出现在首个头字段之后的更新返回 `ErrSizeUpdateNotAtStart`。缩表会立即淘汰超限条目，之后的字面头仍可继续解码。

`Decode`、`DecodeHeaderBlock` 和 `DecodePairs` 提供无状态的一次性调用入口。
