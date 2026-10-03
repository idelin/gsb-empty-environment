# rational-simplex

纯有理数（零精度损失）两阶段单纯形法线性规划求解器，TypeScript 实现。

- 所有运算基于 `bigint` 分子/分母的 `Rational` 类，迭代中的基变量置换（pivot）完全使用精确有理数运算，绝无浮点误差。
- 输入标准型：最大化 `c^T x`，约束 `A x (<=, >=, =) b`，`x >= 0`。
- 输出：精确最优有理数解向量与对偶变量（影子价格），或无界证书（可行点 + 射线方向）、不可行证书（Farkas 乘子）。
- 采用 Bland 规则选择进基/出基变量，保证终止（不循环）。

## 环境要求

Node.js >= 22.18 或 >= 23.6（利用原生 TypeScript 类型擦除直接运行 `.ts`，无需编译）。仓库附 `.nvmrc`（23.9.0）。

## 运行

```sh
nvm use            # 可选，切换到 .nvmrc 指定版本
npm run example    # 运行样例
npm test           # 运行 Node.js 内置测试（node:test）
```

## 验收样例

3 个不等式约束的最大化问题：

```
max  x1 + x2
s.t. x1 + 3 x2 <= 8
     3 x1 + x2 <= 9
     x1 + x2 <= 5
     x1, x2 >= 0
```

`npm run example` 输出：

```
Pivot history (exact rational arithmetic):
  iter 1 [phase 2]: x1 enters, s2 leaves (pivot = 3) -> basis { s1, x1, s3 }
  iter 2 [phase 2]: x2 enters, s1 leaves (pivot = 8/3) -> basis { x2, x1, s3 }

Status: optimal
Primal solution: x = (19/8, 15/8)
Optimal value:   z = 17/4
Dual prices:     y = (1/4, 1/4, 0)

Dual proof:
  A^T y [x1] = 1 >= 1  (ok)
  A^T y [x2] = 1 >= 1  (ok)
  b^T y = 17/4 = z = 17/4  (strong duality ok)

All checks passed.
```

精确最大值 `z = 17/4`，对偶价格 `y = (1/4, 1/4, 0)` 满足对偶可行性与强对偶（`b^T y = 17/4`）。

## 测试结果

`npm test`（node:test，11 个用例全部通过）：

```
✔ normalizes to lowest terms with positive denominator
✔ parses fractions and integers
✔ exact arithmetic without precision loss
✔ comparison and sign
✔ rejects zero denominator and division by zero
✔ acceptance: 3-constraint maximization with exact optimum 17/4
✔ unbounded: returns an exact feasible point and recession ray
✔ infeasible: returns a Farkas certificate of infeasibility
✔ equality constraints and mixed senses solve exactly
✔ Beale's cycling example terminates under Bland's rule
✔ negative rhs rows are normalized and duals map back correctly
ℹ tests 11  ℹ pass 11  ℹ fail 0
```

## 代码结构

- `src/rational.ts` — 精确有理数类（约分、四则运算、比较）。
- `src/simplex.ts` — 两阶段单纯形求解器（Bland 规则、对偶提取、无界/不可行证书）。
- `src/example.ts` — 验收样例（含对偶价格证明输出）。
- `test/rational.test.ts`、`test/simplex.test.ts` — Node.js 内置测试。

## API

```ts
import { Rational } from './src/rational.ts';
import { solve } from './src/simplex.ts';

const result = solve({
  objective: [Rational.parse('1'), Rational.parse('1')],
  constraints: [
    { coeffs: [Rational.parse('1'), Rational.parse('3')], sense: '<=', rhs: Rational.parse('8') },
    // ...
  ],
});
// result.status: 'optimal' | 'unbounded' | 'infeasible'
```
