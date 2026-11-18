# Kafka 幂等写入纪元校验

`netsec.Validator` 为 Go 分区日志提供幂等生产者校验：按
`(producerID, partition)` 保存 epoch、最后序号，以及最近 5 个批次的序号
区间和日志偏移。

- epoch 小于当前值：`StatusFenced` / `ErrFenced`，不消耗偏移、不修改状态。
- epoch 大于当前值：清空该生产者的序号历史，要求从序号 0 重新开始。
- 序号等于最后序号加 1：追加并分配新的偏移区间。
- 序号落在最近 5 个批次内：返回 `StatusDuplicate` 和原偏移区间。
- 其他序号（跳号、过期且已淘汰）：`StatusOutOfOrder` / `ErrOutOfOrder`。
- 序号在 `math.MaxInt32` 后回绕到 0，批次可以跨过回绕边界。
- 非空 `TransactionalID` 必须已通过 `BindTransactionalID` 绑定到该
  `producerID`，否则拒绝。

```go
v := netsec.NewValidator()
_ = v.BindTransactionalID("orders-tx", 7)

res, err := v.Append(netsec.Batch{
    ProducerID:      7,
    Partition:       0,
    Epoch:           1,
    BaseSequence:    0,
    RecordCount:     2,
    TransactionalID: "orders-tx",
})
// res.Status == netsec.StatusAppended, res.BaseOffset == 0, res.LastOffset == 1

res, err = v.Append(netsec.Batch{
    ProducerID:      7,
    Partition:       0,
    Epoch:           1,
    BaseSequence:    0,
    RecordCount:     2,
    TransactionalID: "orders-tx",
})
// res.Status == netsec.StatusDuplicate, res.BaseOffset == 0
```

`Restore` 用于从日志恢复 producer 的最后序号和分区的下一个偏移。
`Snapshot` 可读取当前缓存的批次窗口。
