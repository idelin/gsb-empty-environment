# leveled — 分层压缩（Leveled Compaction）调度合并器

类似 RocksDB 的分层压实算法，纯 Go 标准库实现。

## 设计

- **SSTable**（`sstable.go`）：有序不可变文件，记录为
  `seq | tombstone 标志 | key | value`，文件元信息记录 Key 范围与大小。
- **层级清单**（`db.go`）：L0 文件之间允许重叠，L1+ 层内互不重叠；
  全局递增的序列号（Seq）决定同一 Key 的新旧版本。
- **压缩调度**（`compact.go`）：
  - L0 文件数达到 `L0CompactionThreshold`（默认 4）时，选中 **L0 全部文件**；
  - L1+ 某层总大小超过容量阈值时，选出**与下一层重叠数据量最小**的 SSTable；
  - 将选中文件与下一层所有重叠文件做**多路归并排序**（最小堆，Key 升序、Seq 降序），
    同 Key 只保留最新版本；
  - 若某 Key 在更深层不存在更旧版本，其 **Tombstone 连同 Key 一起清理**，
    生成紧凑的新文件写入下一层，旧文件删除。

## 运行

```sh
go test -v ./...   # 测试
go run ./cmd/demo  # 演示：L0 堆积 4 个文件触发压缩的完整过程
```

## 验收场景（`compact_test.go`）

`TestL0CompactionMergesAndDropsTombstones`：

1. L1 预置 3 个文件 `[a,c]`、`[e,h]`、`[x,z]`；
2. 4 次 Flush 使 L0 堆积 4 个 SSTable（含覆盖写与 Tombstone），第 4 次触发 Compaction；
3. L0 全部文件与 L1 中有交集的 `[a,c]`、`[e,h]` 两个文件归并，`[x,z]` 不参与；
4. Tombstone（`c`、`h`）在归并中被清理，生成紧凑的 L1 新文件 `[a,q]`；
5. 断言：L0 清空、合并产物无 Tombstone、新值生效、已删 Key 不可见。

另有：

- `TestSizeTriggeredPicksSmallestOverlap`：容量触发时选择与下一层重叠最小的文件；
- `TestTombstoneKeptWhenDeeperLevelHasKey`：深层仍有旧版本时 Tombstone 必须保留。
