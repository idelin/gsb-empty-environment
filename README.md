# exact-calibration

精确十进制校准表的分段线性正向 / 逆向换算（TypeScript，零依赖）。

## 设计要点

- **精确算术**：所有校准值从十进制字符串直接解析为 BigInt 有理数（`src/rational.ts`），
  插值全程保持精确有理数运算，**大数绝不经过 Number 中转**（Number 仅用于有界的小数指数）。
- **非单调与平台段**：输出允许先升后降（同一目标可有多个逆解），允许相邻输出相等的平台段。
- **逆解完备**：`inverse` 返回全部离散解 `points` 与连续解区间 `intervals`（平台值逆解），
  共享节点去重，被区间覆盖的端点不再重复列为离散点。
- **冲突检测**：重复输入点输出相同则去重；输出不同则抛 `CalibrationConflictError`。
- **显式超范围策略**：每次调用必须显式指定。正向：`error` / `clamp` / `null`；
  逆向：`error` / `empty`。
- **显式舍入规则**：`half-up`（ ties 远离零）、`half-down`（ties 趋向零）、
  `half-even`（银行家舍入）、`floor`、`ceil`、`down`（截断）、`up`（远离零）。
- **量化保留误差界**：每个量化结果携带 `exact`（精确值，非终止小数以 `num/den` 表示）、
  `exactError`（精确误差）与 `errorBound`（最近舍入为 ½ ulp，定向舍入为 1 ulp）。

## 运行要求

Node.js ≥ 23.6（原生 TypeScript 类型擦除，无需编译；仓库附 `.nvmrc`）。

```sh
npm test      # node --test（Node 内置测试运行器）
npm run sample
```

## API 摘要

```ts
import { CalibrationTable } from './src/index.ts';

const table = new CalibrationTable([
  { x: '0',  y: '0'  },
  { x: '10', y: '10' },
  { x: '15', y: '10' },   // 平台段
  { x: '25', y: '0'  },   // 下降段（非单调）
]);

// 正向：input -> 量化输出 + 误差界
table.forward('12', { scale: 4, rounding: 'half-up', outOfRange: 'error' });
// { value: '10.0000', exact: '10', exactError: '0', errorBound: '0.00005', clamped: false, ... }

// 逆向：target -> 全部解（离散点 + 区间）
table.inverse('5',  { scale: 4, rounding: 'half-up', outOfRange: 'error' });
// { points: [5.0000, 20.0000], intervals: [] }
table.inverse('10', { scale: 4, rounding: 'half-up', outOfRange: 'error' });
// { points: [], intervals: [{ from: 10.0000, to: 15.0000 }] }
```

## 运行样例（`npm run sample`）

```
=== forward conversion (scale=4, half-up) ===
x=5 -> y                           5.0000  (exact 5, |err| <= 0.00005)
x=12 -> y                          10.0000  (exact 10, |err| <= 0.00005)
x=20 -> y                          5.0000  (exact 5, |err| <= 0.00005)

=== non-terminating exact value keeps its error bound ===
x=1 on (0,0)-(3,1)                 value 0.33, exact 1/3, exactError 1/300, bound 0.005

=== inverse: rise-then-fall gives two solutions ===
y=5 -> x points                    5.0000, 20.0000

=== inverse: plateau value gives an interval ===
y=10 -> x in                       [10.0000, 15.0000]

=== explicit out-of-range policies ===
forward(99, 'error')               OutOfRangeError: input x=99 is outside the calibration domain [0, 25]
forward(99, 'clamp')               0.0000 (clamped)
forward(99, 'null')                null
inverse(42, 'empty')               {"target":"42","points":[],"intervals":[]}

=== big integers never pass through Number ===
forward(2^53+1)                    0.50000000000000005551
exact                              0.500000000000000055511151231257827021181583404541015625
error bound                        0.000000000000000000005

=== conflicting duplicate inputs are rejected ===
duplicate x=1                      conflicting calibration outputs for input x=1: 10 vs 11
```

## 测试结果（`npm test`，node:test）

```
✔ forward interpolates linearly and exactly
✔ forward keeps non-terminating results exact and bounded after quantization
✔ forward supports non-monotonic outputs and plateaus
✔ forward out-of-range policies are explicit
✔ duplicate inputs with different outputs are a conflict
✔ duplicate inputs with identical outputs are de-duplicated
✔ inverse of a rise-then-fall table returns both solutions
✔ inverse of a plateau value returns a continuous interval
✔ inverse quantizes solutions and keeps error bounds
✔ inverse out-of-range policies are explicit
✔ huge values never pass through Number
✔ round-trip: forward then inverse recovers the input within the error bound
✔ parse keeps integers beyond Number.MAX_SAFE_INTEGER exact
✔ parse handles fractions, signs and exponents exactly
✔ non-terminating values render as exact fractions
✔ quantize half-up rounds ties away from zero
✔ quantize half-even rounds ties to even
✔ quantize half-down rounds ties toward zero
✔ quantize directed modes
✔ quantization reports exact error and a valid error bound
✔ directed rounding has a full-ulp error bound
✔ negative scales quantize to tens, hundreds, ...
✔ exact values quantize with zero error
ℹ tests 23
ℹ pass 23
ℹ fail 0
```

## 目录结构

- `src/rational.ts` — BigInt 精确有理数与十进制解析/格式化
- `src/decimal.ts` — 量化：显式舍入规则 + 误差界
- `src/calibration.ts` — 校准表：正向/逆向分段线性换算、冲突检测、超范围策略
- `test/` — Node 内置 `node:test` 测试（23 个用例）
- `examples/sample.ts` — 运行样例
