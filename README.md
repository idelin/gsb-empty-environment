# exact-rational-matrix

纯有理数矩阵代数运算库（TypeScript + BigInt）：精确行列式与逆矩阵求值器。

## 特性

- **精确有理数**：`Rational` 基于 BigInt 分子/分母，自动约分、符号归一，全程零精度损失
- **行列式**：Bareiss 无除法（fraction-free）高斯消元，在整数域上精确进行
- **逆矩阵**：伴随矩阵法 `A⁻¹ = adj(A) / det(A)`，各余子式同样由 Bareiss 精确求得
- **奇异性精确判定**：`det(A) === 0` 时 `inverse()` 抛出 `SingularMatrixError`
- **严格验证**：`A * A⁻¹ === I` 与 `A⁻¹ * A === I` 均为逐元素严格相等

## 环境要求

Node.js >= 22.6（使用内置 `--experimental-strip-types` 直接运行 TypeScript，
配合内置 `node:test` 测试运行器，零第三方依赖）。

## 运行

```bash
npm test    # Node.js 内置测试（21 个用例）
npm run demo  # 5 阶希尔伯特矩阵演示
```

## 验收结果：5 阶希尔伯特矩阵

H₅ 是著名病态矩阵（条件数 ≈ 4.8×10⁵），浮点求逆会严重失真。本库精确结果：

```
det(H₅) = 1/266716800000        （分子 1，分母 266716800000，与理论值一致）

H₅⁻¹ =  ⎡   25   -300    1050   -1400    630 ⎤
        ⎢ -300   4800  -18900   26880 -12600 ⎥
        ⎢ 1050 -18900   79380 -117600  56700 ⎥
        ⎢-1400  26880 -117600  179200 -88200 ⎥
        ⎣  630 -12600   56700  -88200  44100 ⎦

H₅ * H₅⁻¹ === I   （严格相等: true）
```

## 测试结果

```
# tests 21
# pass 21
# fail 0
```

覆盖：有理数四则运算与解析（`0.1 + 0.2 === 0.3` 精确成立）、Bareiss 行列式
（含换主元、奇异矩阵）、分数矩阵行列式、1×1/2×2/3×3 求逆、奇异性精确判定、
行列式乘法律 `det(AB)=det(A)det(B)`、1–5 阶 Hilbert 行列式已知值（OEIS A005249）、
8 阶 Hilbert 求逆复原单位阵。

## 项目结构

```
src/rational.ts   精确有理数（BigInt 分子/分母）
src/matrix.ts     Bareiss 无除法行列式 + 伴随矩阵法求逆
src/hilbert.ts    N 阶希尔伯特矩阵构造
tests/            Node.js 内置测试（node:test + node:assert）
examples/demo.ts  5 阶希尔伯特矩阵运行样例
```

## API 速览

```ts
import { Rational } from './src/rational.ts';
import { RationalMatrix } from './src/matrix.ts';
import { hilbert } from './src/hilbert.ts';

const H = hilbert(5);
H.determinant();        // Rational: 1/266716800000
H.inverse();            // RationalMatrix（精确）
H.isSingular();         // false
H.mul(H.inverse());     // 严格等于 RationalMatrix.identity(5)

new Rational(1n, 3n).add(new Rational(1n, 6n)); // 1/2
Rational.from('0.1');   // 1/10（精确十进制解析）
```
