# filtermerge

安全网关按小时合并多个租户的计数布隆过滤器。写Go可合并摘要包，要求相同租户、盐代次和哈希参数才可合并；撤销一个分片时不得因计数饱和出现假阴性。构造刻意碰撞及重复交付，检验快照查询、幂等撤销和内存上限。

## 设计

- **兼容性契约**：`Params{TenantID, SaltGen, NumCounters, NumHashes, CounterBits}` 完全一致才允许 `Merge`，否则返回 `ErrIncompatible`。
- **饱和粘性计数**：计数器在 `CounterBits` 宽度处饱和；`Revoke` 时饱和计数器保持不动（sticky），因此撤销分片只会引入假阳性，绝不产生假阴性。
- **幂等交付与撤销**：按分片 ID 去重，重复 `Merge` 为无操作；`Revoke` 未知或已撤销的 ID 返回 `false`，不产生副作用。
- **内存上限**：`WithMaxBytes` 限制聚合计数器 + 每分片留存副本的总存储，超限合并返回 `ErrMemoryLimit` 且摘要保持不变。
- **快照查询**：`Snapshot()` 拷贝计数器，返回不可变视图；之后的合并/撤销不影响已取快照。`Summary` 可并发使用。

## 用法

```go
p := filtermerge.Params{TenantID: "tenant-a", SaltGen: 7, NumCounters: 1 << 16, NumHashes: 4, CounterBits: 8}
sum, _ := filtermerge.NewSummary(p, filtermerge.WithMaxBytes(64<<20))

shard, _ := filtermerge.NewShard("2026-11-11T09", p)
shard.Add([]byte("203.0.113.7"))
_ = sum.Merge(shard)          // 重复交付同一 ID 安全无操作

snap := sum.Snapshot()
snap.Contains([]byte("203.0.113.7")) // true

sum.Revoke("2026-11-11T09")   // 幂等；饱和计数器不回退
```

测试覆盖刻意碰撞（注入常数哈希）、计数饱和、重复交付、幂等撤销、内存上限与并发：`go test -race ./...`
