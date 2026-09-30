# exact-units

精确单位换算库（TypeScript，零依赖）：

- **单位 = 量纲向量 + 比例 + 可选偏移**（`src/unit.ts`）
- **转换图检测矛盾环**（`src/registry.ts`）
- **绝对量 / 差量类型区分**（`src/quantity.ts`）
- **BigInt 精确有理数，不经 Number 中转大数，舍入规则显式**（`src/rational.ts`）

## 运行要求

Node.js ≥ 22.6（利用内置 TypeScript 类型擦除直接运行 `.ts`，推荐 ≥ 23.6 免 flag）。
源码只使用可擦除语法，无需编译步骤；如需类型检查可 `npm i -D typescript @types/node && npx tsc --noEmit`。

```sh
npm test    # node --test（Node.js 内置测试运行器）
npm run demo
```

## 设计要点

### 精确数（Rational）

所有数值以 BigInt 分子/分母保存，构造接受 `bigint`、安全整数 `number`、
十进制/分数字符串（`"0.01"`、`"5/9"`、`"1.5e-3"`）。非安全整数的 `number`
直接抛 `TypeError`，杜绝大数经 Number 中转丢失精度。
仅在 `toDecimalString(places, mode)` 输出时按显式规则舍入：
`half-even`（默认，银行家）、`half-up`、`half-down`、`up`、`down`、`floor`、`ceiling`。

### 单位与转换图

单位相对该量纲基准单位声明 `scale`（线性比例）与可选 `offset`（绝对零点）。
注册导出单位时自动向基准单位连边；`addConversion(from, to, factor)` 加边前：

1. 量纲向量不一致 → `DimensionMismatchError`（米不能转秒）；
2. 两点间已有路径且合成比例 ≠ 新边 → `ContradictoryConversionError`（矛盾环，图保持原状）；
3. 与已有路径一致 → 视为冗余边，返回 `false`。

### 绝对量 vs 差量

| 运算 | 结果 |
| --- | --- |
| 绝对量 ± 差量 | 绝对量 |
| 绝对量 − 绝对量 | 差量 |
| 差量 ± 差量 | 差量 |
| 绝对量 + 绝对量 / 差量 − 绝对量 | 抛 `TypeError` |

差量转换只乘比例（温差 Δ10 °C = Δ10 K，**不加** 273.15）；
绝对量经基准单位换算并应用偏移（0 °C = 273.15 K，32 °F = 0 °C）。

## 运行样例（`npm run demo` 摘要）

```text
1 cm → m（精确分数）                1/100 m (Δ)
12345678901234567890123456789012345678 cm → m
                                    6172839450617283945061728394506172839/50 m (Δ)
2.5, 0 位, half-even（银行家）        2
绝对量 0 °C → K                     5463/20 K
差量 Δ10 °C → K（不加偏移）           10 K (Δ)
矛盾边 cm→m=1/10 被拒绝              已存在换算比例 1/100，新边声明 1/10，二者构成矛盾环
量纲不符 m→s 被拒绝                  m（长度^1）不能转换为 s（时间^1）
```

## 测试结果

`node --test`：20 个测试全部通过（`test/rational.test.ts`、`test/registry.test.ts`、`test/quantity.test.ts`），
覆盖精确解析/大数运算/七种舍入规则、cm→m=1/100、多跳路径合成、矛盾环拒绝、
量纲不符拒绝、摄氏/华氏绝对量与差量、量的代数规则。
