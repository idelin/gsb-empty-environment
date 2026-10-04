# exact-interval-affine

TypeScript（Node.js ≥ 23 原生运行，零依赖）实现的精确十进制区间 / 仿射算术库。
所有大数运算基于 BigInt，**不经过 Number 中转**；所有可能不精确的运算都显式指定
精度（有效十进制位数）与舍入方向。

## 特性

- **精确十进制 `Dec`**：`m × 10^e`，BigInt 尾数。加减乘精确；除法/舍入显式指定
  `floor`（向 -∞）、`ceil`（向 +∞）、`half-even`。
- **区间 `Iv`**：向外舍入到指定精度（下界 floor、上界 ceil）。`empty`（空区间）与
  `unknown`（全直线）严格区分；支持交（`intersect`）、并/包围（`hull`）、包含判断
  （`containsValue` / `containsInterval`）。负系数缩放自动翻转边界。
- **跨零除法**：区间层直接拒绝（`ZeroDivisionError`）；`Domain.div` 支持
  `mode: 'reject' | 'split'`，split 模式在零点分裂定义域产生多段（射线）。
  段数超过 `maxParts` 时保守包围（hull）并置 `precisionLoss` 及说明。
- **仿射形式 `Affine`**：`c0 + Σ ci·εi`，噪声符号共享——同一不确定输入重复出现时
  相关性保留，线性运算精确（`x - x = 0`，无虚假扩大）。非线性步骤（乘、倒数/除法）
  把保守余项挂到新噪声符号上，并记录**余项证书**。
- **独立证书检查 `Cert`**：用精确有理数（`Rat`）独立重算，验证向外舍入方向、
  包含性与有效位数；并通过**精确有理反例搜索**（网格采样真值必须落在界内）辅助验证。

## 运行

```sh
npm test     # node --test（Node 内置测试运行器）
npm run demo # 运行样例
```

## 样例输出（`npm run demo`）

```
=== 1. Exact decimals, explicit directed rounding (prec = 10) ===
1/3  floor:                                                0.3333333333
1/3  ceil :                                                0.3333333334
-1/3 floor:                                                -0.3333333334
-1/3 ceil :                                                -0.3333333333
40-digit mantissa + 1 (exact, no Number):                  10000000000000000000000000000000000000000

=== 2. Interval arithmetic with outward rounding ===
[1,1] / [3,3]  (prec 3):                                   [0.333, 0.334]
contains exact 1/3?                                        true
[-2,3] * [4,5]:                                            [-10, 15]
[2,3] scaled by -1 (bounds flip):                          [-3, -2]
empty vs unknown:                                          ∅  vs  (-∞, ∞)

=== 3. Affine forms: correlated symbols, no false widening ===
x - x (same symbol):                                       [0, 0]
(x + 1) - x:                                               [1, 1]
same expression with plain intervals:                      [0, 2]

=== 4. Nonlinear steps emit conservative remainder certificates ===
t^2 - t  affine bounds:                                    [-0.5, 2]
certificate mul (sym 1), remainder 0.25:                   VERIFIED — mul: remainder 0.25 vs required 0.25
21 exact rational samples of t^2-t inside bounds?          true

=== 5. Division across zero: split or reject ===
1 / [-2,3] split into parts:                               [-∞, -0.5]  ∪  [0.3333333333, ∞]
precisionLoss?                                             false
reject mode:                                               ZeroDivisionError: Domain.div: denominator crosses zero (reject mode)
cap=1 forces hull:                                         (-∞, ∞)
precisionLoss note:                                        hulled 2 parts into 1 (cap 1); precision lost

=== 6. Independent checker: outward direction + counterexample search ===
checkBinaryOp(mul, [1,2], [1,2], correct):                 true
checkBinaryOp(..., shrunk [1.5,4]):                        ok=false, loOutward=false
counterexample found:                                      point (1, 1) -> 1
```

## 测试结果（`npm test`）

```
ℹ tests 30
ℹ pass 30
ℹ fail 0
```

覆盖：十进制定向舍入（含 half-even  ties、大尾数精确性）、区间向外舍入/空与未知区分/
跨零除法拒绝与射线、仿射相关符号与线性不扩大、非线性余项证书验证、采样真值落在界内、
多段定义域分裂/拒绝/超上限保守包围、独立检查器对篡改结果的反例搜索。

## 代码结构

- `src/decimal.ts` — 精确十进制 `Dec`（BigInt 尾数，显式舍入规则）
- `src/rational.ts` — 精确有理数 `Rat`（检查器与精确采样用）
- `src/interval.ts` — 区间：empty/unknown/range、向外舍入运算、跨零除法拒绝
- `src/affine.ts` — 仿射形式：相关噪声符号、非线性余项证书
- `src/multi.ts` — 多段定义域：跨零分裂、段数上限与保守包围
- `src/certificate.ts` — 独立检查器：舍入方向/包含性/精度 + 有理反例搜索
- `test/*.test.ts` — Node 内置测试（`node:test`）
- `examples/demo.ts` — 可运行样例
