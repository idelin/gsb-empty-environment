# tick-drr-scheduler

离线网络模拟器用的整数 tick 驱动分级发包器（TypeScript，零依赖）。
每类流量（语音 / 交互 / 批量）配置一个字节令牌桶、最大排队字节数和 DRR 权重；
每 tick 给定链路可发送字节额度，包可跨 tick 发送，完成时只上报一次。

## 运行

代码只使用可擦除类型语法，Node ≥ 23.6 可直接运行 TypeScript（Node 22.6+ 需加
`--experimental-strip-types`）：

```sh
nvm use 23          # 或任何 >= 23.6 的 Node
npm test            # node --test（Node 23 自动发现 test/ 下的 .ts 测试）
npm run demo        # 运行 examples/demo.ts
```

## API

```ts
import { Scheduler } from './src/scheduler.ts';

const sched = new Scheduler({
  voice: { tokenRate: 30, tokenCap: 60, maxQueueBytes: 500, weight: 30 },
  inter: { tokenRate: 20, tokenCap: 40, maxQueueBytes: 500, weight: 20 },
  bulk:  { tokenRate: 50, tokenCap: 100, maxQueueBytes: 2500, weight: 10 },
});
sched.submit({ id: 'p1', arrivalTick: 0, bytes: 40, category: 'voice' });
sched.reconfigure(10, { bulk: { tokenRate: 50, tokenCap: 100, maxQueueBytes: 500, weight: 10 } });
const entry = sched.step(100);        // 推进一个 tick，额度 100 字节，返回本 tick 账本
sched.completions;                    // 完成事件（每包恰好一次）
sched.drops;                          // 丢包事件（含原因）
sched.duplicates;                     // 幂等重复事件
sched.ledger;                         // 每 tick 字节账本
```

丢包原因：`'queue-limit'`（整包入队超限）、`'conflict'`（同 ID 不同内容）、
`'unknown-category'`（未配置类别）。

## tick 内事件顺序

`step(budget)` 处理 tick `t` 时严格按以下顺序：

1. **重配置**：原子应用所有安排在 tick `t` 的类别配置（全部同时生效）；
2. **到达入队**：处理 `arrivalTick === t` 的包（按提交顺序），做去重 / 冲突 / 队限检查；
3. **令牌补充**：`tokens = min(tokenCap, tokens + tokenRate)`；
4. **亏欠累积**：对每个**非空**队列 `deficit += weight`（空队列亏欠恒为 0）；
5. **服务**：持久游标轮转发送，直到额度耗尽或没有可服务队列；
6. **记账**：写入本 tick 账本。

因此 tick `t` 到达的包在 tick `t` 即可被发送。

## 令牌与亏欠规则

- **初始令牌**：构造（或重配置新增类别）时为满桶 `tokens = tokenCap`。
- **可服务条件**：队列非空且 `tokens ≥ 1` 且 `deficit ≥ 1`。
- **亏欠更新**：每 tick 服务前，非空队列 `deficit += weight`；队列排空时 `deficit` 归零；
  每发送 1 字节消耗 1 令牌、1 亏欠、1 链路额度。
- **服务顺序**：类别按配置出现顺序排成环，游标持久（跨 tick 不清零）。每次访问
  可服务队列发送 1 字节后游标前移；当某队列是**唯一**可服务队列时，一次性发送
  `min(deficit, tokens, 队首剩余, 剩余额度)` 字节（与逐字节发送等价，见下）。

## 重配置语义

- 在指定 tick 的第 1 步原子生效，对 tick `t-1` 及以前无任何影响；
- **缩小 `maxQueueBytes` 不丢既有包**：队列内容原样保留，只在
  `queuedBytes + bytes > maxQueueBytes` 时拒绝**新**包（整包判断、整包拒绝）；
- 既有类别重配置时：`tokens` 截断到新 `tokenCap`，`deficit` 归零，权重/速率立即替换；
- 重配置可以引入新类别（满桶启动，追加到轮转环末尾）。

## 重复包 ID 语义

- 仅对**已接纳入队**的 ID 做幂等：同 ID 且字节数、类别一致 → 幂等，不重复入队、
  不改变任何状态，只记录一条 duplicate 事件；
- 同 ID 但内容不同 → 冲突丢包（`'conflict'`），保留原包；
- 因队限被拒的包**不**占用 ID，之后队列有空间时可重新提交。

## 正确性论证

记类别数 `n`，每 tick 链路额度 `B ≥ 1`。

**定理 1（链路额度安全）** 任意 tick 发送总字节数不超过 `B`。

*证明*：服务循环维护 `remaining`，初始为 `B`；循环条件 `remaining > 0`，每次发送量
`g = min(..., remaining) ≤ remaining`，发送后 `remaining -= g`。故发送总量
`B - remaining ≤ B`。实现中每 tick 记账前还有 `sentTotal > budget` 的断言兜底，
长期负载测试对每一行账本都校验该不等式。∎

**引理（合并发送与逐字节参考等价）** 主实现在“唯一可服务队列”处的一次性
`g` 字节发送，与逐字节参考实现（`src/reference.ts`，每次访问只发 1 字节）产生
完全相同的状态序列。

*证明*：在服务阶段内，一个队列的可服务性只取决于它自己的
`tokens / deficit / 队列非空`，而这些量只在**该队列自己发送**时减少；其他队列发送、
`remaining` 减少都不会让任何队列由不可服务变为可服务（令牌补充与亏欠累积只发生在
服务阶段之前）。因此当队列 `c` 是唯一可服务队列时，逐字节参考在接下来的
`g = min(deficit_c, tokens_c, 队首剩余, remaining)` 次循环里每次都只能再次选中 `c`，
且游标每次都停在 `c` 的下一位置——与一次性发送 `g` 字节、游标前移一位的结果
逐状态相同。`test/scheduler.test.ts` 用 150 个随机种子（含随机重配置、重复 ID、
冲突、未知类别、随机额度）对两个实现的完成事件、丢包事件和逐 tick 账本做
`deepStrictEqual` 全等校验。∎

**定理 2（无饿死：满足服务条件的队列在有界 tick 内必然推进）**
设队列 `c` 在 tick 区间 `[t0, t0+k)` 内始终非空，且 `weight_c ≥ 1`、
`tokenRate_c ≥ 1`、`tokenCap_c ≥ 1`。则 `c` 在该区间的前
`⌊(n−1)/B⌋ + 1` 个 tick 内至少发送 1 字节。

*证明*：先看 `c` 在整个区间内始终可服务。非空是假设；`c` 若不发送，其令牌只增不减，
每 tick 补充后 `tokens_c ≥ min(tokenCap, tokenRate) ≥ 1`；亏欠同理
`deficit_c ≥ weight_c ≥ 1`。

再看发送事件的全局顺序。游标持久且每次发送后严格前移一位，因此所有发送事件的位置
构成环上的轮转序列：在任意两次**同一队列** `d` 的发送事件之间，游标恰好完整扫过
每个类别位置一次。当游标扫到 `c` 时 `c` 可服务，故扫描序列在到达 `d` 之前必然先在
`c` 处产生一次发送（“唯一可服务时合并发送”不改变相对顺序，由引理）。于是：
**任意两次 `d` 的发送之间必有一次 `c` 的发送**。

反设 `c` 在整个区间发送 0 字节。则每个其他队列在整个区间至多发送 1 次
（否则两次 `d` 发送之间必有 `c` 的发送），其他队列合计至多 `n−1` 字节。另一方面，
`c` 始终可服务意味着服务循环每 tick 只能因额度耗尽而停止，即每 tick 恰好发送
`B` 字节，区间共 `k·B` 字节，全部来自其他队列。故 `k·B ≤ n−1`，即
`k ≤ ⌊(n−1)/B⌋`。所以 `c` 至迟在第 `⌊(n−1)/B⌋ + 1` 个 tick 发送首字节。∎

注意该界与权重无关：权重只决定长期**速率**（见下），而推进性由轮转结构本身保证——
低优先级队列不会被高优先级队列永久饿死。

**命题（长期速率受权重塑形）** 队列 `c` 在任意 `k` 个连续 tick 内发送量
`≤ k·min(weight_c, tokenRate_c) + max(deficit_c(t0), tokenCap_c)`；特别地，长期平均
速率 `≤ min(weight_c, tokenRate_c)` 字节/tick。当所有队列持续积压且
`B ≥ Σ weight` 时，每类每 tick 恰好发送 `weight_c` 字节（亏欠一到账就被发空），
链路按权重比例分配（测试“长期负载”对 4:2:1 权重验证了精确等式）。

*证明*：发送逐字节同时消耗亏欠与令牌，故 `k` tick 发送量既不超过亏欠累计
`deficit_c(t0) + k·weight_c`，也不超过令牌累计 `tokenCap_c + k·tokenRate_c`，
取两者较小者即得。∎

## 文件结构

- `src/scheduler.ts` — 主实现（含合并发送优化与额度断言）
- `src/reference.ts` — 逐字节参考实现（交叉核对用 oracle）
- `test/scheduler.test.ts` — node:test 测试：大包跨 tick、大小包混合、令牌不足、
  队列缩容、幂等/冲突、长期负载、无饿死界、重配置时点、150 种子随机对拍
- `examples/demo.ts` — 语音/交互/批量三类流量的运行示例，打印逐 tick 账本
