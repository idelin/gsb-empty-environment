# Leveled Compaction 调度合并器

类似 RocksDB 的分层压实（Leveled Compaction）算法，Go 标准库实现，无第三方依赖。

## 设计

- **SSTable**（`sstable.go`）：磁盘上的有序不可变文件，格式为
  `magic | count | [seq, op, key, value]...`，元数据记录 Key 范围 `[MinKey, MaxKey]` 与字节数。
- **多路归并**（`merge.go`）：基于最小堆的 k-way merge，按 `(Key 升序, Seq 降序)` 弹出，
  每个 Key 只保留最新版本；压实到最底层时清理 Tombstone，物理移除废弃 Key。
- **调度器**（`lsm.go`）：
  - L0 文件互相重叠，文件数达到 `L0CompactionTrigger`（默认 4）即触发，**L0 全部文件**参与；
  - L1+ 按层容量 `BaseLevelSize * SizeMultiplier^(n-1)` 触发，挑选**与下一层重叠 Key 范围最小**
    （重叠文件数、重叠字节数最少）的 SSTable；
  - 选中文件与下一层所有有交集的 SSTable 归并，写出紧凑的新文件到下一层，旧文件删除；
  - 目标层之下无数据（最底层）时丢弃 Tombstone，否则保留（防止下层旧值复活）。

## 运行

```sh
go test ./... -v   # 运行测试
go run ./cmd/demo  # 运行演示样例
```

## 测试结果

```
=== RUN   TestL0TriggerCompactsWithOverlappingL1
--- PASS: TestL0TriggerCompactsWithOverlappingL1 (0.00s)
=== RUN   TestPickerChoosesSmallestOverlap
--- PASS: TestPickerChoosesSmallestOverlap (0.00s)
=== RUN   TestTombstonePreservedWhenNotBottommost
--- PASS: TestTombstonePreservedWhenNotBottommost (0.00s)
PASS
ok  	lsm
```

- `TestL0TriggerCompactsWithOverlappingL1`：验收主场景 —— L0 堆积 4 个 SSTable 触发压实，
  L0 全部文件与 L1 中有交集的 2 个文件（共 3 个，第 3 个不相交保持不动）归并，
  Tombstone 清理后生成紧凑的 L1 新文件。
- `TestPickerChoosesSmallestOverlap`：非 L0 层超容量时，调度器选中与下一层重叠最小的文件。
- `TestTombstonePreservedWhenNotBottommost`：目标层之下仍有数据时保留 Tombstone。

## 样例输出（`go run ./cmd/demo`）

```
== 1. L1 铺底 3 个 SSTable（t1[a..c] t2[g..m] t3[x..z]） ==
  L1: L1#000001[a..c](48B) L1#000002[g..m](48B) L1#000003[x..z](48B)

== 2. 连续 Flush，L0 逐个堆积（Key 范围 [b..h]，与 t1/t2 相交） ==
Flush #1 (b, d, f=F0)
Flush #2 (d=D2, f=F1)
Flush #3 (DEL d, h)
  L0: L0#000004[b..f](68B) L0#000005[d..f](48B) L0#000006[d..h](46B)
  L1: L1#000001[a..c](48B) L1#000002[g..m](48B) L1#000003[x..z](48B)

== 3. 第 4 次 Flush -> L0 达到 4 个文件，触发 Leveled Compaction ==
压实前 L0 文件数: 3
压实后：
  L1: L1#000008[a..m](168B) L1#000003[x..z](48B)

== 4. 合并产物内容（d 的 Tombstone 已清理，f 只保留最新版 F1） ==
  L1#000008[a..m](168B):
    PUT a = A1 (seq=1)
    PUT b = B1 (seq=7)
    PUT c = C1 (seq=2)
    PUT e = E1 (seq=14)
    PUT f = F1 (seq=11)
    PUT g = G1 (seq=3)
    PUT h = H1 (seq=13)
    PUT m = M1 (seq=4)
  L1#000003[x..z](48B):
    PUT x = X1 (seq=5)
    PUT z = Z1 (seq=6)

== 5. 读取验证 ==
  Get("a") = A1
  Get("d") = <不存在>
  Get("f") = F1
  Get("x") = X1
```
