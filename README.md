# clock-guardian

双时钟源偏差检测与租约熔断守护器（TypeScript + Node.js 内置测试运行器，零依赖）。

## 原理

守护器同时采样两个独立时间基准：

- **Monotonic**（`performance.now()`）：单调递增，不受系统时间修改影响；
- **Wall-Clock**（`Date.now()`）：民用时间，可能被管理员修改、NTP 阶跃、虚拟机快照恢复所扰动。

每个 tick 计算撕裂量 `tear = wallDelta - monoDelta`。健康状态下两者步调一致（tear ≈ 0）。
出现以下任一情况立即熔断：

| 触发条件 | 场景 |
| --- | --- |
| `WALL_CLOCK_BACKWARD` | 人为回拨系统时间 / VM 快照恢复 |
| `CLOCK_TEAR` | NTP 大幅阶跃，两钟步调撕裂超过容差 |
| `WALL_CLOCK_STALLED` | 墙钟冻结而单调钟继续前进 |
| `MONOTONIC_STALLED` | 单调钟冻结而墙钟继续前进（VM 挂起） |

熔断动作（原子、一次性）：

1. 强制注销所有活跃租约（`LeaseManager.revokeAll`）；
2. 守护器降级为 **safe mode**，拒绝一切新租约；
3. 发出 `trip` 事件，携带原因、撕裂量与被注销租约清单。

租约过期计算只使用单调时钟，墙钟混乱无法延长或缩短租约的真实寿命。

## 运行

要求 Node.js ≥ 22.18（原生 TypeScript 类型擦除，无需编译）。

```bash
npm test       # node --test，7 个用例
npm run demo   # 运行熔断演示
```

## 演示输出

```
✅ acquired 3 leases: lease-alpha, lease-beta, lease-gamma
— healthy ticks (both clocks advance together) —
   mode=normal, active leases=3
— ⚡ system time is suddenly set BACK by 1 hour —
🚨 CIRCUIT BREAKER TRIPPED
   reason        : WALL_CLOCK_BACKWARD
   detail        : wall clock moved backwards by 3600000ms
   wall delta    : -3600000ms
   mono delta    : 100ms
   revoked leases: lease-alpha, lease-beta, lease-gamma
   mode=safe, active leases=0
🛡️  new lease rejected in safe mode: lease "lease-delta" rejected: manager is in safe mode
```

## 测试结果

```
✔ healthy clocks advancing in lockstep never trip the breaker
✔ small NTP jitter within tolerance does not trip
✔ ACCEPTANCE: wall clock set back 1 hour -> instant trip, all leases revoked, safe mode
✔ large forward NTP step trips with CLOCK_TEAR
✔ wall clock stalled (VM suspend) trips after stall threshold
✔ monotonic clock stalled while wall advances trips
✔ lease expiry uses monotonic time, immune to wall-clock jumps
ℹ pass 7  ℹ fail 0
```

## 结构

- `src/clocks.ts` — 时钟源抽象与可注入的 `FakeClocks`（测试/演示用）
- `src/lease-manager.ts` — 租约管理（过期基于单调时钟，支持紧急全量注销）
- `src/clock-guardian.ts` — 双时钟比对、熔断判定与安全模式降级
- `test/clock-guardian.test.ts` — 含验收用例：墙钟回拨 1 小时后下一 tick 立即熔断
- `examples/demo.ts` — 端到端演示脚本
