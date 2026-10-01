# strict-decimal

严格格式的精确十进制解析库（TypeScript，Node.js 内置测试运行器验证）。
以 `BigInt` 系数 + 十进制指数为内部表示，**绝不通过 `Number` 中转大数**；
比较、舍入、区间判断全部在精确表示上完成，极大指数只做预算检查、绝不展开。

要求 Node.js >= 23.6（原生 TypeScript 类型擦除，无需构建步骤）。

## 特性

- **严格词法**：可选符号、小数点、科学指数；拒绝分组逗号、下划线、十六进制、
  `Infinity`/`NaN`、悬空小数点（`1.`）、非法指数（`1e1.5`、`1e++1`）等。
- **有效位元数据**：`sigDigits` / `trailingZeros` / `negativeZero` 独立保存。
  `1.2300` 与 `1.23` 数值相等（`equals`），但元数据不同（`identical` 为 false）；
  `-0` 与 `0` 数值相等但符号元数据不被混淆。
- **规范输出**：`toString({ format: 'fixed' | 'scientific' | 'auto' })`，
  保留显式有效位（`1.2300` → `1.2300` / `1.2300e+0`），输出长度受 `maxOutputDigits` 预算约束。
- **增量解析**：`DecimalParser.feed(chunk)` 任意切块与整串解析结果完全一致；
  原词法（`raw`）跨块保留；非法字符、预算耗尽、`finish()` 截断的错误位置
  均为全流绝对位置，与切块方式无关。
- **预算先行**：`maxDigits`（尾数位数）与 `maxExponent`（调整后指数绝对值）
  在超限字符处立即抛出 `BudgetExceededError`，巨大指数在展开前就被拒绝。
- **精确量化**：`quantize(targetExp, mode)` 支持 `down / up / floor / ceil /
  half-up / half-down / half-even` 七种显式舍入规则，返回完整舍入证据
  （保留位、丢弃位原文、与半值关系、是否进位、结果相对原值方向）。
- **区间约束**：`assertInInterval({ min, max, minInclusive, maxInclusive })`
  基于精确比较，违规抛出带值的 `IntervalViolationError`。

## 内部表示

```
value = sign × coeff × 10^exp        (coeff: BigInt, 按书写的有效数字)
```

比较时先按符号分流，再比较"调整后指数"（`exp + 位数`），仅在对齐时按
位数差（≤ 输入长度）做有界缩放——`1e(2^53-1)` 与 `2e(2^53-1)` 的比较
为常数时间，不展开指数。

## 用法

```ts
import { DecimalParser, parseDecimal } from './src/index.ts'

const d = parseDecimal('1.2300')          // 严格整串解析
d.sigDigits                               // 5
d.trailingZeros                           // 2
d.toString({ format: 'scientific' })      // '1.2300e+0'

const p = new DecimalParser({ maxDigits: 1000, maxExponent: 10000 })
p.feed('-1.23').feed('00e+5')             // 任意切块
const v = p.finish()                      // 截断时抛 DecimalParseError(pos)
v.raw                                     // '-1.2300e+5' 原词法保留

const { value, evidence } = parseDecimal('1.2345').quantize(-3, 'half-even')
// value = 1.234, evidence.discardedDigits = '5', relationToHalf = 'equal'

parseDecimal('1.5').assertInInterval({ min: parseDecimal('0'), max: parseDecimal('2') })
```

## 运行

```sh
npm test    # node --test test/*.test.ts
npm run demo
```

## 运行样例（`npm run demo` 实际输出）

```
=== strict parse + significant-digit metadata ===
1.2300     sigDigits=5 trailingZeros=2 negativeZero=false fixed=1.2300 sci=1.2300e+0
1.23       sigDigits=3 trailingZeros=0 negativeZero=false fixed=1.23 sci=1.23e+0
-0         sigDigits=1 trailingZeros=0 negativeZero=true fixed=-0 sci=-0e+0
-0.00      sigDigits=2 trailingZeros=2 negativeZero=true fixed=-0.00 sci=-0.00e+0
0.0012300  sigDigits=5 trailingZeros=2 negativeZero=false fixed=0.0012300 sci=1.2300e-3
1.23e4     sigDigits=3 trailingZeros=0 negativeZero=false fixed=12300 sci=1.23e+4

=== 1.2300 == 1.23 numerically, but metadata differs ===
equals=true identical=false

=== incremental chunked parsing keeps the raw lexeme ===
raw=-1.2300e+5 fixed=-123000 sigDigits=5

=== huge exponents: budget first, compare without expansion ===
budget exceeded at pos=7 budget=exponent limit=100000
1e(2^53-1) vs 2e(2^53-1) -> -1 in 0.009ms

=== quantize with exact rounding evidence ===
1.2345 -> 1.234
evidence={"mode":"half-even","targetExp":-3,"inexact":true,"keptDigits":"1234","discardedDigits":"5","relationToHalf":"equal","incremented":false,"direction":-1}
3.14159265 -> 3.1416 ("9265" discarded, greater half)

=== interval constraints ===
violated max bound: value 2.0000000000000000000000001 violates max bound 2 (inclusive)
1.5 in [0,2]: 1.5

=== error positions: illegal exponent / comma / truncation ===
"1e1.5" -> unexpected-char at pos=3: unexpected character "." at position 3
"1,000" -> unexpected-char at pos=1: unexpected character "," at position 1
"1e+" -> truncated at pos=3: truncated input: expected an exponent digit at position 3

=== no Number transit: precision beyond doubles ===
9007199254740993 vs 9007199254740992 -> 1 (Number would tie)
```

## 测试结果（`npm test`）

```
ℹ tests 109
ℹ suites 10
ℹ pass 109
ℹ fail 0
ℹ cancelled 0
ℹ skipped 0
ℹ todo 0
```

覆盖：严格合法/非法形式与精确错误位置、预算边界（含"先预算检查"）、
规范输出三种格式与往返、有效位/负零/尾零元数据、不展开极大指数的比较、
超出 double 的精度、区间约束（含开闭区间）、全部切块组合与整串一致性、
切块无关的错误位置（非法指数/逗号/预算耗尽/finish 截断）、七种舍入规则
与完整舍入证据。

## 目录结构

- `src/decimal.ts` — `Decimal` 值类型：比较、量化、区间、格式化
- `src/parser.ts` — `DecimalParser` 增量状态机 + `parseDecimal`
- `src/errors.ts` — `DecimalParseError` / `BudgetExceededError` / `IntervalViolationError`
- `test/` — node:test 测试（109 例）
- `examples/demo.ts` — 演示脚本
