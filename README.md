# 分布式租约管理器与时钟漂移容忍器（单机模拟）

Go 标准库实现的分布式租约（Lease）协调内核，含时钟漂移容忍与脑裂防护。

## 设计

- **Coordinator**（`lease.Coordinator`）：租约协调端。仅当旧租约完整经过
  `TTL + maxDrift` 安全窗口后才授予新主，并发放单调递增的 fencing token。
- **Manager**（`lease.Manager`）：节点侧租约管理器。安全期为
  `TTL - maxDrift`，过期后主节点主动放弃特权、拒绝一切写操作。
- **双时钟安全自检**（`Manager.SafetyCheck`，写路径与后台看门狗均触发）：
  - 单调时钟：判定安全期是否结束（不受 NTP 校时影响）；
  - 墙上时钟 vs 单调时钟差值：超过 `maxDrift` 即判定时钟突变（如前拨），
    立即**紧急提前降级**。
- **Clock 抽象**：`RealClock` / `ManualClock`（测试与故障注入用，可只拨墙上时间）。

## 验收边界

| 参数 | 值 |
|---|---|
| 租约 TTL | 10s |
| 最大时钟漂移 maxDrift | 500ms |
| 主节点停用写操作时刻 | 9.5s（TTL - maxDrift） |
| 时钟前拨 1s 突变 | 触发紧急提前降级，杜绝双主 |

## 运行

```sh
go test ./lease/ -v   # 测试
go run ./cmd/demo     # 样例（真实时钟，约 17s）
```

## 测试结果

```
--- PASS: TestPrimaryStopsWritesAtSafeDeadline   # 9.5s 必须停止主身份写
--- PASS: TestClockJumpTriggersEmergencyDemotion # 前拨 1s 触发紧急降级
--- PASS: TestSmallDriftWithinTolerance          # 300ms 漂移被容忍
--- PASS: TestNoSplitBrain                       # 安全窗口内拒绝新主，fencing 单调递增
--- PASS: TestWatchdogAutoDemotion               # 看门狗自动降级（真实时钟）
```
