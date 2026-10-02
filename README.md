# rational-bezier-extrema

一维**有理贝塞尔曲线极值定位器**（TypeScript，零依赖，仅 Node.js 内置模块）。

曲线 `y(t) = N(t)/D(t)`，其中 `N(t) = Σ wᵢpᵢBᵢ(t)`、`D(t) = Σ wᵢBᵢ(t)`。
求导得 `y' = (N'D − ND') / D²`；权重为正时 `D(t) ≥ min(wᵢ) > 0`，故 `y'`
的符号由多项式 `P = N'D − ND'` 决定。本库把 `P` 导出为**本原整系数多项式**，
用 **Sturm 定理 + 大整数有理数二分**在 `(0,1)` 内隔离其所有不同实根，筛出
**变号根**作为极值点，并结合端点 `t=0,1` 给出：

- 导数多项式（整系数）与所有根的有理隔离区间（命中时给出精确有理根）；
- 上升 / 下降 / 常值区间划分；
- 每个极值点的类型（max/min）、认证有理数值区间；
- 全局最大 / 最小值及其全部取得位置（含并列端点）。

所有计算均为精确有理数运算（BigInt），无浮点误差；代数数根以宽度
`< 2^-48`（可配置）的认证有理区间输出。

## 环境要求

Node.js ≥ 22.6（利用原生 TypeScript 类型擦除，无需构建步骤）。

## 运行

```bash
npm test        # node --test test/*.test.ts
npm run demo    # node examples/demo.ts
```

## 用法

```ts
import { analyze } from "./src/index.ts";

const r = analyze([0, 1, 0], [1, 2, 1]);   // 控制点, 权重
console.log(r.derivativePolyString);       // P(t) = -2t + 1
console.log(r.criticalPoints[0].t.exact);  // 1/2（精确有理数）
console.log(r.criticalPoints[0].value);    // 2/3
console.log(r.globalMin.locations.length); // 2（t=0 与 t=1 并列）
```

输入支持 `number | string | bigint | Rat`（如 `"3/4"`、`0.1`、`"1e-3"` 均精确解析）。

## 运行样例（`npm run demo` 摘要）

```
Example 1: symmetric weights -> exact rational extremum
  control points: [0, 1, 0]   weights: [1, 2, 1]
  extrema equation: P(t) = -2t + 1   (integer coefficients)
  monotone segments:
    t: 0 -> 1/2   increasing
    t: 1/2 -> 1   decreasing
  interior critical points:
    max at t = 1/2 (exact)
        value = 2/3  certified in [2/3, 2/3]
  GLOBAL max = 2/3 at t = 1/2
  GLOBAL min = 0 at t = 0, 1

Example 2: asymmetric weights -> irrational extremum, certified
  control points: [0, 1, 0]   weights: [1, 2, 3]
  extrema equation: P(t) = -2t^2 - 2t + 1   (integer coefficients)
  interior critical points:
    max at t = 412107968022933/1125899906842624  in [103026992005733/281474976710656, ...]
        value = 294159345498132566930839973703/548908811460119017870002749440  (certified)
  GLOBAL min = 0 at t = 0, 1
  （t* = (√3−1)/2 ≈ 0.3660254，最大值 4−2√3 ≈ 0.5358984，均被区间严格夹住）

Example 4: cubic with two interior extrema
  control points: [0, 2, -1, 1]   weights: [1, 1, 1, 1]
  extrema equation: P(t) = 5t^2 - 5t + 1   (integer coefficients)
  monotone segments: increasing / decreasing / increasing
  GLOBAL max = 1 at t = 1   （内部极大 0.7236... 并非全局最大）
  GLOBAL min = 0 at t = 0
```

## 测试结果（`npm test`）

```
✔ 20 tests, 0 failures
  test/rational.test.ts        有理数解析 / 四则运算 / 比较
  test/poly.test.ts            Sturm 序列、重根、精确根、无理根隔离（√2/2）
  test/rationalBezier.test.ts  对称/非对称权重、单调、端点导数根、三次曲线、常值曲线
```

## 项目结构

- `src/rational.ts` — BigInt 精确有理数
- `src/poly.ts` — 整/有理系数多项式、Sturm 序列、根隔离与加细
- `src/rationalBezier.ts` — 曲线建模、导数多项式导出、极值分析
- `examples/demo.ts` — 四个运行样例
- `test/` — Node.js 内置测试（`node:test`）
