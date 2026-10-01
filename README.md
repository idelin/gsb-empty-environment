# exact-decimal

TypeScript 精确十进制运算库：以 **BigInt 系数 + 十进制指数** 表示数值，
全程不经过 `Number` 中转，支持加减乘除、量化（quantize）、多种显式舍入
模式、按上下文（Context）独立的精度控制与粘性错误标志。

零依赖，直接由 Node.js ≥ 23.6 内置的 TypeScript 类型擦除运行，测试使用
Node 内置测试框架（`node:test`）。

## 设计

- **表示**：`value = sign × coef × 10^exp`，`coef` 为非负 `BigInt`。
  大数始终以字符串或 `bigint` 进入（`Decimal.of('9007...')`），
  任何运算路径都不把值转成 `Number`。
- **精确运算**：`add` / `subtract` / `multiply` 默认无限精度精确；
  传入 `Context` 时按 `context.precision`（有效数字位数）舍入并记录标志。
- **除法**：`divide` 必带 Context（缺省用 28 位精度的 `defaultContext`）。
  - 除数为零 → 置 `DIVISION_BY_ZERO` 标志并抛 `DivisionByZeroError`；
  - 商不能精确终止 → 按上下文精度舍入，置 `INEXACT` + `ROUNDED`；
  - 两者严格区分：除零时绝不置 `INEXACT`。
- **舍入模式**（`src/rounding.ts`）：`HALF_EVEN`、`HALF_UP`、`HALF_DOWN`、
  `UP`、`DOWN`、`CEILING`、`FLOOR`。舍入决策在绝对值上进行，因此中点
  行为对正负数完全对称（定向模式 `CEILING`/`FLOOR` 除外，这是其定义）。
- **量化**：`quantize(exp, mode)` 显式指定目标指数与舍入模式，如
  `quantize(-2, 'HALF_UP')` 保留两位小数。
- **错误标志**：`Context.flags` 为粘性集合，一直保留到 `clearFlags()`；
  各 Context 的精度与标志互不影响。

## 使用

```ts
import { Decimal, Context, RoundingMode } from './src/index.ts';

Decimal.of('0.1').add(Decimal.of('0.2')).toString();   // '0.3'

const ctx = new Context({ precision: 10, rounding: RoundingMode.HALF_EVEN });
Decimal.of('1').divide(Decimal.of('3'), ctx).toString(); // '0.3333333333'
ctx.flags; // Set { 'INEXACT', 'ROUNDED' }

Decimal.of('2.5').quantize(0, 'HALF_EVEN').toString();  // '2'
Decimal.of('-2.5').quantize(0, 'HALF_UP').toString();   // '-3'
```

## 运行

```sh
npm test    # node --test 'test/*.test.ts'
npm run demo
```

## 测试结果

28 个测试全部通过（Node v23.9.0）：

```
ℹ tests 28
ℹ suites 6
ℹ pass 28
ℹ fail 0
```

覆盖：`0.1+0.2=0.3` 精确性、超 2^53 大整数运算、精确/不精确除法标志、
除零与精度不足区分、7 种舍入模式的正负中点对称性、上下文精度独立性、
量化与标志粘性、解析与格式化。
