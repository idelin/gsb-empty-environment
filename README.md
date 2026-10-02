# cdcprobe — CDC 全链路性能监控探针

纯 Go 标准库实现的 CDC（Change Data Capture）数据变更吞吐与端到端同步延迟（Lag）探针。

## 架构

```
Injector ──> Store(源库) ──> Replicator(网络延迟模型) ──> Store(目标库) ──> Monitor
   ▲            心跳行携带纳秒时间戳 SrcUnixNano                              │
   └──────────────────── 动态限流（RateLimiter）<──────── 报警/恢复 ──────────┘
```

- `Heartbeat`：探针心跳行，含单调序号与高精度纳秒时间戳（`probe.go:24`）。
- `Injector`：源端按固定周期持续注入心跳行，受令牌桶限流器控制（`probe.go:130`）。
- `Replicator`：模拟 CDC 复制链路，按行应用网络延迟（可注入瞬时拥塞）（`probe.go:190`）。
- `Monitor`：消费端计算端到端 Lag = 目标端应用时刻 − 源端写入时刻；
  Lag 突破阈值（默认 1000ms）立即报警并把源端限流至低档，
  回落到阈值一半以下后自动恢复（`probe.go:266`）。
- `Plot`：把 Lag 样本渲染为 ASCII 延迟监控曲线，`-` 行为报警阈值线（`probe.go:394`）。

## 运行样例

```console
$ go run ./cmd/demo
starting CDC probe: 20 hb/s, lag threshold 1000ms, congestion at t=2s..4s
[   1.0s] lag=     6ms  injected=39 applied=39 rate=20 hb/s
[   2.0s] lag=     6ms  injected=60 applied=59 rate=20 hb/s
[   3.0s] lag=     6ms  injected=79 applied=59 rate=20 hb/s
[   3.6s] ALERT: end-to-end lag 1.525s exceeded threshold 1s -> throttle source to 2 hb/s
[   4.0s] lag=  1.525s  injected=91 applied=60 rate=2 hb/s
[   5.1s] lag=  2.977s  injected=93 applied=61 rate=2 hb/s
[   5.3s] RECOVERED: lag back below 500ms -> restore source to 20 hb/s
[   6.1s] lag=     6ms  injected=109 applied=109 rate=20 hb/s
[   7.1s] lag=     6ms  injected=129 applied=129 rate=20 hb/s
[   8.1s] lag=     8ms  injected=149 applied=149 rate=20 hb/s

summary: injected=149 applied=149 samples=149 max-lag=2.977s alerts=1

lag curve (max=2.977s, threshold=1s, samples=149)
  2.977s |                            #
         |                            ####
         |                            #######
         |                            ##########
         |                            #############
         |                            ################
         |                            ################
         |                            ################
         |----------------------------################----------------------------
         |                            ################
         |                            #################
      0s |########################################################################
         +------------------------------------------------------------------------
          t0                                                                    now
```

演示中 t=2s..4s 模拟网络瞬时拥塞（每行延迟 1500ms），Lag 突破 1000ms
后探针立即报警并把注入速率从 20 hb/s 限流至 2 hb/s；拥塞消除、Lag
回落到 500ms 以下后自动恢复全速。

## 测试结果

```console
$ go test ./... -v
=== RUN   TestHeartbeatLag
--- PASS: TestHeartbeatLag (0.00s)
=== RUN   TestStoreAppendSubscribe
--- PASS: TestStoreAppendSubscribe (0.00s)
=== RUN   TestRateLimiter
--- PASS: TestRateLimiter (0.15s)
=== RUN   TestInjectorInjectsHeartbeats
--- PASS: TestInjectorInjectsHeartbeats (0.12s)
=== RUN   TestReplicatorAppliesWithLatency
--- PASS: TestReplicatorAppliesWithLatency (0.02s)
=== RUN   TestMonitorAlertThrottleAndRecover
--- PASS: TestMonitorAlertThrottleAndRecover (0.00s)
=== RUN   TestEndToEndCongestion
--- PASS: TestEndToEndCongestion (0.90s)
=== RUN   TestPlot
--- PASS: TestPlot (0.00s)
PASS
ok  	cdcprobe	1.512s
?   	cdcprobe/cmd/demo	[no test files]
```

其中 `TestEndToEndCongestion` 是端到端验收用例：全链路注入 → 复制 →
应用 → 测量，在瞬时拥塞下验证 Lag 超阈值报警、动态限流与拥塞解除后
的自动恢复。
