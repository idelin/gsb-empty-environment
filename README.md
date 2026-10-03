# cachebench — 缓存操作指标全面度量与吞吐基准分析器

内置高精度打点的缓存审计组件（Go 标准库实现，零第三方依赖）：

- **无锁原子采集**：Hit Count / Miss Count / Eviction Count / Set Count 等核心计数器
  全部通过 `sync/atomic` 无锁收集，高并发下无竞争瓶颈。
- **微秒级延迟分布**：每次 Get/Set 耗时记录到微秒级直方图
  （亚毫秒 1µs 分辨率、亚秒 1ms 分辨率），输出 P50/P90/P95/P99/P99.9/Min/Max/Mean。
- **时间窗口序列**：指标按固定窗口（默认 10ms）分片，用于事后定位
  **命中率拐点**（相邻窗口命中率最大跌幅）与 **内存驱逐突变区间**
  （基于中位数基线的鲁棒异常检测，对双峰分布不敏感）。
- **JSON 健康度报告**：汇总命中率、延迟分位、内存碎片率
  （缓存级估计碎片率 + Go 堆碎片率），并给出 0–100 健康度评分与问题清单。

## 目录结构

| 文件 | 说明 |
|---|---|
| `metrics.go` | 无锁原子计数器、微秒级延迟直方图、时间窗口序列 |
| `cache.go` | 256 分片 LRU 缓存，每次操作自动打点；内存碎片统计 |
| `report.go` | 拐点/突变区间分析、健康度评估、JSON 报告生成 |
| `stress.go` | 三阶段高并发压力场景驱动（测试与 CLI 共用） |
| `cmd/cachebench` | 命令行样例：运行压力场景并输出 JSON 报告 |

## 运行测试

```bash
go test ./...        # 全部单元测试 + 高并发压力验收测试
go test -race ./...  # 竞态检测
go test -short ./... # 跳过压力测试
```

## 运行样例

```bash
go run ./cmd/cachebench                      # 默认：32 并发，3 阶段 × 300ms
go run ./cmd/cachebench -workers 64 -capacity 20000 -phase-ms 500
```

### 样例输出（节选）

```json
{
  "totals": {
    "gets": 3380710, "hits": 2423257, "misses": 957453,
    "sets": 1034105, "evictions": 726309, "hit_rate": 0.7168
  },
  "get_latency": {
    "count": 3380710, "min_us": 0, "p50_us": 0, "p90_us": 0,
    "p99_us": 0, "p999_us": 1000, "max_us": 28123, "mean_us": 5.08
  },
  "memory": {
    "live_entries": 49920,
    "cache_fragmentation_rate": 0.3565,
    "go_heap_fragmentation_rate": 0.0883
  },
  "health": { "score": 78.34, "rating": "良好", "issues": [
    "缓存命中率偏低，建议检查容量配置或访问模式",
    "命中率出现拐点，存在访问模式突变",
    "检测到内存驱逐突变区间，容量可能不足"
  ]},
  "analysis": {
    "hit_rate_inflection": {
      "start_ms": 320, "end_ms": 330,
      "hit_rate_before": 0.514, "hit_rate_after": 0, "drop": 0.514
    },
    "eviction_spike_intervals": [
      { "start_ms": 320, "end_ms": 620, "windows": 30, "evictions": 650451 }
    ]
  }
}
```

压力场景为三阶段：热点读为主（命中率高位）→ 冷键洪峰（命中率拐点 +
驱逐突变区间）→ 回到热点（观察恢复），报告可准确定位阶段切换时刻。

## 测试结果

```
=== RUN   TestCacheSetGet                      --- PASS (0.00s)
=== RUN   TestCacheMiss                        --- PASS (0.00s)
=== RUN   TestCacheOverwrite                   --- PASS (0.00s)
=== RUN   TestCacheLRUEviction                 --- PASS (0.00s)
=== RUN   TestMemoryFragmentationRange         --- PASS (0.00s)
=== RUN   TestHistogramPercentiles             --- PASS (0.00s)
=== RUN   TestHistogramMicrosecondResolution   --- PASS (0.00s)
=== RUN   TestWindowSeries                     --- PASS (0.00s)
=== RUN   TestMetricsCounters                  --- PASS (0.00s)
=== RUN   TestReportJSON                       --- PASS (0.00s)
=== RUN   TestStressHighConcurrency            --- PASS (0.91s)
    hit-rate inflection at 320-330ms: 0.62 -> 0.00 (drop 0.62)
    eviction spike 320-620ms: 622567 evictions in 30 windows
PASS
ok      cachebench      1.014s
```

`go test -race ./...` 同样全部通过。
