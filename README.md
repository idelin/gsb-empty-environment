# 增量 CSV 读取包

`taskpkg` 面向 Go 采集管线，直接从 `io.Reader` 增量读取记录，不会把引号内的
换行误判为新记录。解析状态跨 `Read` 调用保存，因此任意字节位置切块都不会改变结果。

## 解析语义

- `Delimiter`、`Quote` 各为一个字节，默认 `,` 和 `"`。
- 引号只在该字段首字节处开启；引号外的引号按普通字节处理。
- 仅引号内的 `""` 表示一个解码后的 `"`。
- 引号外以 LF 或 CRLF 结束记录；引号内的 LF/CRLF 原样保留在字段值中。单独 CR
  不是行结束符。
- 字段 `Raw` 使用绝对字节区间 `[Start, End)`，包含外层引号，不包含分隔符和行结束符。
- 记录 `LineSpan` 使用从 1 开始的闭区间物理行号；引号内换行会增加行号。

## 使用

```go
reader := taskpkg.NewReader(input, taskpkg.Options{
    Delimiter:     ',',
    Quote:         '"',
    MaxFieldBytes: 1 << 20,
})

for {
    record, err := reader.Next()
    if errors.Is(err, io.EOF) {
        break
    }
    if err != nil {
        // 该错误记录已经被完整消费，且没有返回任何部分字段。
        // 下一轮 Next 会从下一条合法记录边界继续。
        continue
    }
    for _, field := range record.Fields {
        consume(field.Raw, field.Value)
    }
}
```

`MaxFieldBytes` 限制字段原始字节数（含外层引号），`<= 0` 表示不限制。字段超限后
记录进入丢弃状态，但仍持续识别引号转义和结束位置；记录到达边界时返回
`ErrFieldTooLong`。语法错误返回 `ErrMalformedRecord`，EOF 处未闭合的引号返回
`ErrTruncatedQuote`。错误记录通过 `*RecordError` 提供索引、原始区间和行跨度，
但不提供字段切片。

便捷入口 `ParseAll`、`ParseBytes` 会读到第一个错误为止；需要错误后继续恢复时使用
`Reader.Next`。

## 验证

`csvreader_test.go` 使用独立的整段输入解析器作为基准，对每个 fixture 的每一个
切块位置逐一比较，包括跨块 `""`、CRLF 的 CR/LF 之间切块、裸 CR、字段内 CRLF、
截断引号和超长字段后的恢复。
