# bigint-fixed-root

BigInt 定点 n 次方根：全程精确整数运算（不经过 `Number`），按指定小数位**正确舍入**，
给出余数 / 误差界 / 上下整数包围，负输入拒绝，迭代终止可证明。

## 运行要求

- Node.js ≥ 23.6（原生 TypeScript 类型擦除 + `node:test`；开发验证版本 v23.9.0）
- 无任何第三方依赖

```bash
npm test     # node --test test/*.test.ts（Node 内置测试运行器）
npm run demo # 运行样例
```

## API

```ts
import { rootN, verifyRoundedRoot, isqrt, iroot, parseDecimal, formatDecimal } from './src/index.ts';

const res = rootN('0.00000000009', 2, 10);            // 输入 string | bigint | Decimal
// res.coeff / res.exp     结果 = coeff * 10^exp（exp 恒等于 -outScale）
// res.floor / res.ceil    Y = 根 * 10^outScale 的上下整数包围
// res.exact               完全幂时精确命中
// res.lower / res.upper   真实根的认证十进制区间
// res.maxError            认证误差界（最近舍入为 0.5 ulp，floor/ceil 为 1 ulp，exact 为 0）
// res.scaledRemainder     整数根层面的余数：被开方整数 - floor^n
// res.iterations          整数根迭代实际步数

verifyRoundedRoot('0.00000000009', 2, 10, res);       // 独立精确核验 => true
isqrt(10n ** 40n);                                    // 平方根专用快速路径
iroot(7, 3n ** 7n);                                   // 通用整数 n 次根（k=2 自动走快速路径）
```

- 舍入规则（`mode`，显式指定，默认 `'half-even'`）：
  `'floor' | 'ceil' | 'half-up' | 'half-down' | 'half-even'`（最近偶数）。
- 输入输出精度独立：输入是任意精确十进制 `coeff * 10^exp`，输出位数由 `outScale` 单独指定。
- 位数预算：`maxDigits`（默认 100000）限制 `outScale` 与所有大整数操作数的十进制位数，超出抛 `RangeError`。
- 负输入（`< 0`）抛 `RangeError`；`-0` 视为 0。

## 数学原理

设输入 `x = c * 10^e`（`c >= 0` 整数），次数 `n >= 1`，输出比例 `s >= 0`。令

```
Y = x^(1/n) * 10^s   =>   Y^n = c * 10^(e + n*s) = c * 10^E
```

结果系数 `R = round(Y)` 全部用精确整数比较得到：

- **E >= 0**：`Y^n = T := c * 10^E` 是整数，`floor(Y) = iroot(n, T)`。
  中点比较 `Y ? r + 1/2` ⟺ `T * 2^n ? (2r+1)^n`。
- **E < 0**：`Y^n = c / 10^(-E)`。因为 `r^n` 是整数，
  `r^n <= Y^n` ⟺ `r^n <= floor(c / 10^(-E))`，故 `floor(Y) = iroot(n, floor(c/10^(-E)))`——
  被开方数的小数部分不影响整数下取整，**极小的输入不会因提前截断而变成 0**。
  中点比较 `Y ? r + 1/2` ⟺ `c * 2^n ? (2r+1)^n * 10^(-E)`。

中点平局（比较结果恰好相等，即 `Y` 精确等于 `r + 1/2`）由精确整数幂比较判定，
`half-even` 取偶数邻居。完全幂时 `exact = true`，余数为 0，误差界为 0。

## 整数根迭代的终止性证明

`iroot` / `isqrt` 使用整数牛顿迭代 `x' = ((k-1)x + floor(n/x^(k-1))) / k`，
停止规则是**认证式**的（发现 `x^k <= n` 才退出），**不是**“相邻两次迭代值相同”。

设 `r = floor(n^(1/k))`，`b = bitLength(n)`：

1. **起点在根上方**：`x0 = 2^ceil(b/k)`，`x0^k = 2^(k*ceil(b/k)) >= 2^b > n`，故 `x0 > r`。
2. **不变式 `x >= r`**：由 AM-GM，`((k-1)x + n/x^(k-1))/k >= n^(1/k) >= r`；
   外层向下取整后仍 `>= r`（`r` 是整数）。
3. **严格下降**：若 `x > r`，则 `x^k >= (r+1)^k > n`，于是 `n/x^(k-1) < x`，
   `x' <= ((k-1)x + x - 1)/k < x`。严格下降且有下界 `r >= 0` 的整数列必终止。
4. **认证停止正确**：退出时 `x^k <= n < (r+1)^k`，与不变式 `x >= r` 合并得 `x == r`。
5. **步数界**：记 `d = x - r`，可证 `d' <= floor((k-1)d/k)`（`k=2` 时 `d' <= floor(d/2)`），
   超额部分几何收缩，迭代次数 `O(b)`。代码内置保守硬上限
   （`isqrt`: `2b+16`，`iroot`: `2b+4k+16`）作为防御性断言，测试对随机与对抗输入
   验证了更紧的实测界（`isqrt <= ceil(b/2)+2`）。

## 测试（Node 内置 `node:test`，28 个用例）

- 小整数穷举：`isqrt` 0..10000、`iroot` k=3..6 对 0..3000 逐一验证下取整性质与余数。
- 平方/幂边界反例：大 `r` 的 `r^2-1, r^2, r^2+1, (r+1)^2-1` 及 k=3,5,7 的 `r^k±1`。
- 快速路径一致性：`isqrt` 与通用牛顿路径在 300 随机 + 262 个对抗值（2^b、2^b-1）上逐位一致。
- 中点舍入：`r + 1/2`（r=1..50，n=2,3）穷举平局检查，half-even/up/down 行为精确。
- 极小数：`9e-11`、`5e-25`、`1e-1000` 不截断为 0；`1e-1000` 的平方根精确等于 `1e-500`。
- 舍入穷举核验：0..1000 × 比例 0..3 × n∈{2,3} × 5 种舍入模式，全部通过
  `verifyRoundedRoot`（独立的精确整数幂不等式核验，含上下界与误差界一致性）。
- 篡改检测：结果系数 ±1 后核验必须失败。
- 恢复一致性：输入与结果的 format→parse 往返后舍入结果不变。
- 预算与负输入：超限抛 `RangeError`，负数抛 `RangeError`。

实测结果：

```
ℹ tests 28
ℹ pass 28
ℹ fail 0
ℹ duration_ms ~1000
```

## 样例运行（`npm run demo`，节选）

```
sqrt(2) to 50 fractional digits
  result     = 1.41421356237309504880168872420969807856967187537695   (mode=half-even, exact=false, verified=true)
  bracket    = 141421356237309504880168872420969807856967187537694 <= root*10^50 <= 141421356237309504880168872420969807856967187537695
  true root in [1.414213562373095048801688724209698078569671875376945, 1.414213562373095048801688724209698078569671875376955], maxError=5e-51
  remainder  = 228343919038393186956257119542673618801417329162364
  iterations = 7

sqrt(6.25) = 2.5 exactly, half-even -> 2        # 中点平局，取偶
sqrt(30.25) = 5.5 exactly, half-even -> 6
cbrt(3.375) = 1.5 exactly, half-even -> 2
sqrt(9e-11) at scale 10 -> 0.0000094868         # 极小数不截断为 0
sqrt(1e-1000) = 1e-500 exactly (exact=true)     # 完全幂精确
sqrt(10^120 + 7) at scale 20 -> 1000...000.00000000000000000000 (verified=true)
rootN("-4", 2, 2) threw RangeError: rootN: negative input rejected
rootN("2", 2, 5000, {maxDigits: 1000}) threw RangeError: outScale 5000 exceeds digit budget 1000
```

## 文件结构

- `src/decimal.ts` — 精确十进制 `Decimal`、解析 / 格式化（不经过 `Number`）
- `src/iroot.ts` — 整数 n 次根：`isqrt` 快速路径 + 通用牛顿迭代（认证停止，可证终止）
- `src/root.ts` — `rootN` 正确舍入、`verifyRoundedRoot` 独立核验
- `test/` — `node:test` 测试（穷举 + 边界反例 + 对抗输入）
- `demo.ts` — 运行样例
