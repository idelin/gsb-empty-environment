# strict-decimal

严格格式的精确十进制解析库（TypeScript，零依赖）。以 `BigInt` 系数 + 十进制指数为
基础，**绝不经过 `Number` 中转大数**；比较、规范化、量化、区间判断全部在精确表示
上完成，极大指数永不展开。

## 特性

- **严格词法**：`sign? mantissa exponent?`，拒绝分组逗号、下划线、空白、十六进制、
  `NaN`/`Infinity`、空尾指数（`1e`、`1e+`）、空尾小数点（`1.`）等。
- **有效位元数据**：`coefficient` 保留书写的每一位（含尾零），`significantDigits`
  按文档化规则计算 —— `1.2300` 与 `1.23` 数值相等但有效位分别为 5 与 3；
  `12300`（无小数点）有效位为 3；`0.00` 有效位为 2。
- **负零不混淆**：`-0` / `-0.00` 保留符号与尾零，数值比较 `-0 == 0`，但
  `negative`、`raw`、`significantDigits` 元数据各自独立。
- **规范输出**：`toFixed`（定点，预算保护）与 `toScientific`（科学记数，保留有效位），
  `normalize` 二选一。
- **增量解析**：`DecimalParser.write(chunk)` 支持任意切块，与整串解析结果完全一致；
  `raw` 原词法跨块保存。所有错误（非法字符、非法指数、预算耗尽、finish 截断）携带
  跨块绝对位置与 DFA 状态。
- **预算优先**：指数幅度在扫描时即检查（默认 `|exp| <= 999999`），在构造/展开任何东西
  之前抛出 `EXPONENT_OVERFLOW`；尾数位数、定点展开长度、量化位移均有独立预算。
- **比较不展开**：`compare` 只检查符号、调整阶码与系数字符串，`1e999999` 与
  `2e999999` 的比较是 O(位数)。
- **量化与舍入证据**：`quantize(d, targetExponent, mode)` 支持显式舍入模式
  `halfEven | halfUp | halfDown | down | up | floor | ceiling`，返回
  `{ discarded, guardDigit, sticky, exact, roundedUp, ... }` 完整舍入证据。
- **区间约束**：`inRange` / `assertInRange`，开闭区间可选，同样不展开指数。

## 表示

```
value = (negative ? -1 : 1) * coefficient * 10^exponent
```

`coefficient: bigint` 按书写保留全部尾数数字；`exponent: number`（预算内精确）；
`raw: string` 为原始词法。

## 使用

```ts
import { parseDecimal, DecimalParser, toFixed, toScientific,
         compare, quantize, inRange } from 'strict-decimal';

const d = parseDecimal('1.2300');
d.coefficient;        // 12300n
d.exponent;           // -4
d.significantDigits;  // 5
toFixed(d);           // '1.2300'
toScientific(d);      // '1.2300e0'

const p = new DecimalParser();
p.write('-1.').write('23').write('00e+4');
p.finish();           // 与 parseDecimal('-1.2300e+4') 完全一致

quantize(parseDecimal('1.235'), -2, 'halfEven');
// { result: 1.24, evidence: { discarded: '5', guardDigit: 5, sticky: false,
//   exact: false, roundedUp: true, ... } }

inRange('1e999999', { min: '0', max: '2e999999' });  // true，不展开指数
```

## 运行

```bash
npm test     # node --test test/*.test.ts（Node >= 22.6，原生类型擦除）
npm run demo # node examples/demo.ts
```

## 运行样例（`npm run demo` 摘要）

```
=== strict parse + metadata ===
1.2300   coeff=12300 exp=-4 sigDigits=5 negative=false raw="1.2300"
-0.00    coeff=0 exp=-2 sigDigits=2 negative=true raw="-0.00"

=== 1.2300 vs 1.23: equal value, different significant digits ===
compare(1.2300, 1.23) = 0 (numerically equal)
significantDigits: 5 vs 3

=== huge exponents: compare without expansion, budget first ===
compare(1e999999, 2e999999) = -1 (no 10^999999 materialized)
parse('1e1000000') -> EXPONENT_OVERFLOW at position 8
toFixed(1e999999) -> DecimalError: fixed-point expansion of 1e999999 needs 1000000 digits, budget is 100000

=== quantize with explicit rounding + evidence ===
quantize(1.235, 1e-2, halfEven) = 1.24 evidence={...,"discarded":"5","guardDigit":5,"roundedUp":true}
quantize(1.245, 1e-2, halfEven) = 1.24 evidence={...,"roundedUp":false}
quantize(-0.001, 0.01, down) = -0.00 isNegativeZero=true

=== incremental chunked parsing == whole-string parsing ===
chunks ['-1.','23','00e','+4'] -> raw=-1.2300e+4 coeff=12300 exp=0 sig=5
deep-equal to one-shot parse: true

=== error positions: illegal exponent, budget, truncation ===
parse("1,000") -> INVALID_CHARACTER at position 1 (state=int)
parse("2.5e1.5") -> INVALID_EXPONENT at position 5 (state=eDigits)
parse("1e+") -> TRUNCATED_INPUT at position 3 (state=eSign)
parse("1e1000000") -> EXPONENT_OVERFLOW at position 8 (state=eDigits)
```

## 测试结果（`npm test`）

```
✔ test/parse.test.ts       — 严格格式、元数据、非法逗号/指数、预算位置（16 项）
✔ test/incremental.test.ts — 任意切块一致性、跨块绝对错误位置、截断/预算（7 项）
✔ test/decimal.test.ts     — 不展开比较、规范输出、量化证据、区间、负零（9 项）

ℹ tests 32
ℹ pass 32
ℹ fail 0
```

## 有效位规则（文档化）

- 含小数点：从第一个非零数字到最后一个书写的小数位全部有效（尾零计入）。
- 不含小数点：尾零不计入（`12300` → 3）。
- 全零：有小数点时取小数位个数（`0.00` → 2），否则为 1（`0` → 1）。

## 错误码

| code | 含义 |
| --- | --- |
| `EMPTY_INPUT` | 空输入 |
| `INVALID_CHARACTER` | 严格文法外的字符（含分组逗号） |
| `INVALID_EXPONENT` | 指数部分非法字符 |
| `TRUNCATED_INPUT` | `finish()` 时输入截断 |
| `BUDGET_EXCEEDED` | 尾数/展开/位移预算耗尽 |
| `EXPONENT_OVERFLOW` | 指数幅度或位数超预算（先于任何展开） |
| `OUT_OF_RANGE` | 区间约束违例 |
| `PARSER_CLOSED` | 解析器已关闭后继续使用 |
