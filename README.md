# cachebench — 缓存操作指标度量与吞吐基准分析器

纯 Go 标准库实现。内置高精度打点的缓存审计组件，无锁原子收集核心指标，
支持 JSON 格式健康度评估报告与内存碎片率，并提供吞吐基准分析器定位
命中率拐点与内存驱逐突变区间。

## 结构

- `metrics.go` — 无锁原子计数器（Hit/Miss/Eviction/Set）+ 分片环形缓冲
  微秒级延迟记录器（16 分片 × 8192 槽，写入仅一次 `atomic.Add`，无锁）
- `cache.go` — LRU 缓存（按字节容量驱逐），按页分配模型计算内存碎片率
- `report.go` — JSON 健康度评估报告（命中率、P50/P90/P99/P99.9/Max 延迟、
  碎片率、HEALTHY/DEGRADED/CRITICAL 评级）
- `analyzer.go` — 基准分析器：工作集扫描定位命中率拐点；
  时间窗采样 + `median + kσ` 检测驱逐突变区间
- `main.go` — 三阶段演示：拐点扫描 → 高并发压测（驱逐风暴）→ JSON 报告
- `cache_test.go` — go test 高并发压力测试与单元测试

## 运行

```sh
go run .        # 运行三阶段基准分析演示
go test -v ./...  # 运行压力测试与单元测试（建议加 -race）
```

## 运行样例（go run .）

```
=== 阶段一：命中率拐点扫描（工作集/容量 0.25x ~ 8x）===
  工作集  0.25x (  1024 keys): 命中率  95.12%  驱逐        0  吞吐      2826439 ops/s
  工作集  0.50x (  2048 keys): 命中率  89.64%  驱逐        0  吞吐      2858957 ops/s
  工作集  0.75x (  3072 keys): 命中率  84.62%  驱逐        0  吞吐      3663468 ops/s
  工作集  1.00x (  4096 keys): 命中率  79.74%  驱逐        0  吞吐      2748943 ops/s
  工作集  1.50x (  6144 keys): 命中率  57.16%  驱逐     4351  吞吐      2290376 ops/s
  工作集  2.00x (  8192 keys): 命中率  43.53%  驱逐     7186  吞吐      2424005 ops/s
  工作集  4.00x ( 16384 keys): 命中率  22.22%  驱逐    11287  吞吐      2011995 ops/s
  工作集  8.00x ( 32768 keys): 命中率  11.13%  驱逐    13520  吞吐      2896445 ops/s
>> 命中率拐点位于工作集 1.50x（命中率 79.74% -> 57.16%）

=== 阶段二：高并发压测 + 驱逐突变区间检测 ===
  压测完成：500000 ops，耗时 1.215s，吞吐 411378 ops/s
  检测到 4 个驱逐突变区间（median+2σ 阈值）：
    窗口 #10 [380ms, 401ms): 驱逐 12016 次
    窗口 #11 [401ms, 437ms): 驱逐 12169 次
    窗口 #22 [700ms, 729ms): 驱逐 11106 次
    窗口 #30 [885ms, 901ms): 驱逐 14984 次

=== 阶段三：缓存健康度评估报告（JSON）===
{
  "hits": 207686, "misses": 72412, "sets": 219902, "evictions": 145385,
  "total_ops": 500000, "hit_rate": 0.7414,
  "latency_p50_us": 0, "latency_p90_us": 0, "latency_p99_us": 115,
  "latency_p999_us": 1696, "latency_max_us": 8168,
  "memory_fragmentation": 0.5, "health": "DEGRADED", ...
}
```

## 测试结果（go test -race -v ./...）

```
=== RUN   TestConcurrentStress
    压测: 400000 ops / 1.336s = 299323 ops/s, 命中率 48.70%, 驱逐 47169,
    P99 2141us, 碎片率 50.00%, 驱逐突变区间 0 个
--- PASS: TestConcurrentStress (1.37s)
=== RUN   TestLatencyMicrosecondPrecision
--- PASS: TestLatencyMicrosecondPrecision (0.00s)
=== RUN   TestEvictionAndFragmentation
--- PASS: TestEvictionAndFragmentation (0.00s)
=== RUN   TestHitRateInflection
    工作集 0.25x: 命中率 95.72% ... 工作集 4.00x: 命中率 22.50%
--- PASS: TestHitRateInflection (0.88s)
=== RUN   TestEvictionSurgeDetection
--- PASS: TestEvictionSurgeDetection (0.00s)
=== RUN   TestHealthAssessment
--- PASS: TestHealthAssessment (0.00s)
PASS
ok  	cachebench	6.167s
```
