# commission-dag

沿有向无环归属图（DAG）分账的佣金分配库。同一上级可经多条路径到达，但每笔交易只计一次；
支持月封顶、撤单更正，以及封顶释放后按原交易顺序补发被截部分；输出完整来源链并提供逐月金额守恒核对。

## 运行环境

Node.js ≥ 23.6（原生 TypeScript 类型擦除运行，无需编译）。代码仅使用可擦除语法，
如需 `tsc` 类型检查，仓库内 `tsconfig.json` 已开启 `erasableSyntaxOnly`。

```bash
npm test    # node --test 'test/*.test.ts'
npm run demo
```

## 核心概念

- **归属图**：边 `downline -> upline` 表示直接上级关系，边上费率 `rate ∈ (0,1]`，
  路径费率为各边乘积。加边时做环检测，保证 DAG。
- **多路径去重**：`enumerateChains` 枚举交易来源到每个上级的全部简单路径；
  同一上级每笔交易只计一次，计酬链取费率最高者（并列取路径字典序最小，保证确定性），
  全部路径仍保留在 `record.chains` 中作为完整来源链输出。
- **月封顶**：`addAgent(id, { monthlyCap })` 按自然月（`'YYYY-MM'`）限制净实付；
  超出部分截断为 `pendingCents` 进入待补发队列。
- **撤单更正**：`cancel(txId)` 对该笔交易生成负向分录——`reverse` 冲销实付、
  `void` 作废其待补部分——随后对释放了额度的月份按原交易顺序（`txSeq`）补发（`backpay`）
  其他交易被截的部分，可部分补发。历史流水只增不改。
- **金额守恒**：每条分账记录满足不变式
  `gross = paid + reversed + pending + voided`；
  `reconcile()` 按月汇总校验该等式并检查无人超封顶。

## API 摘要

```ts
const engine = new CommissionEngine();
engine.addAgent('B', { monthlyCap: 120 });   // 月封顶 120 元
engine.setUpline('A', 'B', 0.2);             // A 的上级是 B，边费率 0.2

engine.distribute({ txId: 'T1', source: 'A', amount: 1000, month: '2026-10' });
engine.cancel('T1', '退款');                  // 撤单更正 + 触发补发

engine.statement('B', '2026-10');            // 代理月度对账单（含来源链）
engine.reconcile();                          // 逐月金额守恒核对
engine.ledger();                             // 全部不可变流水
engine.allRecords();                         // 全部分账记录
```

金额内部一律以「分」为单位的整数运算（输入元按四舍五入转分），避免浮点误差。

## 文件结构

- `src/graph.ts` — DAG 归属图：加边环检测、来源链枚举
- `src/engine.ts` — 分配 / 撤单更正 / 封顶 / 补发 / 对账 / 守恒核对
- `src/types.ts` — 记录、流水、对账单等类型定义
- `test/engine.test.ts` — 8 个场景测试（node:test）
- `examples/demo.ts` — 端到端示例
