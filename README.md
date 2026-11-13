# CESU8代理项

CESU-8把一对代理项编成六字节，解码后变成增补字符从而绕过过滤。单独的UTF-8代理项字节失败；不把相邻代理项拼成增补字符。真正的四字节增补字符可以解码。字节解码调用可导入的Go解码器做这次裁决。输入一对按CESU-8编码的代理项。

本包用 Go 的 `unicode/utf8` 解码器作严格裁决：

- `Valid` / `Validate` 拒绝 CESU-8 代理项序列，也拒绝单独的代理项字节。
- `Decode` / `DecodeRune` 不把相邻代理项拼成增补字符。
- 标准 UTF-8 的四字节增补字符可以正常解码。
- `ContainsCESU8` / `IsCESU8Pair` 可在过滤层识别 CESU-8 代理项。

示例（U+1F600 的 CESU-8 六字节输入）：

```sh
go run ./cmd/cesu8check -hex 'ed a0 bd ed b8 80'
```

输出中 `valid: false`、`cesu8_pair: true`，且解码结果不会出现 `U+1F600`。
