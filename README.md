# ratelimit-metrics

限流器全量运行时可观测性组件（TypeScript，零依赖）：实时统计 **Throughput、
DroppedCount、WaitDurationHistogram、CurrentTokenLevel**，并通过内置导出器以
**Prometheus 文本格式**自导出标准度量快照。

## 环境要求

- Node.js >= 23.6（内置 type-stripping，直接运行 `.ts`，无需编译与任何依赖）
- 如需真实 HTTP 端口，环境需允许监听 socket（沙箱内自动降级为处理器级测试）

## 结构

```
src/metrics.ts       度量原语：Counter / Gauge / Histogram / RateLimiterMetrics（窗口快照）
src/rate-limiter.ts  令牌桶限流器，每个准入决策都记录到度量
src/prometheus.ts    Prometheus 文本 exposition 渲染（HELP/TYPE/bucket{le}/+Inf/sum/count）
src/exporter.ts      MetricsHandler（传输无关核心）+ MetricsExporter（node:http 适配器）
examples/stress-demo.ts  压测 + 并发采样 /metrics 的演示
examples/serve.ts        真实 HTTP 服务（curl localhost:9464/metrics）
test/                node:test 内置测试（15 个用例）
```

## 设计要点

- **亚微秒记录**：热路径只有数值自增与定长 typed-array 直方图扫描，无分配、
  无格式化；基准测试平均每次记录操作 < 1µs（实测约 10–30ns）。
- **数据守恒**：单线程事件循环下自增即原子；`snapshot()` 在读取与重置窗口计数
  之间不发生事件循环让出，因此任意快照序列满足
  `Σ window.allowed === allowedTotal`、`allowed + dropped === 总请求数`。
- **一致性快照**：每次抓取 `/metrics` 取一次快照，计数器/直方图/窗口在同一
  快照内自洽；计数器单调递增。
- **窗口统计**：`snapshot()` 原子地关闭当前窗口并开启新窗口，输出每个时间窗口
  内的放行数、丢弃数与窗口吞吐（allowed/s）。

## 运行

```bash
npm test    # node --test（15 个用例）
npm run demo   # 50 万次压测 + 每 250ms 采样 /metrics
npm run serve  # 真实 HTTP 导出器（非沙箱环境）
```

## 运行样例（npm run demo 节选）

```
stress: 500000 tryAcquire + 5000 blocking acquire against 20k tok/s (burst 200)
sampling /metrics every 250ms

--- sample #5 @ 2026-10-02T05:34:38.475Z ---
ratelimit_allowed_total 7472
ratelimit_dropped_total 495399
ratelimit_wait_duration_seconds_sum 36.19239147500004
ratelimit_wait_duration_seconds_count 2871
ratelimit_token_level 0.15416000000004715
ratelimit_window_allowed 2100
ratelimit_window_dropped 0
ratelimit_window_throughput_per_sec 6243.145249485531

--- final tally (1.55s) ---
client-side tryAcquire: allowed=4601 dropped=495399
server-side totals:     allowed=9601 dropped=495399 total=505000
wait samples: count=5000 sum=72.874s mean=14.57ms
conservation: OK (allowed+dropped === 505000)
effective throughput: 6210 req/s admitted

--- final /metrics exposition ---
# HELP ratelimit_wait_duration_seconds Time requests waited for a token.
# TYPE ratelimit_wait_duration_seconds histogram
ratelimit_wait_duration_seconds_bucket{le="0.0001"} 4649
...
ratelimit_wait_duration_seconds_bucket{le="+Inf"} 5000
ratelimit_wait_duration_seconds_sum 72.87353998000214
ratelimit_wait_duration_seconds_count 5000
```

## 测试结果（npm test）

```
✔ handler serves /metrics during a stress run; data is conserved
﹣ HTTP adapter serves the same exposition over a real socket   # 沙箱禁 socket，跳过
✔ counter and gauge basics
✔ histogram cumulative buckets, count and sum
✔ histogram rejects empty or duplicate bounds
✔ window snapshot rotates and conserves totals
✔ recording is sub-microsecond on average
✔ exposition contains HELP/TYPE and all four metric families
✔ histogram exposition is cumulative and terminates with +Inf
✔ custom prefix is applied
✔ tryAcquire admits up to capacity then drops
✔ tokens refill over time and level gauge tracks bucket
✔ acquire waits for a token and records wait duration
✔ acquire times out and counts as drop
✔ stress: conservation under mixed load

ℹ tests 15  ℹ pass 14  ℹ fail 0  ℹ skipped 1
```

> 沙箱环境禁止监听 socket，真实 HTTP 适配器用例自动跳过；传输无关的
> `MetricsHandler` 在压测期间被并发采样并完成全部守恒断言。非沙箱环境下
> 该用例会真实绑定端口并通过 `fetch` 验证。
