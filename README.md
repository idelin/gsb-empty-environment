# 探活模拟器（liveness-probe-sim）

离散事件驱动的探活/故障检测仿真器。监控器在**漏收连续心跳后先怀疑节点**，
**收到当前代次（epoch）证据则恢复**，**超过自适应等待窗口才移除**。
误判（误疑/误删）与检测延迟作为**相互独立的指标**分别报告，不合成单一分数。

## 运行

需要 Node.js ≥ 23.6（原生支持运行 TypeScript；22.6+ 可加
`--experimental-strip-types` 运行）。无第三方依赖。

```bash
node src/main.ts                  # 运行全部场景并输出报告
node examples/adaptive-vs-fixed.ts # 自适应窗口 vs 固定窗口对比
node --test                       # 运行测试
```

## 模型

**离散事件**：`send-heartbeat` / `deliver-heartbeat` / `evaluate` /
`pause-node` / `resume-node` / `crash-node` / `restart-node`，
由最小堆按时间排序弹出（`src/heap.ts`、`src/sim.ts`）。

**状态机**（`src/monitor.ts`）：

- `ALIVE`：连续漏收达到 `suspectThreshold` 个心跳 → `SUSPECTED`；
- `SUSPECTED`：收到当前代次心跳 → 恢复 `ALIVE`；超过自适应等待窗口 → `REMOVED`；
- `REMOVED`：只有**比移除决定更新**的证据（`sentAt > removedAt`）或**更高代次**
  的心跳才能使其重加入；旧心跳（更小 epoch，或移除前发出的同代次心跳）一律忽略。

**自适应等待窗口**（历史样本有限）：每个节点维护容量固定（默认 16）的
单程延迟样本环形缓冲（`src/window.ts`），

```
suspectTimeout = clamp(heartbeatInterval + mean + k·stddev, min, max)
```

延迟尾部变重时窗口自动放宽，网络恢复后随旧样本被淘汰而收紧。

## 指标（独立报告）

- **误判**：`误疑`（节点实际存活却被怀疑）、`误删`（节点实际存活却被移除）；
- **检测延迟**：真实崩溃 → 被移除的时间差，逐次记录并给出平均/最大；
  仿真结束仍未检出的记为 `漏检`。

## 场景（`src/scenarios.ts`）

- `baseline`：小抖动无故障，验证零误判；
- `pause-bursts`：50ms / 250ms / 2000ms 暂停，分别对应不怀疑、怀疑后恢复、误删后重加入；
- `long-tail-delay`：间歇性拥塞造成的长尾延迟，考验自适应窗口；
- `stale-heartbeats`：崩溃重启（代次+1）后旧代次心跳迟到、永久崩溃后崩溃前心跳迟到，均不得复活节点；
- `real-crash`：永久崩溃，测量检测延迟。

## 测试

`test/` 覆盖堆、样本窗口、检测器状态机（怀疑/恢复/移除/旧心跳/自适应窗口）
与五个场景的端到端指标断言。
