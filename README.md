# 代数数比较与符号判定器（Algebraic Number Comparator）

带根号与有理系数的代数数**精确**比较器。全程使用 BigInt 有理数精确运算，
**不借由任何浮点近似**得出比较结论（浮点/十进制仅用于展示）。

## 原理

1. **零化多项式构造**：表达式树（`+ - * /`、`sqrt`、有理数）的每个节点通过
   Sylvester 结式（Bareiss 无分数行列式）构造其在 Q 上的零化多项式：
   - `α + β`：`Res_y(p(y), q(x−y))`
   - `α · β`：`Res_y(p(y), y^deg(q) · q(x/y))`
   - `√α`：`p(x²)`；`−α`：`p(−x)`；`1/α`：倒系数多项式
2. **极小多项式**：对零化多项式做平方自由分解（gcd 与其导数）与
   Kronecker 不可约因式分解，再用 Sturm 序列统计各不可约因子在该数
   隔离区间内的实根数，选出唯一因子——即极小多项式。
3. **实根隔离**：Sturm 序列 + Cauchy 根界 + 有理点二分，把极小多项式的
   全部实根隔离到两两严格分离的有理区间（有理根退化为点区间）。
4. **比较**：
   - 极小多项式不同 ⇒ 两数必不相等，加细区间直至分离即得大小；
   - 极小多项式相同 ⇒ 将两数的隔离区间分别加细到落入唯一实根隔离区间：
     同一区间 ⇒ **相等**；不同区间 ⇒ 按区间位置定大小。

## 环境要求

Node.js ≥ 22.6（利用原生 TypeScript 类型擦除运行，零依赖、无需编译）。
仓库内 `.nvmrc` 指向 v23.9.0。

```bash
npm test    # Node.js 内置测试运行器（node:test）
npm run demo
```

## 运行样例（`npm run demo` 实际输出）

```
=== 代数数精确比较演示（全程 BigInt 有理数运算，无浮点近似）===

比较 sqrt(2)+sqrt(3) 与 sqrt(5+2*sqrt(6))
  极小多项式(sqrt(2)+sqrt(3)) = x^4 - 10x^2 + 1
  极小多项式(sqrt(5+2*sqrt(6))) = x^4 - 10x^2 + 1
  极小多项式相同 → 用实根隔离区间判定是否为同一个实根:
    实根 #1 ∈ [-3.14626437, -3.14626435]
    实根 #2 ∈ [-0.31783725, -0.31783724]
    实根 #3 ∈ [0.31783719, 0.31783727]
    实根 #4 ∈ [3.14626421, 3.14626437]
  区间证据: sqrt(2)+sqrt(3) ≈ 3.146264369941, sqrt(5+2*sqrt(6)) ≈ 3.146264369941
  结论: sqrt(2)+sqrt(3) == sqrt(5+2*sqrt(6))

比较 sqrt(2)+sqrt(3) 与 sqrt(10)
  极小多项式(sqrt(2)+sqrt(3)) = x^4 - 10x^2 + 1
  极小多项式(sqrt(10)) = x^2 - 10
  极小多项式不同 → 两数必不相等，加细区间直至分离
  区间证据: sqrt(2)+sqrt(3) ≈ 3.146264369941, sqrt(10) ≈ 3.162277660168
  结论: sqrt(2)+sqrt(3) < sqrt(10)

比较 sqrt(7+2*sqrt(10)) 与 sqrt(2)+sqrt(5)
  极小多项式相同 (x^4 - 14x^2 + 9) → 结论: ==

比较 sqrt(2)*sqrt(2) 与 2
  极小多项式相同 (x - 2) → 结论: ==

比较 2+sqrt(3) 与 4
  极小多项式不同 (x^2 - 4x + 1 vs x - 4) → 结论: <
```

## 测试结果（`npm test`，Node.js 内置 `node:test`，29 个测试全部通过）

```
✔ minimal polynomials are built exactly
✔ ACCEPTANCE: sqrt(2) + sqrt(3) === sqrt(5 + 2*sqrt(6)) exactly
✔ ACCEPTANCE: sqrt(2) + sqrt(3) < sqrt(10)
✔ nested radicals: sqrt(7 + 2*sqrt(10)) === sqrt(2) + sqrt(5)
✔ nested radicals: sqrt(9 + 4*sqrt(5)) === 2 + sqrt(5)
✔ arithmetic identities            ✔ strict orderings
✔ sign determination               ✔ isZero detects exact cancellation
✔ sqrt of negative throws          ✔ division by exact zero throws
✔ decimal approximation for display is consistent with comparisons
✔ arithmetic / divmod / gcd / squareFree / sturm / isolation (poly)
✔ resultant compositions build annihilating polynomials
✔ factorSquareFree / factor (Yun + Kronecker)
✔ rational normalization / arithmetic / parsing / isqrt / decimal
ℹ pass 29
ℹ fail 0
```

## 代码结构

- `src/rational.ts` — BigInt 有理数（唯一的数值类型）
- `src/poly.ts` — Q[x] 多项式：算术、gcd、平方自由部分、Sturm 序列、实根隔离
- `src/resultant.ts` — Sylvester 结式（Bareiss 行列式）与和/积的零化多项式构造
- `src/factor.ts` — Yun 平方自由因式分解 + Kronecker 不可约因式分解
- `src/algebraic.ts` — 代数数表达式树、可细化隔离区间、精确比较器
- `test/` — Node.js 内置测试（`node:test`）
- `examples/demo.ts` — 运行样例

## API 速览

```ts
import { Alg } from './src/index.ts';

const r = (n: number) => Alg.rational(n);
const a = Alg.sqrt(r(2)).add(Alg.sqrt(r(3)));            // sqrt(2) + sqrt(3)
const b = Alg.sqrt(r(5).add(r(2).mul(Alg.sqrt(r(6)))));  // sqrt(5 + 2*sqrt(6))

a.minimalPolynomial().toString(); // "x^4 - 10x^2 + 1"
a.compare(b);                     // 0  （精确相等）
a.compare(Alg.sqrt(r(10)));       // -1 （严格小于）
a.sub(b).sign();                  // 0
a.isZero();                       // false
```
