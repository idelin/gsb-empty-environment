# swim-deterministic-sim

离散事件网络模拟器 + SWIM 协议全链路确定性测试驱动（TypeScript 版）。

单进程内完整模拟虚拟交换机（丢包率、延迟抖动、硬分区）、离散时钟步进与
SWIM 失败检测状态机。整个仿真由**唯一种子随机数流**驱动：给定相同种子，
10000 个时钟步长内每一次丢包、每一次抖动、每一条 gossip 的顺序都逐比特
可复现——竞态 BUG 可以 100% 重现、调试与回归。

## 运行要求

- Node.js >= 22.6（使用 `--experimental-strip-types` 原生运行 TypeScript；
  Node >= 23.6 起类型擦除为默认行为）。无需任何第三方依赖、无需编译步骤。

```bash
npm test     # Node.js 内置测试器 (node:test)
npm run demo # 运行样例
```

## 架构

```
src/
  rng.ts          种子随机数 (mulberry32) + FNV-1a 日志指纹
  event-queue.ts  离散事件优先队列，(time, seq) 全序，打破并列确定性
  network.ts      虚拟交换机：per-link 丢包率 / 基础延迟 / 抖动 / 硬分区
  swim/node.ts    SWIM 状态机：ping / ack / ping-req、怀疑定时器、
                  incarnation 仲裁、gossip 传播、死节点重连探测
  simulator.ts    单进程离散时钟：每 tick 先投递到期帧（队列序），
                  再按节点 id 字典序执行协议 tick
test/
  rng.test.ts     随机数流确定性
  network.test.ts 丢包/抖动重放一致性、分区语义
  swim.test.ts    崩溃检测收敛、无误报、整段日志重放一致
  race.test.ts    验收场景：10000 步、固定种子、双节点互判崩溃竞态
examples/demo.ts  运行样例
```

## 确定性从何而来

1. **单一种子随机数流**：丢包判定、抖动、探测目标选择全部取自同一个
   `Rng(seed)`，调用顺序本身也是确定的。
2. **事件全序**：事件队列按 `(time, seq)` 排序；同一 tick 内先投递网络帧，
   再按节点 id 字典序执行协议逻辑——不存在任何依赖系统调度的顺序。
3. **虚拟时钟**：`step()` 推进一个离散 tick，与真实时间完全解耦。

## 仲裁规则（被测代码）

- 更新比较：incarnation 更大者胜；同 incarnation 时状态优先级
  `dead > suspect > alive`。
- 节点收到关于**自己**的 suspect/dead 且 incarnation >= 自身时，绝不接受：
  自增 incarnation 并广播 `alive` 反驳。分裂脑双方互相宣布对方死亡后，
  该规则保证收敛结果唯一（双方各反驳一次，incarnation 各 +1）。

## 验收场景（test/race.test.ts）

固定种子 `0x5eed`，精确 10000 个离散时钟步长：

| 时间 | 事件 |
| --- | --- |
| t=1..1000 | 5 节点健康运行（{A,B} 洁净链路；{C,D,E} 10% 丢包 + 抖动背景流量） |
| t=1000 | A<->B 硬分区（双向 100% 丢包） |
| t=1056 | **A 与 B 在同一 tick 同时宣布对方崩溃**（分裂脑竞态） |
| t=2000 | 分区恢复 |
| t=2101 | 双方收到"你已死亡"的 gossip，仲裁代码各反驳一次（inc 1->2） |
| t=2102 | 双方互相恢复 alive，集群收敛 |
| t=10000 | 断言终态 + 全量事件日志黄金哈希 `f64e616c` |

断言包括：互判崩溃发生在**同一 tick**；双方恰好各反驳一次；终态
incarnation 与成员视图精确匹配；同种子两次完整运行日志逐字节一致；
事件日志哈希等于固化的黄金指纹。

## 运行样例（npm run demo，节选）

```
=== Part 1: split-brain race (seed=0x5eed, 10000 ticks) ====
race events (A/B pair):
  t=1008 A suspect B inc=1
  t=1008 B suspect A inc=1
  t=1056 A declare-dead B inc=1
  t=1056 B declare-dead A inc=1
  t=2101 B refute dead inc=2
  t=2101 A refute dead inc=2
  t=2102 A alive B inc=2
  t=2102 B alive A inc=2

final membership views:
  A (inc=2) -> B:alive@2
  B (inc=2) -> A:alive@2

event log: 2058 lines, fnv1a=f64e616c

=== Part 2: replay proof (same seed, same hash) ============
run 1 hash: f64e616c
run 2 hash: f64e616c
IDENTICAL - fully deterministic

=== Part 3: crash detection (4 nodes, D crashes at t=500) ==
  t=516 A suspect D inc=1
  t=564 A declare-dead D inc=1
  A sees D as: dead
```

## 测试结果（npm test）

```
✔ split-brain race: A and B declare each other dead at the same tick
✔ arbitration converges deterministically to both-alive
✔ fixed seed reproduces the run bit-for-bit (10000 steps, twice)
✔ event log matches the recorded golden fingerprint
✔ cluster detects a crashed node and converges
✔ healthy cluster produces no false positives
✔ identical seeds replay the exact same event log
✔ loss and jitter replay identically from the same seed
... (共 15 项)
ℹ tests 15
ℹ pass 15
ℹ fail 0
```
