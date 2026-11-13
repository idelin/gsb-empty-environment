# 折叠头行拒绝

用空格或制表符折行时，折叠前后会被拼成一个头或两个头。观测到的折行直接拒绝，不把续行拼进上一个头，也不当成新头。单行头的值内部空格保留。协议网关调用可导入的Go解码器做这次裁决。在值中间插入CRLF加空格。

## 解码器

`netsec` 包提供可导入的头块解码器，入口为 `DecodeHeaders([]byte)` 与
`DecodeHeaderString(string)`，返回 `[]Header` 或错误。

裁决规则：

- 任何以 SP/HTAB 开头的物理行都视为 obs-fold 折行，整个块以
  `ErrLineFolding` 拒绝；续行既不拼进上一个头，也不作为新头返回。
- 普通 `Name: value` 行只剥离值首尾的 SP/HTAB（OWS），值内部空格与
  制表符逐字节保留。
- 行分隔兼容 CRLF 与裸 LF；空行结束头块，其后内容忽略。
- 无法解析为单个头字段的行返回 `ErrMalformedHeader`。
- 错误可经 `errors.Is` 匹配哨兵值，`errors.As` 可取带物理行号的
  `*FieldError`。

示例：

```go
headers, err := netsec.DecodeHeaderString("X-Long: part1\r\n part2\r\n")
// err 包装 netsec.ErrLineFolding，headers 为 nil
```
