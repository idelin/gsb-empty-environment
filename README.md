# STCS — Size-Tiered Compaction 优化器

面向高吞吐写入场景的大小分级压缩策略（Go 标准库实现，无第三方依赖）。

## 策略

- Memtable 刷盘产生的新 SSTable 进入 **tier 0**。
- 同一 tier 内累积 **N 个大小相近**（默认 N=4，最大/最小 ≤ 1.5）的
  SSTable 后，触发一次**全量多路归并**（k-way heap merge），输出单个
  大 SSTable 到下一 tier，输入文件删除。
- 归并时同一 Key 只保留**最新版本**（序列号最大者）；tombstone 在
  输出层被回收。
- 合并过程统计读/写放大：
  - `WA = (flush 字节 + compaction 写字节) / 逻辑写字节`
  - `RA = compaction 读字节 / 逻辑写字节`
  - 单层合并理论控制线：WA ≤ 2.0，RA ≈ 1.0；每多一层级联 WA 上限 +1。

## 布局

- `stcs/sstable.go` — SSTable 磁盘格式、写入器与流式迭代器
- `stcs/compactor.go` — 分层调度、相似度挑选、k-way 归并、放大统计
- `stcs/compactor_test.go` — 验收测试（go test）
- `cmd/stcs-demo` — 高频写入运行样例

## 运行

```sh
go test ./stcs/ -v     # 验收测试
go run ./cmd/stcs-demo # 运行样例
```

## 验收覆盖

| 边界 | 测试 |
| --- | --- |
| 同层 4×10MB 触发合并为单个 ~40MB 文件 | `TestFourSimilarTablesMergeIntoOne` |
| 重复更新的 Key 只保留最新版本 | `TestFourSimilarTablesMergeIntoOne` |
| 写放大 ≤ 理论控制线 2.0（单层）/ 3.0（两层级联） | 同上 / `TestHighFrequencyWritesCascade` |
| 持续高频写入、层级联 | `TestHighFrequencyWritesCascade` |
| 不足 N 个不合并 | `TestNoMergeBelowFanout` |
| 大小不相近不合并 | `TestSizeSimilarityGating` |
| 删除（tombstone）回收 | `TestTombstonesDropped` |
