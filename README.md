# 分布式滑动窗口限流：心跳租约 + 配额预分配（TypeScript）

中心协调器（Master）向各工作节点（Worker）分发限流配额。Worker 本地优先消耗
预分配的 Local Token，余额低于阈值时**异步批量心跳续订**，把 RPC 往返次数从
“每请求一次”降到“每批量一次”。空闲池不足时，Master 按“用量最少者优先”
从其他 Worker 未使用的预分配额度中**调拨**给突发节点。

零依赖：Node.js >= 23.6 原生运行 TypeScript（type stripping），测试使用
Node 内置 `node:test`。

## 架构

```
┌────────────┐  heartbeat(usedTotal, requested)  ┌──────────────────┐
│ WorkerNode │ ───────────────────────────────▶ │ MasterCoordinator │
│ 本地令牌桶 │ ◀─────────────────────────────── │ 滑动窗口 + 租约   │
└────────────┘  {allocated, expiresAt, window}  │ 空闲回收/调拨     │
                                                └──────────────────┘
```

- `src/master.ts` — 中心协调器：滑动窗口（默认 1s）每窗口重置 3000 QPS 配额池；
  心跳时先从未分配池划拨，不足则按“已上报用量最少者优先”回收其他 Worker 的
  未使用余额并调拨；租约有效期 = 当前窗口结束时间。
- `src/worker.ts` — 工作节点：`tryConsume()` 纯本地扣减（无 RPC）；余额
  `<= renewThreshold` 时触发异步批量续订（单飞，不并发重复心跳）；响应中的
  `allocated` 是 Master 权威值，Worker 用 `allocated - usedTotal` 重算本地余额，
  被调拨走的额度在下一次心跳自动收敛。
- `src/transport.ts` — 进程内模拟 RPC 通道，统计 RPC 次数，可注入延迟；
  替换为 HTTP/gRPC 时保持 `heartbeat()` 签名即可。
- `src/clock.ts` — 时钟抽象，`ManualClock` 保证测试/演示确定性。

## 运行

```bash
npm run demo   # 验收场景演示
npm test       # node:test 内置测试
```

## 验收场景（src/demo.ts）

3 个 Worker 平分 3000 QPS（各预分配 1000）；B 半忙（用 500），C 空闲；
A 突发流量耗尽自身 1000 后，批量心跳从 C 调拨 1000、再从 B 的未使用余额
调拨 500，最终 A 消费 2500 tokens，全程仅约 14 次 RPC。

运行样例（节选）：

```
=== Phase 1: 注册，3 个 Worker 平分 3000 QPS 配额池 ===
[master] 授予 1000 tokens -> worker-A (持有=1000, 池剩余=2000)
[master] 授予 1000 tokens -> worker-B (持有=1000, 池剩余=1000)
[master] 授予 1000 tokens -> worker-C (持有=1000, 池剩余=0)

=== Phase 3: Worker A 突发流量，耗尽自身配额后触发批量续订/调拨 ===
[master] 调拨 1000 tokens: worker-C (空闲, 已用=0) -> worker-A
[master] 授予 1000 tokens -> worker-A (持有=2000, 池剩余=0)
[master] 调拨 500 tokens: worker-B (空闲, 已用=500) -> worker-A
[master] 授予 500 tokens -> worker-A (持有=2500, 池剩余=0)
worker-A 突发共消费 2500 tokens（自身初始配额仅 1000）
worker-C 下一次心跳后收敛：本地余额=0（被调拨的额度自动失效）

=== Phase 4: 滑动窗口推进 1s，租约过期，配额池重置 ===
[master] 窗口滑动 -> [1000, 2000)，配额池重置为 3000
总计：worker-A 消费 2500 tokens，全程 RPC 仅 14 次
```

## 测试结果

```
✔ 验收：3 Worker 平分 3000 QPS，A 突发耗尽后从空闲的 C 批量调拨
✔ Master 将配额池平分给 3 个 Worker
✔ 配额池不足时授予部分额度
✔ 空闲池不足时优先从用量最少的空闲 Worker 调拨
✔ 滑动窗口推进后租约失效、配额池重置
✔ 本地消耗 Local Token 不产生额外 RPC
✔ 余额低于阈值时异步批量续订，RPC 次数远小于消费次数
✔ Master 无额度可授时 tryConsume 快速失败
✔ 租约随窗口过期后，本地余额作废并重新批量注册
ℹ tests 9  ℹ pass 9  ℹ fail 0
```

## 设计说明

- **降低 RPC 频率**：消费完全本地化；仅注册、阈值续订、窗口切换时心跳，
  且同一时刻至多一个 in-flight 心跳（单飞去重）。
- **总量守恒**：Master 保证 `Σ allocated <= pool`，调拨只是存量转移，不超发。
- **最终一致**：被调拨方在下次心跳收到权威 `allocated` 后收敛；窗口边界
  由 `windowStart` 字段同步，租约过期即本地余额作废。
