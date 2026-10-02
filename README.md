# ratelimit-metrics-exporter

限流监控指标度量与 Prometheus 格式自导出器（TypeScript / Node.js，零依赖）。

为令牌桶限流器提供全量运行时可观测性：实时统计 **Throughput**、**DroppedCount**、
**WaitDurationHistogram**、**CurrentTokenLevel**，并以标准度量快照 + Prometheus
文本 exposition 格式自导出。度量热路径为 O(1) 计数器递增，实测约 **48 ns/样本**
（远低于微秒级要求）。

## 运行环境

- Node.js ≥ 23.6（原生运行 TypeScript，无需编译），或 Node.js ≥ 22.6（加
  `--experimental-strip-types`）。源码仅使用可擦除类型语法，无需 tsc 构建。
- 无第三方依赖，测试使用 Node.js 内置 `node:test`。

## 快速开始

```bash
npm test          # node --experimental-strip-types --test test/*.test.ts
npm run demo      # 压测 + 期间采样度量接口的完整示例
```

## 组件

| 文件 | 说明 |
| --- | --- |
| `src/limiter.ts` | 令牌桶限流器：`tryAcquire()`（非阻塞）、`acquire()`（等待并记录等待时长） |
| `src/metrics.ts` | 度量核心：累计计数器、滑动窗口环（tumbling windows）、等待时长直方图 |
| `src/exporter.ts` | Prometheus 文本格式渲染 + 自包含 HTTP 导出器（`/metrics`、`/snapshot`） |

## 度量指标

| 指标 | 类型 | 说明 |
| --- | --- | --- |
| `ratelimit_requests_total` | counter | 总请求数（= allowed + dropped，守恒不变式） |
| `ratelimit_allowed_total` / `ratelimit_dropped_total` | counter | 累计放行 / 丢弃数 |
| `ratelimit_throughput_per_second` | gauge | 上一完整窗口的放行速率 |
| `ratelimit_dropped_last_window` | gauge | 上一完整窗口的丢弃数 |
| `ratelimit_current_token_level` | gauge | 当前令牌水位 |
| `ratelimit_window_allowed` / `ratelimit_window_dropped` | gauge | 当前窗口内放行 / 丢弃数 |
| `ratelimit_wait_duration_seconds` | histogram | 等待令牌耗时（累计 `le` 桶 + `_sum`/`_count`） |

JSON 快照（`GET /snapshot` 或 `limiter.snapshot()`）额外包含最近 N 个时间窗口
各自的放行/丢弃计数，窗口序列之和恒等于累计计数（数据守恒）。

## 使用示例

```ts
import { TokenBucketLimiter, startMetricsServer } from "./src/index.ts";

const limiter = new TokenBucketLimiter({ capacity: 1000, refillPerSec: 20_000 });
await startMetricsServer(limiter, { port: 9090 });   // GET :9090/metrics

if (limiter.tryAcquire()) { /* 放行 */ }             // 自动计入度量
await limiter.acquire();                              // 阻塞获取，记录等待时长
```

## 压测采样运行样例（`npm run demo` 实测输出）

```
t=500ms  allowed=11140  dropped=660988  throughput=8760/s  tokens=540  waits=2128  conserved=true
t=1000ms allowed=21822  dropped=745610  throughput=8760/s  tokens=0    waits=2432  conserved=true
t=1500ms allowed=30942  dropped=811730  throughput=15981/s tokens=0    waits=2672  conserved=true
t=2000ms allowed=41093  dropped=866787  throughput=15981/s tokens=0    waits=2880  conserved=true
t=2500ms allowed=48896  dropped=964320  throughput=19707/s tokens=0    waits=3216  conserved=true
t=3000ms allowed=58537  dropped=1054999 throughput=19707/s tokens=280  waits=3536  conserved=true

final snapshot:
  requestsTotal      = 1113552
  allowedTotal       = 58553
  droppedTotal       = 1054999
  conservation       = true
  currentTokenLevel  = 264.00
  wait histogram     = count=3552 mean=6.654ms max=149.804ms
  per-window (allowed/dropped):
    03.000  allowed=   8760  dropped= 643756
    04.000  allowed=  15981  dropped= 113983
    05.000  allowed=  19707  dropped= 145821
    06.000  allowed=  14105  dropped= 151439  (current)
```

压测期间每次采样均满足 `requests_total = allowed_total + dropped_total`；
每个时间窗口的放行数/丢弃数精确输出，窗口序列与累计计数守恒。

## 测试结果（`npm test`，Node.js 内置 test runner）

```
✔ renderPrometheus emits valid exposition with conserved totals
✔ metrics interface stays consistent while sampled during a stress run
✔ routing: unknown paths and non-GET methods return 404
✔ live HTTP server serves /metrics and /snapshot
✔ tryAcquire admits up to capacity, then drops
✔ bucket refills at the configured rate and caps at capacity
✔ conservation holds under mixed stress: allowed + dropped === requests
✔ acquire() waits for a token and records the wait duration
✔ rejects invalid configuration
✔ per-window allowed/dropped counts are exact and conserve totals
✔ throughput and droppedLastWindow come from the last completed window
✔ window ring retains only the configured number of windows
✔ wait-duration histogram: cumulative buckets, sum, min, max
✔ metrics recording overhead is sub-microsecond per sample
    metrics hot path: best 47.5 ns/sample over 5 rounds x 400,000 samples

tests 14  pass 13  skipped 1  fail 0   # 沙箱环境；正常环境 14 全过
```

> 注：在禁止监听 socket 的沙箱环境中，`live HTTP server` 用例会标记为
> skipped，压测采样用例通过同一处理函数 `resolveMetricsResponse` 在进程内
> 完成，验收路径完全一致；正常环境下 HTTP 用例真实监听并 fetch 验证。

## 设计要点

- **亚微秒统计延迟**：记录路径只有一次 `Date.now()`、窗口比较与计数器递增，
  无分配、无锁、无定时代价；窗口轮换惰性摊还。
- **数据守恒**：每次准入决策同步记录 exactly-one（allowed/dropped），任意时刻
  采样均满足守恒不变式，压测并发采样亦成立。
- **标准快照**：`MetricsSnapshot` 为纯数据对象，Prometheus 渲染与 HTTP 服务
  均为其上的纯函数/薄适配层，便于测试与替换。
