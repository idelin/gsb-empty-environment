# exact-window-stats

基于 **BigInt 精确有理数** 的滑动窗口统计库（TypeScript）：维护 sum / mean / 方差，
支持窗口增删、重复值、大偏移小方差场景，**全程不经过 Number 中转大数**，无灾难消减。

## 运行环境

- Node.js ≥ 23.6（原生运行 TypeScript，开箱即用）
- 或 Node.js ≥ 22.6，运行时加 `--experimental-strip-types`

## 快速开始

```bash
npm test        # node --test "test/**/*.test.ts"
npm run example # node examples/demo.ts
```

## 设计

- `src/rational.ts` — 精确有理数 `Rational`：BigInt 分子/分母，自动约分、符号归一；
  十进制字符串（含科学计数法）**精确解析**；`toDecimalString(places, mode)` 按显式
  舍入规则在 BigInt 上完成舍入（比较 `2*余数` 与分母），无浮点误差。
- `src/stats.ts` — `WindowStats`：只维护精确量 `n`、`Σx`、`Σx²` 与多重集合
  （值 → 次数）。方差按 `(Σx² − (Σx)²/n) / (n−1 | n)` 精确计算——消减只发生在
  精确算术中，因此 10³⁰ 附近相差 1 的样本方差仍然精确（浮点实现会在这里丢光精度）。
  删除即精确回滚 `Σx`/`Σx²`，统计量恢复到添加前状态。

## API 摘要

```ts
const w = new WindowStats();
w.add('1000000000000000000000000000000').add('1000000000000000000000000000001');
w.count;                    // 2
w.sum().toString();         // '2000000000000000000000000000001'
w.mean().toString();        // '2000000000000000000000000000001/2'
w.sampleVariance()!.toString();    // '1/2'  —— 大数附近相差 1，样本方差精确
w.populationVariance()!.toString();// '1/4'
w.remove('1000000000000000000000000000000'); // 撤回一个值
w.sampleVariance();         // null —— n=1 时样本方差未定义
```

- 输入：`string`（精确十进制，推荐）/ `bigint` / `Rational` / 安全整数 `number`
  （非安全整数直接抛错，杜绝经 Number 丢精度）。
- `variance('sample')` 除以 `n−1`（n<2 返回 `null`）；`variance('population')`
  除以 `n`（n<1 返回 `null`）。空窗口 `mean()` 抛 `RangeError`。
- `remove(v)` 移除一次出现；值不存在抛 `RangeError`。
- `standardDeviation(kind, places, mode)`：用整数平方根在 BigInt 上精确判定舍入。
- `snapshot(places, mode)`：一次性输出全部统计量的格式化字符串。

## 舍入规则（`RoundingMode`，默认 `half-even`）

| 模式 | 含义 |
| --- | --- |
| `half-even` | 平局向偶数（银行家舍入） |
| `half-up` / `half-down` | 平局远离零 / 向零 |
| `down` / `up` | 向零截断 / 远离零进位 |
| `floor` / `ceil` | 向负无穷 / 向正无穷 |

舍入到 0 时不输出负号（无 `-0.00`）。
