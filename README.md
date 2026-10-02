# Global Read-View Aligner（分布式事务全局一致性读取对齐器）

分库分表集群的全局快照读取器。各分片产生独立的本地提交时间戳（LSN），
对齐器协调各分片的 CDC 流，计算全局因果闭合的安全时间戳 `T_Safe`，
确保跨分片读取看到完全自洽的数据集——跨片转账的转入与转出要么同时可见，要么同时不可见。

## 核心模型

每个分片输出一条**按 LSN 严格递增**的 CDC 流，事件类型：

| 事件 | 含义 |
| --- | --- |
| `local-commit` | 单分片事务提交，携带账户变更操作 |
| `participant-commit` | 跨分片事务的单个参与方提交腿，携带全局 `txnId` 与全部参与方列表 |
| `heartbeat` | 水位线推进：本分片不会再产生 LSN ≤ 该值的提交 |

## 安全时间戳算法

```
T_raw  = min(各分片水位线)                    -- 所有分片都已确认到此点
T_Safe = min(T_raw, min(未闭合事务的首腿 LSN) - 1)
```

- **未闭合事务**：至少一条参与腿已到达、但参与方未到齐的跨分片事务。
  其首腿 LSN 之前的快照一定看不到该事务的任何部分，因此 `T_Safe` 被钉在 `首腿LSN - 1`。
- **快照原子可见性**：已闭合事务仅当 `max(各腿LSN) ≤ T_Safe` 才整体应用；
  单分片事务按 `LSN ≤ T_Safe` 应用。任何时刻 `T_Safe` 处都不存在"半可见"事务。

## 查询门禁

- `snapshot()`：默认严格模式。存在未闭合跨片事务（或某分片尚未上报水位线）时
  抛出 `SnapshotNotReadyError`，**阻止外部生成查询视图**。
- `snapshot({ allowStale: true })`：放宽模式，返回 `T_Safe` 处的历史一致视图
  （例如转账前的状态），同样保证因果闭合、绝不出现半 applied 事务。
- `waitForSnapshot()`：异步等待，因果闭合达成后兑现最新一致快照。
- `status()`：水位线、未闭合事务、当前 `T_Safe` 等可观测状态。

## 不变式

**资金守恒**：任何可对外输出的快照中，账户总额恒等于初始总额——钱不会凭空消失或产生。
由随机交错测试（50 个种子 × 20 笔并发转账的任意到达顺序）持续验证。

## 运行

无需任何依赖（Node ≥ 22.6，原生 TypeScript 类型擦除）：

```bash
npm test     # node --test 'test/*.test.ts'
npm run demo # node src/demo.ts
```

## 文件结构

- `src/types.ts` — CDC 事件与领域类型
- `src/aligner.ts` — 对齐器核心：水位线跟踪、因果闭合、快照构建、查询门禁
- `src/demo.ts` — 运行样例：alice → bob 跨片转账 50 元的完整时序
- `test/aligner.test.ts` — Node 内置测试运行器（`node:test`）验证全部验收边界
