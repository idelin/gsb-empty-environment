# ladder-piecewise

把多条「阶梯规则」编译为**精确分段函数**的纯数值 TypeScript 库。基于 BigInt 有理数，
不经过 `Number` 中转大数；舍入规则与舍入位置全部显式。仅为数值函数，不接真实订单或计费服务。

要求 Node.js ≥ 23.6（原生类型擦除，直接运行 `.ts`，无需构建步骤）。

## 概念

- **两种阶梯模式**
  - `progressive`（累进分段）：每一档只对该档覆盖的输入部分计费（边际税率式）。
  - `flat`（整量落档）：整个输入落入哪一档，就按该档费率/固定额对全量计费。
  - 跨档时两模式结果不同（见样例）；恰在阈值上不会双计——相邻档共享端点时，
    编译器强制恰好一侧闭区间，两侧都闭会直接报错。
- **端点开闭显式**：每个档位的 `lower/upper` 都带 `lowerClosed/upperClosed`；
  `null` 表示 ±∞。允许留洞（hole），洞内贡献为 0。
- **规则可叠加**：`compileLadder({ rules: [...] })` 把多条规则求和为总函数；
  每条规则可带 `discount`（折扣乘子）、`min`/`max`（最低/最高，钳制）。
  单条规则内的作用顺序固定为：分段 → 分段舍入（若配置）→ 求和 → 规则总额舍入（若配置）
  → 折扣 → min/max 钳制。
- **舍入位置独立建模**：`rounding.at: 'segment' | 'rule-total'` 与组合层的
  `finalRounding` 是三个互不可交换的位置，配置在哪就发生在哪，库不会擅自交换
  （测试与样例都展示了同一费率在不同锚点下结果不同）。
- **逐段贡献证据**：`evaluate` 返回每一档的 `inputPortion / rate / offset / raw /
  rounded`、规则小计、折扣后值、钳制标记与最终总额。
- **反解**：`invert(ladder, target, window)` 精确求出窗口内所有满足 `f(x) = target`
  的输入区间（含平台多解与单点解），端点开闭正确。
- **网格参考验证**：`validateOnGrid` 在小有理网格上交叉验证正向求值与反解一致，
  并逐一检查断点不被双计。

## 精确性

- 所有数值为 `Rat`（BigInt 分子/分母，自动约分）。`Rat.parse('0.1')` 精确等于 `1/10`，
  大整数（如 2^80）运算不经过 `Number`。
- 舍入模式显式：`floor | ceil | truncate | half-up | half-down | half-even`，
  量子（quantum）为任意正有理数；负数的舍入方向有测试锁定。

## 运行

```sh
npm test        # node --test（Node 内置测试运行器）
npm run sample  # 运行 samples/demo.ts
```

## API 速览

```ts
import { Rat, compileLadder, evaluate, invert, validateOnGrid } from './src/index.ts';

const ladder = compileLadder({
  rules: [
    { name: 'tiered', mode: 'progressive',
      brackets: [
        { lower: Rat.parse('0'),  lowerClosed: true,  upper: Rat.parse('50'), upperClosed: true, rate: Rat.parse('0.1') },
        { lower: Rat.parse('50'), lowerClosed: false, upper: null,            upperClosed: false, rate: Rat.parse('0.3') },
      ],
      rounding: { quantum: Rat.parse('0.1'), mode: 'half-up', at: 'rule-total' } },
    { name: 'fee', mode: 'flat',
      brackets: [ { lower: null, lowerClosed: false, upper: null, upperClosed: false, rate: Rat.parse('0'), offset: Rat.parse('5') } ],
      discount: Rat.parse('1.5'), min: Rat.parse('1'), max: Rat.parse('4') },
  ],
  finalRounding: { quantum: Rat.parse('1'), mode: 'floor' },
});

evaluate(ladder, Rat.parse('75'));            // 逐段证据 + 总额
invert(ladder, Rat.parse('12'), { from: Rat.parse('-100'), to: Rat.parse('1000') }); // => 区间列表
validateOnGrid(ladder, { from: Rat.parse('-20'), to: Rat.parse('120'), step: Rat.parse('2.5') });
```

## 文件

- `src/rational.ts` — BigInt 有理数
- `src/rounding.ts` — 显式舍入
- `src/ladder.ts` — 阶梯规则、编译校验、求值与证据
- `src/inverse.ts` — 精确反解（断点细化 + 分段线性求根 + 平台合并）
- `src/validate.ts` — 有理网格参考验证
- `samples/demo.ts` — 运行样例
- `test/*.test.ts` — Node 内置测试

## 样例输出（节选）

```
x = 75
  rule tiered (progressive)
    bracket#0 portion=50 rate=1/10 offset=0 raw=5 rounded=5
    bracket#1 portion=25 rate=1/5 offset=0 raw=5 rounded=5
    subtotal=10 (raw 10) x discount 1 = 10 -> [min -inf, max +inf] => 10
  rule fee (flat)
    bracket#1 portion=75 rate=0 offset=5 raw=5 rounded=5
    subtotal=5 (raw 5) x discount 3/2 = 7.5 -> [min 1, max 4] => 4 (clamped)
  total before final rounding: 14  =>  floor to 1 =>  14

=== progressive vs flat across a tier (x = 150) ===
  progressive: 25
  flat:        30

=== rounding anchor matters (same rates, different anchor) ===
  per-segment floor at x=12: 0
  rule-total  floor at x=12: 1

=== inversion: all x with composed output = 12 ===
  [64.75, 69.75)

=== rational grid validation (step 2.5, includes negatives) ===
  points checked: 57
  inverse mismatches: 0
  overall: ok
```

## 测试结果

`npm test`（Node v23.9.0，node:test）：**31 个测试全部通过**，覆盖：

- 有理数精确性（2^80 运算、十进制精确解析、负数 floor/ceil）
- 六种舍入模式与负数平局方向、非单位量子
- 累进 vs 整量落档跨档差异、阈值不双计、端点开闭归属
- min/max/discount 顺序、分段舍入 vs 总额舍入 vs 最终舍入的不可交换性
- 负输入、逐段证据、大数不经过 Number
- 反解：单点解、平台区间（含端点开闭）、多区间、负区间、无解、钳制平台、最终舍入平台
- 小有理网格参考验证（断点、负输入、正逆一致性）
