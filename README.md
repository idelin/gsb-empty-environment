# fieldbinding

数据湖接收的更新批次带有独立演进的嵌套字段模式。为Go写入端实现按稳定字段ID绑定的列映射包，允许重命名、移动可空子树及无损数值拓宽；拒绝把缺失值和显式null合并。批次跨版本时先原子校验再发布，测试同名异ID、父节点缺失及最后一行溢出时读者仍看到旧快照。

## 设计要点

- **按 ID 绑定**：字段的唯一绑定键是稳定 ID（`Field.ID`），名称仅用于错误信息。同名异 ID 视为不同字段（`ErrUnknownField`），异名同 ID 是重命名，免费通过。
- **允许的演进**：重命名；可空子树移动（两侧路径上的祖先及被移动根都必须可空，否则 `ErrIllegalMove`）；无损数值拓宽 `int32→int64/float64`、`float32→float64`（`int64→float64` 超过 2^53 会损失精度，拒绝）。
- **三态值模型**：`Missing`（记录中缺失）与 `Null`（显式 null）是不同状态，投影全程保留、绝不合并；必填字段缺失直接拒绝（`ErrMissingValue`），不为缺失值编造 null。
- **原子校验再发布**：`Table.Commit` 在写锁内完成"规划映射 + 逐行校验"，全部通过后才通过 `atomic.Pointer` 交换快照；任一行失败（如最后一行 int32 溢出）整个批次中止，读者始终看到完整的旧快照或新快照。

## 用法

```go
table := fieldbinding.NewTable(tableSchema)
snap, err := table.Commit(&fieldbinding.Batch{Schema: batchSchema, Rows: rows})
if err != nil {
    // 校验失败，已发布快照不受影响
}
cur := table.Snapshot() // 读者侧：原子加载，永不观察到半提交的批次
```

运行测试：`go test -race ./...`
