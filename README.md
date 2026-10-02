# LSM Tombstone & Lifecycle Cleaner

LSM 树删除语义管理内核（Go，仅标准库）：

- **Tombstone 标记**：`Delete` 不就地移除数据，而是写入一条 `Tombstone=true` 的特殊记录（携带全局单调递增的 `Seq`）。
- **读路径屏蔽**：Point Get 按 memtable → L0 → … → Ln（层内新表优先）查找，命中 tombstone 即返回 `ErrNotFound`；Range Scan 按 key 归并所有版本（`Seq` 大者胜），跳过 tombstone。
- **生命周期清理器（Compaction）**：某层 SSTable 数超过 `LevelCap` 时，将该层所有表归并推入下一层，归并中同 key 只保留最新版本；当合并目标是 **bottom-most 层** 时，tombstone 已不可能遮蔽更老版本，被**物理丢弃**，空间随之释放。

## 结构

- `entry.go` — `Entry`（含 Tombstone/Seq）、`ErrNotFound`
- `memtable.go` — 有序内存写缓冲
- `sstable.go` — SSTable 二进制格式与读写
- `db.go` — `DB`：Put/Delete/Get/Scan/Flush/Compaction
- `db_test.go` — 验收测试
- `cmd/demo/main.go` — 可运行的生命周期演示

## 运行

```sh
go test ./... -v
go run ./cmd/demo
```

## 测试覆盖（验收边界）

- `TestPointGetTombstoneMasking` — 插入 K1=V1 后删除 K1，查询立即返回 NotFound（tombstone 在 memtable / 已落盘两种情形）；删除后可重新 Put 复活。
- `TestRangeScanMasksTombstones` — Range Scan 正确屏蔽已删除 key（flush 前后一致）。
- `TestTombstonePreservedAtIntermediateLevel` — tombstone 在中间层合并时被保留（仍需遮蔽潜在的更老版本）。
- `TestTombstonePhysicallyPurgedAtBottomLevel` — 多轮 Compaction 后 key 沉至最后一层：tombstone 计数归零、所有 `.sst` 文件字节中不再出现 `K1`，且 Get 仍返回 NotFound。
- `TestPersistenceAcrossReopen` — tombstone 是持久化记录，重启后删除语义不丢失。

## 实测结果

```
=== RUN   TestPointGetTombstoneMasking
--- PASS: TestPointGetTombstoneMasking (0.01s)
=== RUN   TestRangeScanMasksTombstones
--- PASS: TestRangeScanMasksTombstones (0.00s)
=== RUN   TestTombstonePreservedAtIntermediateLevel
--- PASS: TestTombstonePreservedAtIntermediateLevel (0.00s)
=== RUN   TestTombstonePhysicallyPurgedAtBottomLevel
--- PASS: TestTombstonePhysicallyPurgedAtBottomLevel (0.02s)
=== RUN   TestPersistenceAcrossReopen
--- PASS: TestPersistenceAcrossReopen (0.00s)
PASS
ok  	lsm	0.493s
```

Demo 输出：

```
== LSM tombstone lifecycle demo (MaxLevels=3, LevelCap=1) ==
1. Put K1=V1, flush -> value lives in L0                   [{tables:1 entries:1 tombstones:0} {tables:0 entries:0 tombstones:0} {tables:0 entries:0 tombstones:0}]
2. Delete K1 (tombstone in memtable); Get(K1) -> NotFound immediately
3. Flush tombstone; L0 overflows, merged into L1 (tombstone kept) [{tables:0 entries:0 tombstones:0} {tables:1 entries:1 tombstones:1} {tables:0 entries:0 tombstones:0}]
4. More flushes push K1 down to the bottom-most level      [{tables:0 entries:0 tombstones:0} {tables:0 entries:0 tombstones:0} {tables:1 entries:1 tombstones:0}]
5. Get(K1) -> NotFound (still correctly masked)
6. Tombstones remaining on disk: 0 -> K1 physically erased, space reclaimed
```
