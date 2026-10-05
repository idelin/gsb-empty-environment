# ghtable — 渐进扩容哈希表

一个 Go 泛型哈希表库：扩容（rehash）被摊薄到每次操作上，迁移期间查询、
删除、快照遍历全部可用，且每次操作迁移的桶数有硬上限。

## 设计

- **双层结构**：主层 `ht0` 与迁移期的新层 `ht1`。读路径先查新层再查旧层，
  新层条目（含墓碑）永远优先，因此**重复更新只有一个可见值**。
- **受限迁移**：每个操作（含 `Get`）最多迁移 `MigrateBudget` 个桶；旧桶中被
  新层遮蔽的条目与墓碑在迁移时直接丢弃，迁移同时完成压缩。
- **墓碑防复活**：迁移中 `Delete` 只在新层写墓碑；当旧桶随后迁移时，对应
  条目被丢弃，**旧值不会复活**。迁移完成后的链式清理 rehash 会回收墓碑。
- **稳定快照**：条目与桶链一旦共享即不可变（写时复制）。`Snapshot` 只复制
  桶切片头并记录**映射版本号**；无论之后如何写入/迁移，迭代该快照都
  **不漏不重**。版本号仅在可见映射变化（Set/Delete）时递增，迁移不改变它。
- **可观测性**：`Stats` 报告迁移进度（`BucketsMigrated`/`Progress`）与
  **最坏单步工作量**（`Metrics.MaxStepBuckets`/`MaxStepEntries`）。

## 用法

```go
tbl := ghtable.New[string, int](ghtable.Config[string]{
    InitialBuckets: 64,
    MigrateBudget:  2, // 每次操作最多迁移 2 个桶
})
tbl.Set("a", 1)
v, ok := tbl.Get("a")
tbl.Delete("a")

snap := tbl.Snapshot()      // 固定当前映射版本
tbl.Set("b", 2)             // 不影响 snap
it := snap.Iterator()
for it.Next() {
    fmt.Println(it.Key(), it.Value())
}

st := tbl.Stats()           // 迁移进度 + 最坏单步工作量
_ = st.Metrics.MaxStepEntries
```

键类型只需 `comparable`；零配置使用随机种子的默认哈希。传入自定义
`Hasher`（如常量或取模哈希）即可在测试中**受控制造哈希碰撞**、精确
决定键落入哪个桶。

## 测试

```sh
go test ./...
```

覆盖：受控碰撞链、迁移中增删查、墓碑防复活、重复更新唯一可见值、
快照在持续写入/迁移下的稳定性（不漏不重）、单步迁移工作量上限、
缩容与墓碑清理、映射版本语义、针对参考模型的 5000 步随机对拍。
