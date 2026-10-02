# stcs — Size-Tiered Compaction 优化器（Go 标准库实现）

面向高吞吐写入场景的 LSM 压缩策略：同一层（size tier）内累积 N 个大小相近的
SSTable 后，触发一次全量多路归并，合并为单个大 SSTable，并在合并过程中统计
读放大与写放大比率。仅依赖 Go 标准库。

## 结构

- `sstable.go` — SSTable 文件格式、流式 Writer / Iterator。记录为
  `[seq u64][keyLen u32][valLen u32][key][value]`，footer 存条目数、seq 范围与
  首末 key。
- `merge.go` — 基于最小堆的 k 路归并；同一 key 的多个版本只保留 seq 最大
  （最新）的一条，返回丢弃的过期版本数。
- `compactor.go` — 大小分级压缩器。按 Cassandra 风格把大小相近（均值 ±ratio）
  的表分桶；某桶累积到 `threshold`（N）张表即触发全量归并，输出自然落入更高一层。
  合并时累计读/写字节，并通过 `OnCompact` 回调上报每次合并的放大比率。
- `engine.go` — 写入路径：memtable 写满（默认 10MiB）即 flush 成 SSTable，
  随后交给压缩器；`Get` 先查 memtable 再按 seq 从新到旧查 SSTable。
- `metrics.go` — 放大指标：
  - 写放大 =（flush 字节 + 合并输出字节）/ 用户写入字节。单层 STCS 每字节最多
    被重写一次，理论控制线为 **2.0**。
  - 合并读放大 = 合并读取字节 / 合并写回字节（全量归并下略大于 1.0）。
  - 点查读放大 = 当前存活 SSTable 数（含峰值高水位）。

## 运行样例

```console
$ go run ./cmd/demo
[write] 25000 puts, 25.8MiB ingested, 2 flushes, live tables=2, WA=0.788
[compact] merged 4 tables (40.6MiB) -> table-000004.sst (40.1MiB); dropped 552 stale versions; merge read-amp=1.014 write-amp=0.986; live tables 4 -> 1
[write] 50000 puts, 51.6MiB ingested, 5 flushes, live tables=2, WA=1.762
[write] 75000 puts, 77.3MiB ingested, 7 flushes, live tables=4, WA=1.437
[compact] merged 4 tables (40.6MiB) -> table-000009.sst (40.1MiB); dropped 552 stale versions; merge read-amp=1.014 write-amp=0.986; live tables 5 -> 2
[write] 100000 puts, 103.1MiB ingested, 10 flushes, live tables=4, WA=1.762
[compact] merged 4 tables (40.6MiB) -> table-000014.sst (40.1MiB); dropped 552 stale versions; merge read-amp=1.014 write-amp=0.986; live tables 6 -> 3
[write] 125000 puts, 128.9MiB ingested, 12 flushes, live tables=3, WA=1.878
[write] 150000 puts, 154.7MiB ingested, 15 flushes, live tables=6, WA=1.762
---- final ----
elapsed            936ms
user bytes         154.7MiB
flush writes       157.1MiB (16 flushes)
compaction read    121.9MiB
compaction write   120.2MiB (3 compactions, 1656 stale versions dropped)
write amplification      1.792 (bytes written / user bytes)
compaction read-amp      1.014 (bytes read / bytes written)
point-lookup read-amp    7 live tables now, peak 7
```

可调参数：`go run ./cmd/demo -puts 300000 -memtable-mib 10 -tier-threshold 4`。

## 测试

```console
$ go test -v ./...
=== RUN   TestBucketTables
--- PASS: TestBucketTables (0.00s)
=== RUN   TestCompactorTriggersAtThreshold
--- PASS: TestCompactorTriggersAtThreshold (0.04s)
=== RUN   TestSizeTieredCompactionAcceptance
    engine_test.go:60: puts=38495 flushes=4 user=40.0MiB flushed=40.6MiB compacted(in=40.6MiB out=39.2MiB)
--- PASS: TestSizeTieredCompactionAcceptance (1.31s)
=== RUN   TestConcurrentSustainedWrites
    engine_test.go:191: compactions=24 liveTables=8 WA=3.636
--- PASS: TestConcurrentSustainedWrites (1.61s)
=== RUN   TestMergeKeepsOnlyLatestVersion
--- PASS: TestMergeKeepsOnlyLatestVersion (0.02s)
=== RUN   TestMergeEmptyInputs
--- PASS: TestMergeEmptyInputs (0.02s)
=== RUN   TestTableRoundTrip
--- PASS: TestTableRoundTrip (0.01s)
=== RUN   TestTableWriterRejectsUnsortedKeys
--- PASS: TestTableWriterRejectsUnsortedKeys (0.00s)
PASS
ok  	stcs	3.319s
```

## 验收边界对照（`TestSizeTieredCompactionAcceptance`）

- 持续高频写入，memtable 每满 10MiB flush 一次，共 4 个 ~10MiB 的 SSTable。
- 同层累积 4 个大小相近的表后触发一次全量多路归并，生成单个 ~40MiB
  （实测 39.2MiB，差额为被丢弃的重复版本）新文件，输入文件被清理。
- 500 个热点 key 被反复更新；归并后扫描输出表验证 key 严格递增（无重复），
  且每个热点 key 的值都是最新版本（seq 最大）。
- 写放大 = (flush + 合并写) / 用户字节，断言不超过单层 STCS 理论控制线 2.0；
  合并读放大断言落在 [1.0, 1.2]。
