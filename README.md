# commission-dag

沿**有向无环归属图**分账的佣金分配库（TypeScript，零依赖）。

## 核心规则

- **DAG 归属图**：边 `from -> to` 表示「to 是 from 的直接上级」，佣金沿边向上传导，
  一条链的有效费率为沿途各边费率（万分比）的连乘。加边时拒绝成环。
- **多路径去重**：同一上级可经多条路径到达，但每笔交易对其只计一次——
  取有效费率最高的链（费率相同取字典序最小链，保证确定性）。
- **月封顶**：每个接收人按月独立封顶（可配默认值与单人覆盖）。
  超出部分被截断，进入待补发队列。
- **撤单更正**：`reverseTransaction` 冲回该单全部已发金额（负向流水），
  作废该单自己的待补发部分，释放其占用的封顶额度。
- **补发**：封顶释放后，按**原始交易顺序**（提交序号升序）补发被截部分，
  额度不足时部分补发、剩余继续排队。
- **逐月守恒**：对每个（接收人 × 月份）恒有 `应发 = 实发净额 + 待补发`，
  `monthlyReport` / `verifyConservation` 可校验。

金额一律用最小货币单位的整数表示，佣金向下取整（floor），费率连乘用
bigint 精确计算，守恒关系不受舍入影响。账簿只增不删（`PAYOUT` / `REVERSAL` / `REISSUE`）。

## 用法

```ts
import { CommissionEngine } from './src/engine.ts';

const engine = new CommissionEngine();
engine.registerAgent('alice');
engine.registerAgent('bob', { monthlyCap: 400 }); // bob 每月封顶 400
engine.registerAgent('carol');

engine.addAttributionEdge('alice', 'bob', 500);   // alice 的上级 bob，5%
engine.addAttributionEdge('alice', 'carol', 300); // alice 的上级 carol，3%
engine.addAttributionEdge('carol', 'bob', 1000);  // carol 的上级 bob，10%

// bob 经两条路径到达（直连 5% vs 经 carol 0.3%），只按 5% 计一次
engine.submitTransaction({ id: 'tx1', agentId: 'alice', amount: 10000, month: '2026-09' });
engine.submitTransaction({ id: 'tx2', agentId: 'alice', amount: 10000, month: '2026-09' });

engine.explainTransaction('tx1');   // 完整来源链：全部候选链 + 去重选中结果
engine.getPending('2026-09');       // 被截断待补发队列（按原交易顺序）

engine.reverseTransaction('tx1');   // 撤单：冲回 tx1、释放额度、按序补发 tx2

const report = engine.monthlyReport('2026-09'); // 逐月金额守恒报告
console.assert(report.balanced);
```

## API 一览

| 方法 | 说明 |
| --- | --- |
| `registerAgent(id, { monthlyCap? })` | 注册代理人，可配月度封顶（默认无限） |
| `addAttributionEdge(from, to, rateBps)` | 加归属边，校验环与重复边 |
| `submitTransaction({ id, agentId, amount, month })` | 分账并落账，返回本单流水 |
| `reverseTransaction(id, note?)` | 撤单更正：冲回 + 释放封顶 + 按序补发 |
| `explainTransaction(id)` | 该单全部来源链、应发额与去重选中标记 |
| `getLedger()` / `getPending(month?)` | 只增账簿 / 待补发队列 |
| `monthlyReport(month)` | 逐接收人 `due / issued / pending / reversed` 与守恒校验 |
| `verifyConservation()` | 全部月份守恒总校验 |

## 运行测试

需要 Node ≥ 23.6（原生运行 TypeScript）：

```sh
npm test
```
