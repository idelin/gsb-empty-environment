# clock-skew-guardian

双时钟源偏差检测与租约熔断守护器（TypeScript / Node.js 内置测试）。

## 设计

守护器持续比对两个时钟源的时间推进步调：

- **单调时钟（Monotonic）**：`performance.now()`，只增不减，不受系统时间调整影响；
- **墙钟（Wall-Clock）**：`Date.now()`，会随人为修改系统时间、NTP 阶跃而跳变。

每次采样计算偏差 `skew = wallΔ - monoΔ`，触发以下任一条件立即**紧急熔断**：

| 场景 | 检测条件 | 原因码 |
| --- | --- | --- |
| 人为回拨系统时间 / NTP 大幅阶跃 | 单次 `|skew| > maxSkewMs`（默认 250ms） | `WALL_CLOCK_JUMP` |
| 虚拟机时钟挂起（VM suspend） | 单调时钟单次推进 `> maxTickGapMs`（默认 5s） | `CLOCK_SUSPEND` |
| 缓慢漂移累积 | 累积 `|Σskew| > maxCumulativeSkewMs`（默认 1s） | `DRIFT_ACCUMULATED` |

**熔断动作**：强制注销所有活跃租约 → 租约注册表降级为安全模式（拒绝一切新租约）→
守护器锁定 `SAFE_MODE`，不会自动恢复，须人工确认时钟一致后调用 `reset()`。

## 结构

- `src/clock.ts` — 时钟源抽象：`SystemMonotonicClock` / `SystemWallClock` / `ManualClock`（测试用）
- `src/lease.ts` — `LeaseRegistry`：租约申请/释放/强制注销/安全模式
- `src/guardian.ts` — `ClockSkewGuardian`：双时钟比对、熔断、复位（基于 `EventEmitter`）
- `tests/guardian.test.ts` — Node.js 内置测试（`node:test` + `node:assert`）
- `examples/demo.ts` — 运行样例：模拟系统时间回拨 1 小时

## 运行

要求 Node.js ≥ 23.6（内置 TypeScript 类型剥离，无需编译）。

```bash
npm test      # 运行全部测试
npm run demo  # 运行回拨 1 小时的熔断演示
```

## 使用示例

```ts
import { SystemMonotonicClock, SystemWallClock } from "./src/clock.ts";
import { LeaseRegistry } from "./src/lease.ts";
import { ClockSkewGuardian } from "./src/guardian.ts";

const registry = new LeaseRegistry();
const guardian = new ClockSkewGuardian(
  registry,
  { monotonic: new SystemMonotonicClock(), wall: new SystemWallClock() },
  { intervalMs: 100, maxSkewMs: 250, autoStart: true },
);

guardian.on("trip", (e) => console.error("熔断!", e.reason, e.detail));
const lease = registry.acquire("worker-1", Date.now(), 30_000);
```

## 验收场景（测试结果）

`npm test` 输出（9 项全部通过）：

```
✔ 正常步调：双时钟同步推进时不熔断，租约保持活跃
✔ 验收边界：系统时间突然回拨 1 小时，下一瞬间熔断所有租约并降级安全模式
✔ NTP 大幅向前阶跃 1 小时同样触发熔断
✔ 虚拟机时钟挂起：采样间隔异常（单调时钟单次推进超限）触发熔断
✔ 小幅 NTP  slew 微调（每次 50ms）不触发瞬时熔断
✔ 缓慢漂移持续累积最终超限熔断
✔ 熔断后持续采样保持安全模式，不会自动恢复
✔ 人工 reset 后退出安全模式，可重新申请租约
✔ 租约注册表：正常申请与释放
ℹ tests 9  ℹ pass 9  ℹ fail 0
```

`npm run demo` 关键输出：

```
>>> 模拟人为修改系统时间：墙钟回拨 1 小时 <<<
[sample] monoΔ=100ms wallΔ=-3599900ms skew=-3600000ms cumulative=-3600000ms
*** CIRCUIT BREAKER TRIPPED ***
  reason        : WALL_CLOCK_JUMP
  revoked leases: lease-1(worker-1), lease-2(worker-2), lease-3(worker-3)
  guardian state: SAFE_MODE
[lease] new acquire rejected: LeaseRegistry is in SAFE MODE: new leases are rejected
```
