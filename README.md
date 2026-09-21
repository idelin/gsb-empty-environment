# endconv

按声明式 schema 在大端 / 小端二进制记录之间转换的 Go 库（仅标准库）。

## 语义

- 多字节整数与 IEEE-754 浮点（`F32`/`F64`）一律按**原始字节反转**，
  不经过浮点运算，因此 NaN 的 quiet/signaling 位与 payload 位逐位保留。
- `VarBlob`（变长字段）= 16 位长度头（按源端序解释）+ 不透明载荷；
  **只反转长度头，载荷逐字节复制**。
- 字节反转是对合（involution），同一操作双向通用；方向参数仅影响
  `VarBlob` 长度头的解释。双重转换恢复全部字节。
- `Convert(dst, src, ...)`：同一切片（原地）与完全不重叠的缓冲区均支持；
  部分重叠在写入前检测并返回 `ErrOverlap`。
- `Parser` 接受任意分块输入，`Next` 区分 `NeedMoreData`（缺数据）与错误
  （坏数据，如长度头超出额度）；长度额度在任何按长度分配之前检查。

## 快速使用

```go
schema := endconv.Schema{
    {Kind: endconv.U16},
    {Kind: endconv.F64},
    {Kind: endconv.VarBlob},
    {Kind: endconv.U32},
}

// 原地
endconv.ConvertInPlace(buf, schema, endconv.BigToLittle)

// 分块流
p, _ := endconv.NewParser(schema, endconv.BigToLittle, endconv.Limits{
    MaxRecordBytes: 65536,
    MaxPayloadBytes: 16384,
})
p.Write(chunk)
rec, status, err := p.Next() // endconv.RecordReady | endconv.NeedMoreData
```

## 测试与样例

    go test ./...
    go run ./cmd/demo
