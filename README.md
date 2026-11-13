# ZIP64额外字段

ZIP64额外字段的未压缩尺寸和本地头不一致。需要ZIP64时必须以额外字段为准且与中央目录一致；不一致失败。一致的ZIP64条目可以读出全部字节。归档读取调用可导入的Go解码器做这次裁决。改动额外字段里的尺寸。

## 行为

- 本地头的 32 位尺寸仅作提示；存在 ZIP64 扩展字段（tag `0x0001`）时，以其中的 64 位未压缩尺寸和压缩尺寸为准。
- ZIP64 尺寸必须与中央目录的最终尺寸一致；缺少必需的扩展字段或两侧不一致时返回 `ErrFormat`。
- 一致的 ZIP64 条目可完整读取，读取过程中同时校验未压缩长度和 CRC-32。
- 普通 ZIP 按 32 位本地头与中央目录进行一致性校验。

## API

```go
zr, err := netsec.NewReader(r, size)   // io.ReaderAt
zr, err := netsec.OpenReader(name)     // file on disk
entries, err := netsec.DecodeBytes(data)
err := netsec.ValidateBytes(data)
```

`File.Open`、`Reader.File` 字段、`Reader.Files` 和 `Reader.Lookup` 与 `archive/zip` 的读取方式兼容。提取前即可通过 `Validate*` 或 `NewReader` 得到裁决结果。
