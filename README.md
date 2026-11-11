# Protobuf字段影子值

网关对消息先验证再透传时，重复单值字段可能在后端被取成另一值。写供Go网关调用的线格式准入包，调用方给字段号、类型与重复性描述；单值重复拒绝，packed与非packed重复字段按到达顺序合并，未知字段保留原始区间但不得解释为已授权字段。验证长度、varint溢出和嵌套预算后才转发原报文。用同字段不同wire type、截断和跨块输入测试。

## wireadmit

`wireadmit` 是无生成代码的 protobuf 线格式准入包，位于 `wireadmit/`。
网关在转发原始字节前调用它做结构准入，输入切片不会被复制或修改。

### 准入语义

- **单值影子值**：`Singular` 字段出现第二次即返回 `ErrDuplicateSingular`，
  即使两次取值相同也拒绝，后端无法选取与网关验证时不同的那一份。
- **重复字段合并**：`Repeated` 标量同时接受 unpacked 与 packed 编码（含混发），
  `Report.Fields` 按到达顺序记录每次出现，`Report.Values(field)` 把 packed
  展开后的元素与 unpacked 元素按线上顺序拼接。
- **未知字段隔离**：无 `Rule` 的字段号只按 wire 规则跳过并记录原始字节区间
  `Report.Unknown`，绝不按已授权类型解释；未知 length-delimited 负载即使内部
  出现授权字段号也保持不透明。
- **长度与 varint**：所有定长字段和 length-delimited 长度做边界检查；varint
  限制在 64 位（最多 10 字节，第 10 字节至多 1 个有效位），超长返回
  `ErrVarintOverflow`，声明长度越界返回 `ErrLengthOverflow`。
- **嵌套预算**：`Message` 与 `Group` 每层消耗预算，`WithNestingBudget` 控制
  最大深度，超限返回 `ErrNesting`；未知 group 同样计数。
- **wire type 冲突**：已知字段使用与声明类型不兼容的 wire type 时返回
  `ErrWireType`；单值/不可打包字段误用 wire type 2 返回 `ErrPacked`。

### 调用方式

```go
schema, err := wireadmit.NewSchema([]wireadmit.Rule{
    {Number: 1, Type: wireadmit.Varint, Cardinality: wireadmit.Singular},
    {Number: 2, Type: wireadmit.Varint, Cardinality: wireadmit.Repeated},
    {Number: 3, Type: wireadmit.Bytes, Cardinality: wireadmit.Singular},
})
if err != nil { /* schema 本身非法 */ }

// 一次性准入：成功后 msg 可原样转发。
report, err := wireadmit.Admit(schema, msg,
    wireadmit.WithNestingBudget(64),
    wireadmit.WithMaxMessageSize(1<<20),
)

// 跨块输入：只缓存不解释，收齐后统一准入。
sv := wireadmit.NewStreamValidator(schema)
for chunk := range chunks {
    if _, err := sv.Write(chunk); err != nil { /* 超限 */ }
}
if _, err := sv.Admit(); err != nil { /* 拒绝 */ }
forward(sv.Bytes())
```

错误通过 `*wireadmit.Error` 返回（含 `Code`、字节偏移 `Offset`、字段号
`Field`），并支持 `errors.Is` 匹配包级哨兵（如 `wireadmit.ErrDuplicateField`）。

### 测试

- `go test ./...`：覆盖同字段不同 wire type、单值重复、packed/unpacked 混发、
  截断、长度越界、varint 溢出、嵌套预算、group、未知字段区间和输入不变性。
- `go test ./wireadmit -fuzz=FuzzParseNeverPanics -fuzztime=30s`：随机字节
  下验证不 panic、区间不越界，并要求一次性与流式路径结论一致。
