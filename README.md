# pipeline-divergence

精确比较两条数值计算流水，定位**首个**舍入 / 单位 / 分母口径分歧，给出可复算的精确差异链。
全程基于 BigInt 有理数，**不经过 Number 中转**；舍入规则显式；不使用任何容差。

## 运行

需要 Node ≥ 23（直接执行 TypeScript + 内置 `node:test`）。`run.sh` 会自动从 nvm 里挑一个：

```sh
./run.sh test   # 33 个测试
./run.sh demo   # 6 个场景演示
./run.sh all
```

## 方法

1. **精确求值**（`src/dag.ts`）：流水 = 运算 DAG（const/input/add/sub/mul/div/neg/round/convert/output）。
   每个节点的中间值是精确 `Rational`（`src/rational.ts`，BigInt 分子分母）+ 量纲向量（`src/units.ts`）。
   量纲不匹配（如 m+s）记为硬错误，永不以数值相近掩盖。
2. **DAG 对齐**（`src/align.ts`）：先做规范化（`sub→add(neg)`、add/mul 按结合律+交换律扁平化排序、
   双重否定消除），再按**形状**对齐节点；输出按名字、输入按槽位做来源对齐。
   因此 `(a+b)+c` 与 `a+(c+b)` 对齐，**绝不简单比较第 N 个节点**。
   形状忽略 round 的参数与 const 的数值——它们是"规则"，留给下一步比较。
3. **分歧定位**（`src/compare.ts`）：按拓扑序找到首个精确值/量纲不同的对齐节点对；
   根因 = 局部规则不同（舍入 mode/places、常数值、单位换算因子、convert 目标、结构差异）
   且上游无其他规则差异的最小集合；分母口径（÷N vs ÷(N−1)）识别为 `denominator`。
   输出从根因到每个分歧输出的**精确差异链**（每步含两侧精确值与精确差值 delta）。
4. **反事实确认**（`src/counterfactual.ts`）：把 B 的规则替换进 A（舍入规则 / 常数 / 单位因子 /
   convert 目标 / 旁路多余舍入级），**独立重算**，输出精确相等则确认该规则即原因。
5. **最小反例搜索**（`src/search.ts`）：对有限有理输入空间（|p|≤N, q≤M 的既约分数，按高度
   |p|+q 枚举、多槽位对角线枚举）自动搜索最小反例。三种终态：
   `counterexample-found` / `no-divergence-in-space`（空间枚举完）/ `undecided-budget-exhausted`
   （预算耗尽，**绝不**把"没搜到"当作"等价"）。
6. **诚实状态**：最终结果相同但中间不同 → `intermediate-divergence`（照常记录）；
   输出不同但中间证据缺失（`keepTrace` 模拟只暴露部分中间值的外部流水）→ `cannot-locate`。

## 舍入规则（显式定义，`src/rounding.ts`）

`half-up`（平局向 +∞）、`half-down`（向 −∞）、`half-even`（银行家）、
`toward-zero`、`away-from-zero`、`floor`、`ceil`；`places` 可为负（如 -2 舍入到百位）。

## 场景演示（`src/cli.ts`）

1. 舍入口径：`half-up` vs `half-even`，x=0.125 → 13/100 vs 3/25，反事实替换后精确相等确认。
2. 分母口径：均值 ÷N vs ÷(N−1)，两条流水的求和 DAG 重新结合仍正确对齐。
3. 单位换算因子：1in = 127/5000 m vs 近似 1/40 m → `unit-conversion-factor`。
4. 最终相同但中间不同：两级舍入 `places=2` vs `places=3` → `intermediate-divergence`。
5. 中间证据缺失：B 只暴露输入与输出 → `cannot-locate`。
6. 最小反例搜索：`half-up` vs `half-even` 在 2 位小数下的最小反例 x=1/8（高度 9 的最小平局点）；
   预算 5 → `undecided-budget-exhausted`。

## 目录

```
src/rational.ts        BigInt 精确有理数（解析/运算/精确十进制输出）
src/rounding.ts        显式舍入规则
src/units.ts           量纲向量 + 精确换算表
src/dag.ts             流水 DAG 定义与精确求值（trace/证据保留）
src/align.ts           规范化 + DAG 对齐
src/compare.ts         分歧定位、根因分类、精确差异链
src/counterfactual.ts  规则替换 + 独立重算确认
src/search.ts          有限有理输入的最小反例搜索（带预算）
src/cli.ts             场景演示
test/                  node:test 测试（33 个）
```
