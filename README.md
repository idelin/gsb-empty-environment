# exact-window-stats

滑窗精确统计库（TypeScript）：维护 **sum / mean / variance**，全部基于 **BigInt 有理数**，
不经过 `Number` 中转，因此**不存在灾难消减**——大偏移 + 小方差（如 1e30 附近相差 1）依然精确。

## 特性

- **精确有理数**：内部以约分后的 `num/den`（BigInt）表示；输入支持十进制字符串（含科学计数法）与 `bigint`。
- **窗口增删**：`add(value)` 返回 id，`remove(id)` 按 id 删除；重复值各自持有独立 id。可选 `maxSize`，超容量自动驱逐最旧值。
- **undo 恢复**：`undo()` 按操作日志逆序回滚（含自动驱逐），精确恢复原统计；`snapshot()` 可保存/对比统计快照。
- **样本/总体区分**：样本方差分母 `n−1`（`n < 2` 时返回 `null`，未定义）；总体方差分母 `n`（`n = 0` 时返回 `null`）。
- **无灾难消减**：方差用精确公式 `t = n·Σx² − (Σx)²`，样本 `= t/(n(n−1))`，总体 `= t/n²`，全程 BigInt。
- **显式舍入**：`toDecimal(r, places, mode)`，mode ∈ `down | up | floor | ceil | half-up | half-even`，默认 `half-up`。

## 要求

Node.js ≥ 23.6（原生 TypeScript 类型擦除，无需编译/依赖）。

## 用法

```ts
import { ExactWindowStats } from './src/stats.ts';
import { toDecimal, toFraction } from './src/rational.ts';

const w = new ExactWindowStats({ maxSize: 3 });
w.add('1000000000000000000000000000000');
w.add('1000000000000000000000000000001');
w.add('1000000000000000000000000000002');

toFraction(w.sum());                  // '3000000000000000000000000000003'
toFraction(w.mean());                 // '1000000000000000000000000000001'
toFraction(w.variance('sample'));     // '1'      （大偏移小方差仍精确）
toFraction(w.variance('population')); // '2/3'
toDecimal(w.variance('population'), 4, 'half-even'); // '0.6667'

const id = w.add('5');  // 超容量，驱逐最旧值
w.remove(id);           // 按 id 删除
w.undo();               // 撤销 remove，恢复原统计
```

## 运行

```bash
npm test    # node --test（Node 内置测试，15 个用例）
npm run demo
```

## 运行样例（`npm run demo` 输出节选）

```
== 大偏移小方差（1e30 附近 ±1） ==
  sum: 3000000000000000000000000000003
  mean: 1000000000000000000000000000001
  sample variance: 1  ≈ 1.000000
  population variance: 2/3  ≈ 0.666667

== undo 恢复 ==
  三次 undo 后与之前快照一致: true

== n=1 样本方差未定义 ==
  sample variance: undefined
  population variance: 0

== 显式舍入规则（1/3 与 0.125，保留 2 位小数） ==
  down      1/3 → 0.33   0.125 → 0.12
  up        1/3 → 0.34   0.125 → 0.13
  half-up   1/3 → 0.33   0.125 → 0.13
  half-even 1/3 → 0.33   0.125 → 0.12
```

## 测试结果

```
ℹ tests 15
ℹ pass 15
ℹ fail 0
```

覆盖：十进制/科学计数法解析、0.1+0.2+0.3 精确求和、样本/总体方差区分、n=1 样本方差未定义、
1e30 附近相差 1（及 0.5）的方差、超出 `Number.MAX_SAFE_INTEGER` 的大数、重复值独立删除、
maxSize 驱逐、undo 恢复（add/remove/驱逐三种）、六种舍入规则（含负数与银行家舍入）。

## 文件

- `src/rational.ts` — BigInt 有理数：解析、四则运算、显式舍入格式化
- `src/stats.ts` — `ExactWindowStats` 滑窗统计
- `test/stats.test.ts` — Node 内置测试
- `examples/demo.ts` — 运行样例
