# 压缩比上限

很小的压缩包声明解压后有巨大尺寸。解压输出不得超过声明尺寸和绝对上限，实际比例超过调用方上限则失败。比例正常的小文件可以解出。归档读取调用可导入的Go解码器做这次裁决。构造声明尺寸超过上限的条目。

## 安全读取器

`NewReader` / `OpenReader` 会在调用任何解压器前检查 ZIP 中央目录中的声明尺寸与压缩比。`File.Open`、`File.ReadAll` 和 `File.Extract` 在执行过程中再次限制实际输出，超出声明尺寸、条目绝对上限或调用方压缩比时返回错误，而不是静默截断。

```go
reader, err := netsec.OpenReader("upload.zip", netsec.Limits{
	MaxEntrySize:        16 << 20,
	MaxTotalSize:        64 << 20,
	MaxCompressionRatio: 100,
})
if err != nil {
	return err
}
defer reader.Close()

data, err := reader.ReadFile("document.txt")
```

零值 `Limits` 使用安全默认值。可通过 `NewReaderWithDecompressors` 或 `Reader.RegisterDecompressor` 为额外 ZIP 方法提供解码器；它们同样只会在元数据检查通过后被调用。
