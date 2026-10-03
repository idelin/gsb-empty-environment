# exact-matrix

纯有理数矩阵代数库（TypeScript + BigInt）：精确行列式与逆矩阵求值，全程零浮点误差。

## 原理

- **有理数**：`Rational` 以 `BigInt` 分子/分母存储，始终保持最简规范形（约分、分母为正）。
- **行列式**：无除法 Bareiss 算法（fraction-free 高斯消元）。每步更新
  `m[i][j] <- (m[i][j]*pivot - m[i][k]*m[k][j]) / prevPivot`，
  由 Sylvester 恒等式保证整除精确成立，中间量永不引入舍入；行交换只翻转符号。
- **奇异性**：主元列全零时行列式**精确**为零，绝非近似判断。
- **逆矩阵**：伴随矩阵法 `inv(A) = adj(A) / det(A)`，每个代数余子式都由
  Bareiss 精确求出；奇异矩阵抛错拒绝求逆。

## 运行

```sh
npm test    # Node.js 内置测试运行器（node:test），无需任何编译步骤
npm run demo
```

要求 Node.js >= 22.6（原生 TypeScript 类型擦除，直接运行 `.ts`）。

## 验收：5 阶希尔伯特矩阵

`det(H5) = 1/266716800000`（分子 1，分母 266716800000），
而 float64 计算相对误差约 `4.7e-10`。本库精确求出整数逆矩阵，
并验证 `H5 * H5^-1 === I` 逐元素严格成立。

## 用法

```ts
import { Matrix, hilbert } from './src/matrix.ts';

const h5 = hilbert(5);
const det = h5.determinant();   // 1/266716800000
const inv = h5.inverse();      // 精确整数矩阵
h5.multiply(inv).equals(Matrix.identity(5)); // true，严格相等
```
