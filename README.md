# repartition

采集端在切换数据源分区时会同时收到新旧拓扑的数据。写一个可嵌入Go消费者的重分区屏障组件，旧分区各自到达切换偏移后才能原子启用新拓扑。将屏障与下游提交回执持久化，允许重复通知和任意一次写盘后重启；通过故障注入验证跨分区记录既不提前可见也不重复发布。

## 设计

`repartition.Barrier` 是一个可直接嵌入消费者的进程内组件，全部状态写入数据
目录下的单文件预写日志 `repartition.wal`，每次状态变更都先 `fsync` 再更新内存。

- **屏障语义**：每个旧分区在配置的切换偏移处调用 `NotifyCutover`；只有全部
  旧分区都到达后，新拓扑才原子翻转（`switched` 状态由 cutover 集合派生，
  即使翻转帧落盘前崩溃，恢复后也保持一致）。
- **可见性**：旧拓扑记录在翻转前持续可见；新拓扑记录只持久化缓存，翻转前
  `Next/TryNext/Run` 永远取不到，保证不提前可见。翻转后迟到的旧记录被
  `ErrFenced` 隔离。
- **持久化事件**：`config / record / cutover / switched / delivered /
  committed`。帧格式含 CRC32 与整帧 SHA-256，重启时半写或损坏的尾部自动
  截断到最后一个完整帧，支持任意一次写盘后的重启。
- **不重复发布**：下游发布采用事务发件箱协议——先落盘 `delivered` 投递意图，
  再调用 `Sink.Publish`，成功后落盘 `committed` 回执。若崩溃发生在发布与回执
  之间，重启会重新调用 `Publish`（至少一次），`Sink` 以记录 ID 幂等去重，
  因此每条记录只生效一次。
- **幂等输入**：重复的记录、切换通知、投递意图、回执都不会产生重复状态；
  重开屏障时配置不一致返回 `ErrConfigMismatch`。

## 用法

```go
cfg := repartition.Config{
    OldPartitions: []int{0, 1},
    NewPartitions: []int{10, 11},
    CutOffsets:    map[int]int64{0: 2, 1: 1},
}
b, err := repartition.Open("/var/lib/consumer/barrier", cfg)
if err != nil { return err }
defer b.Close()

// 采集循环：新旧拓扑记录都可随时摄入，重复摄入安全。
_ = b.Ingest(repartition.Record{Partition: 0, Offset: 0})

// 旧分区到达切换偏移时通知；最后一个通知触发原子翻转。
_ = b.NotifyCutover(0, 2)
_ = b.NotifyCutover(1, 1)

// 阻塞式发布循环；Sink 必须按记录 ID 幂等。
sink := repartition.SinkFunc(func(ctx context.Context, r repartition.Record) error {
    return downstream.Apply(r.Key, r.Payload) // 内部做幂等
})
return b.Run(ctx, sink)
```

也可以手动驱动：`TryNext`/`Next` 取记录，`MarkDelivered` 落投递意图，
`Publish` 后 `Ack` 落回执。

## 故障注入与验证

`WithFault(FaultConfig{AtAppend, BeforeSync, Err})` 在第 `AtAppend` 次逻辑
追加处注入一次崩溃：`BeforeSync=true` 写半帧且不 fsync（断电半写），
`false` 完整 fsync 后再失败（提交后崩溃）。故障后屏障中毒，调用方重新
`Open` 即模拟重启。

- `TestFaultInjectionSweep` 对每个 WAL 位置、两种故障模式逐一注入，断言
  恢复后全部记录恰好生效一次、新拓扑记录不早于任何旧拓扑记录发布。
- `TestRandomMultiCrash` 在一条工作负载上连续注入 30 次随机崩溃。
- `TestTornWALTail`/`TestTornLastFrame` 直接损坏 WAL 文件验证尾部截断。
- `TestNoEarlyVisibility` 验证翻转前新拓扑完全不可见、翻转后旧拓扑被隔离。

运行：

```sh
go test -race ./...
```
