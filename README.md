# rehash — 渐进扩容哈希表

Go 泛型哈希表，扩容/缩容时**增量迁移**：每次操作最多迁移固定数量的桶，
扩容期间查询、删除、快照遍历全部可用。

## 设计

- **双表结构**：迁移期间同时持有 `old` / `cur` 两张表；每次操作（含 `Get`）
  先迁移至多 `chunk` 个旧桶（`WithMigrateChunk`，默认 1），再执行本体逻辑。
- **唯一可见值**：任意时刻一个键只存在于一张表中。`Put` 在键所在的表中
  原地更新，新键只插入最新表——重复更新永远只有一个可见值。
- **墓碑安全**：`Delete` 同时从两张表删除，迁移是"移动"而非"复制"，
  旧值不会被迁移或查找复活。
- **稳定快照**：`Snapshot()` 在创建时物化全部条目并记录逻辑版本号，
  之后的迁移与增删改都不影响它；迭代不漏不重。
- **版本号**：每次 `Put` 与成功的 `Delete` 使版本 +1，快照携带捕获时的版本。

## 指标

`Stats()` 报告迁移进度与最坏单步工作量（工作单元 = 桶访问 + 条目搬移 + 链探测）：

```go
st := tab.Stats()
// st.MigrateNext / st.OldBuckets  迁移进度
// st.MigratedEntries              累计搬移条目数
// st.LastOpWork / st.WorstOpWork  最近 / 历史最坏单步工作量
```

`Progress()` 返回 `(已迁移桶数, 旧表总桶数)`。

## 用法

```go
tab := rehash.New[string, int](rehash.StringHash, rehash.WithMigrateChunk(2))
tab.Put("a", 1)
v, ok := tab.Get("a")
tab.Delete("a")

snap := tab.Snapshot() // 稳定版本，可在迁移进行中创建
for k, v, ok := snap.Next(); ok; k, v, ok = snap.Next() { ... }
```

自定义 `HashFunc` 可注入受控哈希碰撞（测试中用常量哈希把所有键压进
同一个桶，验证长链迁移的正确性与最坏单步工作量）。

## 测试

- `table_test.go`：基本操作、迁移触发/完成/进度单调、唯一可见值、
  删除不复活、快照稳定性、扩缩容、版本计数。
- `model_test.go`：随机操作序列对照"映射 + 版本号"参考模型，
  每步校验 Get / Snapshot / Version / Len 与迁移不变量。
- `collision_test.go`：受控碰撞（常量哈希、模 4 哈希）下的正确性，
  以及最坏单步工作量在好哈希下有界、在全碰撞下如实放大。

运行：`go test ./...`（非并发安全，多 goroutine 使用需自行加锁）。
