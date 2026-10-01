# leaseguard — 分布式租约管理器与时钟漂移容忍器

单机模拟的分布式租约（Lease）协调内核，Go 标准库实现，零第三方依赖。

## 设计

- 租约由主节点持有，具有明确 TTL（默认 10s）。
- 主节点保守地扣除最大时钟漂移（500ms）与通信往返延迟（RTT）：
  `safeWindow = TTL - MaxClockDrift - MaxRTT`，安全期过后主动放弃特权、停止以主身份写。
- 看门狗周期性安全自检：
  1. **时钟突变检测**：本地时钟两次自检间前进超过 `CheckInterval + MaxClockDrift`，
     判定时钟不可信，触发**紧急提前降级**；
  2. **安全期强制**：到达安全边界即主动降级（TTL 尚未到期也降级）。
- 协调者（Coordinator）保证任意时刻至多一个未过期租约，杜绝双主脑裂。

## 验收边界

| 项 | 取值 |
|---|---|
| 租约时长 TTL | 10s |
| 最大时钟漂移 | 500ms |
| 停止写边界（无 RTT） | t = 9.5s |
| 时钟前拨 1s 突变 | 自检触发紧急提前降级 |

## 运行

```sh
go test ./...     # 运行测试
go run ./cmd/demo # 运行样例
```

## 样例输出（go run ./cmd/demo）

```
config: TTL=10s maxDrift=500ms maxRTT=200ms => safe window=9.3s

=== Scenario 1: safe boundary at TTL - drift - RTT = 9.3s ===
[t=    0s] node-A acquired lease (TTL=10s)
[t=    5s] node-A WRITE "set x=1"    -> OK
[t=  9.3s] safe period elapsed (TTL - maxDrift - maxRTT)
[t=  9.3s] node-A WRITE "set x=2"    -> REJECTED (lease: not leader ...)
[t=  9.3s] node-A demoted=true reason="lease safe period (9.3s) elapsed; TTL not yet expired"

=== Scenario 2: inject +1s clock jump at t=5s ===
[t=    0s] node-A acquired lease
[t=    0s] node-B acquire -> lease: held by another node (holder="node-A", expires in 10s)
[t=    5s] node-A WRITE "set y=1"    -> OK
[t=    5s] !! injecting clock fault: local clock jumps forward +1s (max drift 500ms)
[t=    6s] node-A emergency demotion: "emergency: clock jumped forward 1s since last self-check (tolerance 600ms)"
[t=    6s] node-A WRITE "set y=2"    -> REJECTED (lease: not leader ...)
[t=    6s] node-B acquired lease after A released it
[t=    6s] node-B WRITE "set y=3"    -> OK
[t=    6s] safety check: exactly one writable leader (A=false, B=true) — no split-brain
```

## 测试结果（go test ./lease/ -v）

```
--- PASS: TestLeaderStopsWritesAtSafeBoundary   # 9.5s 边界停止写（TTL 10s - 漂移 500ms）
--- PASS: TestClockJumpTriggersEmergencyDemotion # 前拨 1s 触发紧急提前降级
--- PASS: TestNoSplitBrainAfterDemotion          # 降级后旧主被隔离，无双主
--- PASS: TestCoordinatorRejectsConcurrentLease  # 协调者不发放并发租约
--- PASS: TestRTTShrinksSafeWindow               # RTT 进一步压缩安全窗口
--- PASS: TestLeaseExpiresFully                  # TTL 完全过期后禁止写
PASS
ok  	leaseguard/lease
```

## 代码结构

- `lease/clock.go` — 时钟抽象：`RealClock` / 可注入漂移与突变的 `SimClock`
- `lease/manager.go` — 协调者、租约管理器、看门狗安全自检
- `lease/manager_test.go` — 验收边界测试
- `cmd/demo/main.go` — 运行样例
