# exact-lp-simplex

精确有理数线性规划求解器：`max c·x`，约束 `Ax ≤ b`、`x ≥ 0`。
全部运算基于 **BigInt 有理数**，输出约分分数，不存在浮点舍入误差；
允许负 `b`、退化约束、零变量、零约束、冗余行。

## 特性

- **精确算术**：`src/rational.ts` 以 BigInt 分子/分母表示有理数，始终保持约分形式。
- **两阶段单纯形**（`src/simplex.ts`）：第一阶段引入辅助变量处理负 `b` 与可行性；
  **Bland 最小指标规则**进基/离基，从理论上保证不发生循环（Beale 示例 6 次迭代收敛）。
- **四种状态**：`optimal` / `infeasible` / `unbounded` / `iteration_limit`。
  迭代预算耗尽时单独报告，绝不伪称不可行。
- **可独立验证的证书**（`src/checker.ts` 只做代数检查，不调用求解器）：
  - `optimal`：原始可行 `x` 与对偶 `y ≥ 0`，满足 `Aᵀy ≥ c` 且 `b·y = c·x`（强对偶）；
  - `infeasible`：Farkas 证书 `y ≥ 0`，`Aᵀy ≥ 0`，`b·y < 0`；
  - `unbounded`：可行点 `x` 与射线方向 `d ≥ 0`，`Ad ≤ 0`，`c·d > 0`。
- 最优时附带 `hasAlternativeOptima`（多重最优检测，保守可靠）。

## 运行环境

- Node.js ≥ 23.6（直接运行 TypeScript），或 Node ≥ 22.6 加 `--experimental-strip-types`；
  也可以用任意 TypeScript 编译器先编译（`tsconfig.json` 已备好）。无第三方依赖。

## 命令行

```sh
# 求解（文件或 stdin 用 -）
node src/cli.ts solve examples/optimal.json
node src/cli.ts solve examples/optimal.json --budget 1000
echo '{"c":["1"],"A":[["1"]],"b":["5"]}' | node src/cli.ts solve -

# 独立验证求解结果
node src/cli.ts solve examples/optimal.json > /tmp/result.json
node src/cli.ts check examples/optimal.json /tmp/result.json
```

退出码：`0` 正常；`1` 证书验证失败；`2` 用法/输入错误；`3` 迭代预算耗尽。

## 输入格式

系数为整数（JSON number 或 `"3"`）或分数字符串（`"22/7"`、`"-3/4"`）：

```json
{
  "c": ["3", "2"],
  "A": [["1", "2"], ["2", "1"]],
  "b": ["6", "6"],
  "budget": 10000
}
```

`budget` 为可选的最大枢轴迭代次数（默认 1,000,000）。

## 输出格式

所有数值为约分分数字符串：

```json
{
  "status": "optimal",
  "iterations": 2,
  "objective": "10",
  "x": ["2", "2"],
  "y": ["1/3", "4/3"],
  "hasAlternativeOptima": false
}
```

`infeasible` 时输出 `y`（Farkas 证书）；`unbounded` 时输出 `x` 与 `direction`；
`iteration_limit` 时只输出状态与已迭代次数。

## 编程接口

```ts
import { solve } from "./src/simplex.ts";
import { checkResult } from "./src/checker.ts";
import { parseRat } from "./src/rational.ts";

const lp = {
  c: [parseRat("3"), parseRat("2")],
  A: [[parseRat("1"), parseRat("2")], [parseRat("2"), parseRat("1")]],
  b: [parseRat("6"), parseRat("6")],
};
const result = solve(lp, 10000);
const check = checkResult(lp, result); // { ok: true, violations: [] }
```

## 测试

```sh
npm test        # 或：node --test tests/*.test.ts
```

- `tests/rational.test.ts`：有理数运算、约分、解析/格式化。
- `tests/simplex.test.ts`：手工构造实例——负 `b`、退化顶点、Beale 循环示例、
  多重最优、冗余行、零变量、零约束、迭代预算、手工可行点/对偶/射线/Farkas 证书。
- `tests/checker.test.ts`：检查器拒绝各类伪造证书（负例）。
- `tests/enumeration.test.ts`：400+ 组随机小规模实例，用**精确顶点枚举**
  （独立于单纯形）交叉验证有界实例的最优值，并验证所有证书。
- `tests/cli.test.ts`：命令行端到端（solve/check/stdin/预算退出码）。

## 示例

`examples/` 内含：`optimal`（有界）、`fractional`（分数系数与分数最优值 `47/30`）、
`infeasible`、`unbounded`、`degenerate`（退化顶点）、`beale`（经典循环示例，
Bland 规则下 6 次迭代得到精确最优 `1/20`）。

## 方法说明

- 字典形式单纯形：`x_B = d - T·x_N`，`z = v + w·x_N`，全程精确有理数枢轴。
- 第一阶段：任一 `b_i < 0` 时加入辅助变量 `x0`，最大化 `-x0`；
  最优值 `< 0` 即不可行，目标行直接给出 Farkas 证书；
  最优值 `= 0` 时把留在基中的 `x0` 用退化枢轴换出（整行为零则删冗余行），进入第二阶段。
- Bland 规则：进基取既约费用为正的最小下标变量，离基取最小比值平局中的最小下标变量，
  保证有限步内终止（精确算术下不会循环）。
- 对偶提取：最优字典中 `y_i = -（松弛变量 s_i 的既约费用）`，自动满足互补松弛与强对偶。
