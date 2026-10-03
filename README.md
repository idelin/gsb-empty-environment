# cdcprobe — CDC 全链路性能监控探针

纯 Go 标准库实现的 CDC（Change Data Capture）数据变更吞吐与端到端同步延迟（Lag）探针。

## 架构

```
Source(心跳注入) --stream--> Link(网络传输+目标应用) --> Monitor(消费/测量/报警/限流)
      ^                                                            |
      +-------------------- 动态限流(RateLimiter) <-----------------+
```

- `heartbeat.go` — 探针心跳行（纳秒级时间戳）与源端周期注入器
- `link.go` — 模拟复制链路：基础网络延迟 + 目标端应用耗时 + 可注入的瞬时拥塞
- `limiter.go` — 动态令牌桶限流器（报警时按比例降速，恢复后还原）
- `monitor.go` — 消费端：计算端到端 Lag、阈值报警、联动限流、ASCII 延迟曲线
- `cmd/cdcprobe` — 可运行演示：3s 后注入 +1200ms 拥塞，观察报警、限流与恢复

## 运行

```sh
go run ./cmd/cdcprobe
```

## 测试

```sh
go test ./... -v
```

## 验收边界覆盖

| 要求 | 实现 | 测试 |
|---|---|---|
| 持续注入心跳探针（纳秒时间戳） | `Source.Run` | `TestSourceInjectsHeartbeats` / `TestHeartbeatLagNanosecondPrecision` |
| 实时测量源库写入到目标应用完成的全链路耗时 | `Monitor.Observe` | `TestMonitorMeasuresEndToEndLag` |
| Lag 突破 1000ms 立即报警 | 阈值判定 + `Alerts()` 通道 | `TestMonitorAlertsAndThrottlesOnBreach` |
| 报警联动动态限流 | `RateLimiter.Throttle/Restore` | `TestEndToEndPipeline`（全链路集成） |
| 动态绘制延迟监控曲线 | `Monitor.RenderChart` | `TestRenderChart` |
