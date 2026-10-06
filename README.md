# wquantile

Go 加权分位数统计库：整数值 + 非负整数权重，支持在线增减与多分区屏障一致性合并。

## 单机多重集 `Multiset`

- `Add(v, w)` / `Remove(v, w)`：在线增减指定样本；权重为零自动清除节点，零权重增删为 no-op。
- `Quantile(q)`：累计权重首次 `>= q * 总权重` 的最小值；`q` 截断到 `[0,1]`，`q=0` 取最小正权重值，`q=1` 取最大值。
- 空集合返回 `ErrEmpty`；移除超过现存权重返回 `ErrInsufficientWeight`。
- 实现：按值排序的 treap，子树维护权重和，增删查均为 O(log n)。

## 多分区协调器 `Coordinator`

- 各分区通过 `PublishDelta`（带序号的增减差量，`Weight>0` 增、`<0` 减）与 `PublishBarrier` 上报；序号每分区独立、乱序缓存、重复去重。
- 屏障切面：某屏障 ID 在**所有**已注册分区都到齐后才提交，差量按序原子生效；查询永不混算新旧分区数据。
- `Quantile(q)` 查询最近完整切面；`QuantileAt(cut, q)` 对未完成的切面返回 `ErrIncompleteCut`（缺分区时不会返回混算结果）。
- `Disconnect` / `Reconnect`：断连分区保持注册，切面停滞直到重连续传；断连期间发布返回 `ErrPartitionDown`。
- `Snapshot` / `RestoreCoordinator`：快照保留已提交集合、每分区去重状态（已消费序号 + 乱序缓存）与未提交的暂存差量（撤销语义），恢复后重发旧序号会被忽略。

## 测试

```
go test ./...
```

- `multiset_test.go`：小权重展开为原始样本序列，核对端点（q=0/1）、重复值、零权重与随机增删。
- `coordinator_test.go`：多分区乱序 + 重复投递，屏障切面与汇总原始样本模型逐轮核对；缺分区返回未完成；快照恢复后续传去重与撤销。
