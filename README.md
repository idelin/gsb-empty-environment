# bigint-rational

纯 BigInt 精确有理数库（TypeScript，零依赖）。全程不经过 `Number` 中转大数，
利用 Node.js ≥ 22.18 / 23 的内置类型擦除直接运行 `.ts`，测试使用 Node 内置
`node:test` 运行器。

## 功能

- **规范化存储**：构造时 gcd 约分；分母为负时符号移到分子；`0` 统一为 `0/1`；分母为 0 抛错。
- **四则运算**：`add / sub / mul / div`，结果自动约分；除零抛错。
- **比较**：`cmp` 交叉相乘（`a.num*b.den` vs `b.num*a.den`），BigInt 无溢出，安全精确。
- **循环节十进制**：`toDecimal` 用余数状态表（`Map<remainder, position>`）检测循环，
  产出 `{ integer, prefix, period }`，如 `1/6 → 0.1(6)`、`1/7 → 0.(142857)`、
  `1/8 → 0.125`（有限小数 `period` 为空）。余数状态至多 `den-1` 个，必然终止或入环。
- **预算与未完成标记**：`maxDigits` 限制状态表规模与产出位数，超出即返回
  `complete: false`（格式化输出带 `…[incomplete]`），不会无限存储，截断结果
  不冒充精确表示。
- **分页输出**：`fractionPage(exp, page, pageSize)` 按前缀/周期长度直接定位任意页，
  无需展开巨大十进制串。
- **显式舍入**：`toFixed(places, mode)` 支持 `trunc / floor / ceil / half-up /
  half-even` 五种模式，返回值是舍入近似串，与精确表示严格区分。
- **表达式 DAG**（`src/dag.ts`）：`lit/add/sub/mul/div` 构建表达式，
  `evaluate` 自底向上惰性求值并记忆化共享子节点；运算过程**延迟约分**——
  先不约分组合分子分母，仅当位长超过 `maxBits` 预算时才 gcd 约分补救，
  仍超预算则返回 `{ status: 'incomplete' }`；`compareExprs` 走交叉乘比较，
  无需展开十进制。

## 运行

```sh
npm test    # node --test（15 个测试，含小整数穷举）
npm run demo
```

## 示例

```ts
import { Rational, fractionPage } from './src/rational.ts';

Rational.of(1n, 6n).toDecimalString();        // '0.1(6)'
Rational.of(1n, 97n).toDecimal({ maxDigits: 10 }).complete; // false（超预算）
fractionPage(Rational.of(1n, 7n).toDecimal(), 2, 3).digits; // '142'
Rational.of(1n, 8n).toFixed(2, 'half-even');  // '0.12'
```

## 测试要点

- 小整数穷举：规范化（分母正、互素、0→0/1）、四则恒等式（`(a+b)-b=a`、
  `(a*b)/b=a`）、`cmp` 与直接交叉乘符号一致性及反对称性。
- 循环节回转：`-30..30 / 1..30` 全部展开→`fromDecimalParts` 还原→与原值相等。
- 预算：`1/97`（周期 96）在 `maxDigits=10` 下标记未完成，预算充足时完整还原。
- 大数：`2^90+1`、`2^80` 级分数比较等远超 `2^53` 的场景保持精确。
