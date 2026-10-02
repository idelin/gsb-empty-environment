# φ-Accrual 自适应心跳健康度探测器

基于失联概率评估的自适应心跳系统（仿 Cassandra / Akka 的 φ-Accrual Failure Detector）。
不使用固定的超时阈值，而是根据历史心跳间隔的**正态分布**动态计算当前节点崩溃的怀疑度 φ；
当 φ 超过阈值时触发租约回收（`lease-revoked` 事件）。

## 原理

每次心跳到达后，记录与上一次心跳的间隔，存入固定大小的滑动采样窗口，
实时维护间隔的均值 μ 与标准差 σ。对于当前已静默时长 `t`：

```
P_later(t) = 1 - F(t)        （F 为 N(μ, σ) 的累积分布函数，即"心跳比 t 更晚到达"的概率）
φ(t)       = -log10(P_later(t))
```

φ 的含义直观：**φ = 8 表示在当前历史分布下，节点其实还活着却只有 10⁻⁸ 的概率**。
因此：

- 网络抖动落在历史方差之内时，φ 缓慢增长，远低于阈值 —— 不误判；
- 真正断网时，静默时长迅速脱离分布尾部，φ 单调陡增并果断越过阈值 —— 不拖延。

与固定超时的本质区别：阈值作用于**概率**而非**时长**，检测灵敏度随网络状况自适应。

## 配置项

| 选项 | 默认值 | 说明 |
| --- | --- | --- |
| `threshold` | `8` | φ 阈值，超过即判定失效并回收租约 |
| `maxSampleSize` | `200` | 心跳间隔滑动窗口容量 |
| `minStdDeviationMs` | `25` | σ 下限，防止间隔过于规律时 φ 因微小延迟爆炸 |
| `acceptableHeartbeatPauseMs` | `0` | 可容忍的额外停顿（叠加到 μ 上，吸收 GC 等毛刺） |
| `firstHeartbeatMeanMs` / `firstHeartbeatStdDeviationMs` | `500` / `100` | 冷启动时首个心跳的分布估计 |

## 使用

```ts
import { PhiAccrualFailureDetector } from './src/index.ts';

const detector = new PhiAccrualFailureDetector({ threshold: 8 });

detector.on('lease-revoked', ({ phi, at }) => reclaimLease(phi, at));
detector.on('lease-restored', () => reissueLease());

detector.reportHeartbeat();          // 每次收到心跳时调用
detector.check();                    // 周期性调用，驱动状态迁移与事件
detector.phi();                      // 当前怀疑度
detector.isAvailable();              // 当前是否存活
```

所有方法都接受可选的 `now` 参数（毫秒时间戳），便于接入虚拟时钟做确定性仿真与测试。

## 运行

要求 Node.js ≥ 22.6（利用原生 TypeScript 类型擦除，零依赖、零构建）。

```bash
npm test     # Node.js 内置测试运行器 (node:test)
npm run demo # 对比演示：固定超时 vs φ 自适应
```

## 演示输出（`npm run demo`）

虚拟时钟仿真：正常心跳 ~100ms（抖动 σ≈25ms）→ 两次延迟尖峰（300ms / 280ms）→ 真正断网 1500ms → 恢复。

```
  [t=6370ms] FIXED-TIMEOUT  超过 250ms 无心跳 → 触发重选（⚠ 误判！只是延迟抖动）
  [t=6411ms] φ-ACCRUAL  延迟尖峰 300ms 到达，期间峰值 φ=4.30 < 8  →  判定为正常方差，不告警
  [t=7200ms] FIXED-TIMEOUT  超过 250ms 无心跳 → 触发重选（⚠ 误判！只是延迟抖动）
  [t=7221ms] φ-ACCRUAL  延迟尖峰 280ms 到达，期间峰值 φ=1.84 < 8  →  判定为正常方差，不告警
  [t=7950ms] FIXED-TIMEOUT  超过 250ms 无心跳 → 触发重选（真实故障）
  [t=8000ms] φ-ACCRUAL  断网持续中 φ=2.16
  [t=8100ms] φ-ACCRUAL  断网持续中 φ=6.64
  [t=8130ms] φ-ACCRUAL  φ=8.52 ≥ 8  →  租约回收（确认节点失效）
  [t=9198ms] φ-ACCRUAL  心跳恢复，租约重新发放

=== 结果汇总 ===
固定超时探测器: 触发重选 3 次（含抖动误判 2 次）
φ 自适应探测器: 触发租约回收 1 次（误判 0 次）
真实断网检测延迟: 固定超时 252ms / φ 自适应 432ms
```

## 测试结果（`npm test`）

```
✔ fixed timeout baseline: alive within timeout, dead beyond it
✔ normalCdf matches known standard normal values
✔ normalCdf shifts with mean and scales with stdDeviation
✔ cold start: phi is 0 before any heartbeat and stays low right after the first one
✔ steady healthy traffic keeps phi near zero and node available
✔ short jitter spike within historical variance does NOT trigger a false alarm
✔ real network partition drives phi past the threshold and revokes the lease exactly once
✔ phi grows monotonically as silence stretches during a partition
✔ heartbeats after a partition restore availability and re-issue the lease
✔ minStdDeviationMs floor prevents phi explosion when intervals are constant
✔ sampling window is bounded by maxSampleSize
ℹ tests 11  ℹ pass 11  ℹ fail 0
```

## 目录结构

```
src/
  phi-accrual-failure-detector.ts   核心探测器（φ 计算、阈值判定、租约事件）
  heartbeat-history.ts              滑动窗口采样（增量维护均值/方差）
  fixed-timeout-detector.ts         固定超时基线（用于对比）
  math.ts                           erfc / 正态分布 CDF 与生存函数
examples/demo.ts                    虚拟时钟对比仿真
test/                               node:test 确定性测试（全部使用虚拟时钟）
```
