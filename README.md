# ttlcache

基于显式单调时间的 Go TTL 缓存，支持持久化/恢复与代际（generation）隔离的到期事件重放。

## 设计要点

- **显式单调时间**：缓存内部不直接调用 `time.Now`，一切时间判断经由 `Clock` 接口（`ttlcache/clock.go`）。`SystemClock` 只取单调分量，`ManualClock` 供测试精确推进。
- **持久化**（`ttlcache/persist.go`）：`Snapshot` 为每个未过期键记录 `Remaining`（保存时刻的剩余寿命）与 `SavedAt`（保存时刻），不存绝对到期时间。
- **恢复**：`Restore(clock, records, downtime)` 对每条记录扣减停机时长，`Remaining - downtime <= 0` 的键立即过期不写入；为正的键以剩余寿命续命，**绝不重新给满 TTL**。恢复出的键分配全新代。
- **代际隔离**（`ttlcache/cache.go`）：每次 `Set` 开启新一代，到期事件携带 `(key, generation, expiresAt)` 三元组。`ExpireDue` 重放事件时仅当三元组与当前代完全匹配才删除，旧代事件作为陈旧事件丢弃——更新键后，旧到期事件不会误删新值。

## 验证

```sh
go test ./... -v
```

核心测试 `TestEventReplay_UpdateDoesNotDeleteNewValue` 以事件重放验证：旧代事件重放结果为 `Applied=false`，新值存活至自身到期时刻。
