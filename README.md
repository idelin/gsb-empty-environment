# exact-interval-affine

精确十进制区间 / 仿射算术库（TypeScript，零依赖，Node.js 内置测试运行器）。

## 运行

```sh
node --test tests/*.test.ts   # 或 npm test —— 运行全部测试
node samples/demo.ts          # 或 npm run demo —— 运行样例
```

要求 Node >= 23.6（内置 type stripping，直接执行 .ts）。如需类型检查，
安装 TypeScript 后运行 `npm run typecheck`（本仓库代码只用可擦除语法）。

## 设计

- **精确十进制**（`src/decimal.ts`）：`coeff * 10^exp`，BigInt 尾数，解析与
  运算不经过 Number，大数（>2^53）保持精确。舍入规则显式：
  `floor / ceil / down / up`，下界一律 `floor`、上界一律 `ceil`（向外）。
- **区间**（`src/interval.ts`）：`EMPTY`（空集）与 `UNKNOWN`（全直线，双 null
  界）是不同种类。支持交（`intersect`）、并/凸包（`hull`）、包含判断
  （`containsPoint / containsInterval`）。所有运算向外舍入到指定 scale。
- **除法跨零**（`src/interval.ts` + `src/union.ts`）：`'reject'` 抛
  `ZeroCrossingError`；`'split'` 显式返回 1–2 段射线，由 `IntervalUnion`
  承载多段定义域。段数超过 `maxSegments` 时只能保守包围为凸包，并置
  `precisionLoss` 与说明性 `note`。
- **仿射形式**（`src/affine.ts`）：`x = c + Σ a_i·ε_i`。同一不确定输入共享
  噪声符号 id，线性运算逐系数精确（`x - x = [0,0]`，无虚假扩大）；负系数
  缩放翻转所有系数符号从而翻转边界。非线性步骤（`mul`、`recip`）引入新
  噪声符号并附带保守余项证书（`AffineMulCert / AffineRecipCert`）。
- **独立证书检查**（`src/verify.ts`）：区间运算用精确有理算术
  （`src/exact-range.ts`）重算真实值域，验证向外舍入方向与包含性；仿射
  余项用自适应有理二分细化严格证明 `|g| ≤ δ`。
- **精确有理反例搜索**（`src/search.ts`）：所有采样点用 `Rational` 精确
  求值，发现的任何违例都是真实反例；`sampledTruthCheck` 将同一表达式
  分别运行于仿射形式与精确有理数，断言任何采样真值落在输出界内。

## 测试

`tests/` 下 6 个文件共 41 个用例，覆盖：精确大数运算与四种舍入模式、
空/未知区间语义、向外舍入方向、跨零除法的分裂与拒绝、段数超限的精度
损失标注、相关符号保留、负系数翻转、证书接受/篡改拒绝、反例搜索命中、
采样真值包含性。
