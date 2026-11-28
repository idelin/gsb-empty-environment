# cowfs — CoW 快照 Deadlist 计费 + ARC 协同引擎

`cowfs` 是一个可导入的 Go 存储层，实现 ZFS 风格的写时复制快照、birth-time
块归属、deadlist 延迟释放，以及与物理块释放联动的四链表 ARC。

## 组件

- `blockstore.go`：物理块分配器。每次 `Alloc` 都生成带 `BirthTxg` 的不可变块，
  释放的物理号进入空闲列表可被重用；`Free` 通过 `InvalidateHook` 同步通知 ARC。
- `engine.go`：CoW 主引擎。`Write`/`WriteBlock` 分配新块并打上当前 `txg`；
  被替换或删除的旧块若早于最新快照则进入活跃 `deadlist`，否则立即回收。
  `Snapshot` 固化根指针并继承活跃 deadlist，随后开启新 `txg`。
- `accounting.go`：基于「可见树引用 + deadlist 保留边界」的块持有者模型，
  按需重算每个快照的 `referenced` 与 `unique_used`，并提供 `Validate()` 从第一性
  原理复核所有不变量（含零物理块泄漏）。
- `arc.go`：四链表 ARC（`T1/T2/B1/B2`），实现论文 Case 1–4 与 `Replace(x,p)`，
  硬约束 `len(T1)+len(B1) <= c` 与四表总占用 `<= 2c`，目标 `p` 按
  `delta1=max(1,B2/B1)` 增大、`delta2=max(1,B1/B2)` 减小自适应。
- `lru.go`：同容量标准 LRU 基线，用于顺序扫描抗污染对比。

## 关键不变量

- 删除快照实际释放的字节数严格等于删前报告的 `SnapshotInfo.UniqueUsed`
  （`DeleteSnapshot` 内部断言，违例直接 panic）。
- 任意顺序删除首/中/尾快照：deadlist 合并到相邻边界，仍被更老快照观察的块保留，
  失去最后持有者的块立即物理释放。
- 每个存活物理块要么可达、要么恰好位于一个 deadlist 上；全部快照与文件删除后
  `AllocatedBytes` 与 `LiveBlocks` 归零，零泄漏。
- 物理块 `Free` 同步清除 ARC 常驻与幽灵条目，物理号重用不会读出陈旧缓存。

## 快速上手

```go
engine := cowfs.NewEngine(cowfs.WithARCCapacity(16))
_ = engine.Write("ledger", []byte("v1"))
_, _ = engine.Snapshot("snap")
_ = engine.Write("ledger", []byte("v2"))
info := engine.ListSnapshots()[0]
freed, _ := engine.DeleteSnapshot("snap")
// freed == info.UniqueUsed，且 engine.Validate() 永远通过
```
