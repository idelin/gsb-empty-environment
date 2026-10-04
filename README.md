# tick-scheduler

离线网络模拟器用的整数 tick 驱动分级发包器（TypeScript，Node ≥ 22.6 直接运行，无需编译）。
三类流量：`voice` / `interactive` / `bulk`。每类一个字节令牌桶 + 最大排队字节数 + DRR 权重；
链路每 tick 有固定字节额度，包可跨 tick 发送，完成时只上报一次。

## 运行

```sh
npm test        # node --test（Node 23 原生剥离类型，无需 tsc）
npm run demo    # 打印逐 tick 账本、完成/丢包事件与总量核对
```

## API 速览

```ts
import { Scheduler } from './src/scheduler.ts';

const sim = new Scheduler(linkBytesPerTick, {
  voice:       { tokenRatePerTick, tokenBurstBytes, maxQueueBytes, weight },
  interactive: { ... },
  bulk:        { ... },
}, quantumUnit /* 缺省 256，quantum = weight * quantumUnit */);

sim.submit({ id, arrivalTick, bytes, className });        // 缓冲到达（不得早于当前 tick）
sim.scheduleReconfig({ tick, className, patch });         // 在指定 tick 开始原子生效
const ledger = sim.step();                                // 推进一个 tick，返回账本
sim.run(n); sim.runUntilIdle(maxTicks);
```

每个 `TickLedger` 包含：本 tick 各类/总发送字节、发送后令牌/亏欠/排队字节、
完成记录（含排队时延）与丢包记录（`queue_overflow` / `id_conflict`）。

## Tick 内事件顺序（tick t）

1. **重配置原子生效**：应用所有 `tick === t` 的重配置。缩小 `maxQueueBytes`
   不丢既有包，只在第 3 步用新上限拒绝新包；`tokenBurstBytes` 缩小时令牌立即截断；
   权重变化时该类亏欠清零（旧亏欠失效）。
2. **令牌补充**：`tokens = min(tokenBurstBytes, tokens + tokenRatePerTick)`。
3. **到达入队**（按提交顺序）：
   - 幂等检查：包 ID 的指纹为 `(className, bytes, arrivalTick)`。指纹相同 → 静默忽略；
     不同 → 丢 `id_conflict`。指纹仅在**成功入队**时登记，因此被队列上限拒绝的包
     之后可以同 ID 重传。
   - 整包准入：`queuedBytes + bytes > maxQueueBytes` → 整包丢 `queue_overflow`
     （不允许部分入队）。
4. **DRR 服务循环**（见下）。
5. **生成本 tick 账本**，tick 计数 +1。

## 初始状态与亏欠规则

- 初始令牌 = `tokenBurstBytes`（满桶），初始亏欠 = 0。
- 服务循环按轮（round）进行，每轮从轮转游标开始扫描三类（游标每 tick 前进一格，
  避免固定某一类永远先服务）。访问队列 q 时：
  - q 为空 → `deficit[q] = 0`（DRR 标准规则：空队列放弃亏欠），跳过；
  - `tokens[q] == 0` → 跳过，**不累积** quantum（防止亏欠无限增长）；
  - 否则 `deficit[q] += weight[q] * quantumUnit`，发送
    `grant = min(deficit, tokens, 链路剩余额度, 队首包剩余)` 字节，
    四处同步扣减。
- 队首包剩余归零即出队并上报完成（出队即销毁，天然只上报一次）；
  出队后队列变空则亏欠清零，否则亏欠保留给下一个包。
- 一轮内没有任何字节发送（全空或全部令牌耗尽）或链路额度耗尽时循环结束。

## 正确性论证

**容量界（安全性）。** 每 tick 发送总量不超过链路额度：发送只发生在服务循环内，
每发 `grant` 字节都从 `remaining`（初值 = `linkBytesPerTick`）中扣除，且
`grant = min(..., remaining, ...)` 保证 `remaining` 单调不增且不为负，循环在
`remaining == 0` 时终止。同理每类发送量 ≤ 该类可用令牌（`grant ≤ tokens`），
满足令牌桶约束。测试 `assertLedgerInvariants` 在所有场景逐 tick 断言这两条。

**无饿死（进展性）。** 设队列 q 非空。令牌桶每 tick 补充 `tokenRatePerTick > 0`，
故存在某个 tick 使 `tokens[q] > 0`。在该 tick 的服务循环中，只要链路额度未耗尽，
q 每被访问一轮就获得 `deficit += weight*quantumUnit ≥ quantumUnit ≥ 1`，于是
`grant = min(deficit, tokens, remaining, headRemaining) ≥ 1`：q 每轮至少推进 1 字节。
又因为 q 非空时亏欠不会因"空队列清零"规则被抹掉，且 DRR 轮转访问**所有**非空队列
（不存在严格优先级抢占），低权重类只是分得比例小，而不会被永久阻塞。
若各类长期到达率分别低于 `tokenRatePerTick` 且总到达率低于链路额度，则队列有界并最终
排空——长期负载测试（4000 tick 随机负载后 `runUntilIdle`）验证了所有被接纳的包都完成、
字节守恒（`admitted == sent + queued`）且完成恰好一次。

**逐字节参考核对。** `src/reference.ts` 是同一语义的刻意朴素实现：发送路径逐字节
循环并逐字节重查全部约束。混合大小包、令牌受限、中途重配置的随机工作负载下，
主实现（按块发送）与参考实现 400 个 tick 的账本逐字段 `deepEqual` 一致。

## 文件

- `src/scheduler.ts` — 调度器实现
- `src/reference.ts` — 逐字节参考模拟（交叉核对用）
- `test/scheduler.test.ts` — node:test 测试（大包跨 tick、大小包混合、令牌不足、
  队列缩容、幂等/冲突、DRR 权重公平、长期负载、不变量）
- `examples/demo.ts` — 可运行示例，打印逐 tick 账本
