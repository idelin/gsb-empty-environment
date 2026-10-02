# lsmwatch — LSM 读放大与 Bloom Filter 误判率监控器

一个仅依赖 Go 标准库的 LSM 存储引擎性能度量诊断器：

- **读放大统计**：记录每次 `Get` 实际读取的 SSTable 数量（累计 + 滑动窗口即时指标）。
- **误判率统计**：记录 Bloom Filter 的 False Positive 比率（判定"可能有"但实际不存在）。
- **动态建议**：读放大突增时建议增加 Compaction 并发/权重压制读延迟；误判率超阈值时建议重建 Filter。

## 结构

| 文件 | 说明 |
| --- | --- |
| `bloom.go` | 双重哈希 Bloom Filter（可调 bits/key） |
| `sstable.go` | SSTable：内嵌 Bloom Filter，可关闭/重建 |
| `engine.go` | 简化 LSM 引擎：MemTable、Flush、Get、Compact，Get 路径逐次上报度量 |
| `monitor.go` | 监控器：滑动窗口指标、基线标定、告警（`READ_AMPLIFICATION_SPIKE` / `BLOOM_FILTER_REBUILD`） |
| `cmd/demo/main.go` | 验收场景演示 |

## 运行样例

```console
$ go run ./cmd/demo
== 阶段1: 写入随机散列 Key，Bloom Filter 正常（健康基线） ==
已写入 2000 个 Key，共 5 个 SSTable，Bloom Filter 8 bits/key
【健康基线】累计Get=2000 累计读放大=1.048 窗口读放大=1.047 窗口误判率=1.56% 基线=1.048

== 阶段2: 故意关闭全部 Bloom Filter（故障注入） ==
  🚨 告警: [READ_AMPLIFICATION_SPIKE] 读放大从基线 1.05 跃升至 3.01 | 读放大=3.01 误判率=1.51% | 建议: 增加 Compaction 并发/权重，加速 SSTable 合并以压制读延迟
【Bloom 关闭后】累计Get=3000 累计读放大=2.099 窗口读放大=4.200 窗口误判率=0.00% 基线=1.048

== 阶段3: 重开 Bloom Filter，但位数过小（2 bits/key） ==
  🚨 告警: [BLOOM_FILTER_REBUILD] Bloom Filter 误判率 28.00% 超过阈值 10.00% | 读放大=4.09 误判率=28.00% | 建议: 重建 Bloom Filter（提高 bits/key）以降低误判率
【过滤器过小】累计Get=3500 累计读放大=2.047 窗口读放大=1.697 窗口误判率=23.22% 基线=1.048

== 阶段4: 采纳建议 —— 重建 Filter(10 bits/key) + Compaction ==
【治理后】累计Get=4000 累计读放大=1.916 窗口读放大=1.000 窗口误判率=0.00% 基线=1.048
```

验收点：写入随机散列 Key（SHA-256）后，故意关闭 Bloom Filter，监控器即时捕捉到
窗口读放大从基线 **1.05 跃升至 4.20**，并输出"增加 Compaction 并发/权重压制读延迟"的告警；
采纳建议执行 Compaction 后读放大回落至 **1.00**。

## 测试结果

```console
$ go test ./... -v
=== RUN   TestBloomFilterNoFalseNegatives
--- PASS: TestBloomFilterNoFalseNegatives (0.00s)
=== RUN   TestEnginePutGetAndMiss
--- PASS: TestEnginePutGetAndMiss (0.00s)
=== RUN   TestBloomDisabledForcesFullScan
--- PASS: TestBloomDisabledForcesFullScan (0.01s)
=== RUN   TestReadAmplificationJump
--- PASS: TestReadAmplificationJump (0.04s)
    engine_test.go:112: 读放大从基线 1.05 跃升至 4.20，告警: 读放大从基线 1.05 跃升至 3.01
=== RUN   TestCompactionSuppressesReadAmplification
--- PASS: TestCompactionSuppressesReadAmplification (0.01s)
=== RUN   TestTinyBloomTriggersRebuildAlert
--- PASS: TestTinyBloomTriggersRebuildAlert (0.03s)
=== RUN   TestReadAmplificationSpikeAlert
--- PASS: TestReadAmplificationSpikeAlert (0.00s)
=== RUN   TestBloomFalsePositiveAlert
--- PASS: TestBloomFalsePositiveAlert (0.00s)
=== RUN   TestNoAlertWhenHealthy
--- PASS: TestNoAlertWhenHealthy (0.00s)
=== RUN   TestAlertRearmAfterRecovery
--- PASS: TestAlertRearmAfterRecovery (0.00s)
=== RUN   TestNoSpikeAlertWithoutBaseline
--- PASS: TestNoSpikeAlertWithoutBaseline (0.00s)
PASS
ok  	lsmwatch	0.947s
```

## 告警判定规则

- **读放大突增**：窗口读放大 ≥ 3.0 且 ≥ 2× 健康基线（`Monitor.Calibrate()` 标定）→ 建议增加 Compaction 权重；恢复后自动解除并允许再次告警。
- **误判率过高**：窗口误判率 ≥ 10%（且窗口内判定样本 ≥ 50）→ 建议重建 Bloom Filter（提高 bits/key）。
