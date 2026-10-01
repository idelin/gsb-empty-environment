# exact-decimal

TypeScript 精确十进制运算库：以 **BigInt 系数 + 十进制指数** 为基础，大数不经过 `Number` 中转，舍入规则显式，上下文（精度 + 舍入模式）相互独立，错误标志保留在各自上下文中。运行于 Node.js 原生 TypeScript（类型擦除），测试使用 Node 内置测试运行器（`node:test`），零第三方依赖。

## 数据模型

`value = coeff × 10^exp`，其中 `coeff` 为任意精度 `BigInt`（带符号），`exp` 为整数指数。特殊值 `NaN` / `±Infinity` 单独表示。大整数直接以字符串或 `bigint` 构造，全程不经过 `Number`。

## 功能

- 四则运算：`add` / `sub` / `mul` / `div`，以及 `quantize(exp)` 量化
- 8 种显式舍入模式（`Rounding`）：`HALF_EVEN`、`HALF_UP`、`HALF_DOWN`、`DOWN`（向零）、`UP`（远离零）、`CEILING`、`FLOOR`、`05UP`
- 上下文独立：每个 `Context` 自带 `precision`、`rounding` 与 sticky 标志集
- 错误标志（`Flag`）：`DivisionByZero`、`InvalidOperation`、`Inexact`、`Rounded`，累积保留直到 `clearFlags()`
- 除零（`DivisionByZero` → ±Infinity）与精度不足（`Inexact` → 舍入结果）严格区分；`0/0` 为 `InvalidOperation` → NaN

## 使用

```ts
import { d } from './src/decimal.ts';
import { Context } from './src/context.ts';
import { Rounding } from './src/rounding.ts';

d('0.1').add('0.2').toString();             // '0.3'（精确）
d('9007199254740993').add('1').toString();  // '9007199254740994'（超过 2^53）

const ctx = new Context({ precision: 10, rounding: Rounding.HALF_UP });
d('1').div('3', ctx).toString();            // '0.3333333333'
ctx.flags;                                  // Set { 'Rounded', 'Inexact' }

d('2.005').quantize(-2, ctx).toString();    // '2.01'（量化到分）
```

## 运行

```sh
npm test    # node --test（Node 内置测试运行器）
npm run demo
```

要求 Node.js >= 23.6（原生 TypeScript 类型擦除）。`tsconfig.json` 仅供编辑器与可选的 `tsc --noEmit` 类型检查使用，运行不依赖编译步骤。

## 运行样例输出（`npm run demo`）

```
0.1 + 0.2                                      0.3
0.1 + 0.2 === 0.3 ?                            true
9007199254740993 + 1 (past 2^53)               9007199254740994
123456789123456789 * 987654321987654321        121932631356500531347203169112635269

1 / 3 @ precision 5                            0.33333
1 / 3 @ precision 30                           0.333333333333333333333333333333

HALF_EVEN: 2.5 / -2.5                          2 / -2
HALF_UP:   2.5 / -2.5                          3 / -3
CEILING:   2.5 / -2.5                          3 / -2
FLOOR:     2.5 / -2.5                          2 / -3

1 / 0                                          Infinity     (flags: DivisionByZero)
1 / 3 (precision 10)                           0.3333333333 (flags: Rounded, Inexact)
1 / 4 (exact)                                  0.25         (flags: none)
0 / 0                                          NaN          (flags: InvalidOperation)

2.005 -> cents (HALF_UP)                       2.01
2.005 -> cents (HALF_EVEN)                     2.00
```

## 测试结果（`npm test`）

```
tests 47
suites 8
pass 47
fail 0
```

覆盖：精确构造与解析、超 2^53 大数四则运算、8 种舍入模式的正负中点对称性、上下文精度独立性、除零 vs 精度不足的标志区分、标志保留与清除、量化、特殊值（NaN / ±Infinity）传播。

## 文件结构

- `src/decimal.ts` — `Decimal` 类型与四则运算、量化、比较
- `src/rounding.ts` — 舍入模式与 BigInt 舍入核心（`roundDrop` / `roundToPrecision`）
- `src/context.ts` — `Context`（精度 + 舍入 + 标志集）与 `Flag`
- `test/decimal.test.ts` — `node:test` 测试
- `examples/demo.ts` — 运行样例
