# matsolve — 物料采购与替代消耗求解器

给定物料的整包采购价格、需求数量，以及一组**有理比例、可多级级联、可附带辅料消耗**的替代规则，
求**总采购成本最小**的采购计划与替代（消耗）计划。数量必须整包、规则必须整次应用，规模有限（可配置上界）。

## 模型

- 物料 `i`：每包 `packageSize` 单位，每包成本 `packageCost`（有理数，可写 `"12"`、`"3.5"`、`"7/2"`）。
- 需求：每种物料需要的单位数（多种需求可累加）。
- 替代规则 `r`：消耗 `input.quantity` 单位 `input.material`，产出 `output.quantity` 单位
  `output.material`，并额外消耗 `aux` 列表中的辅料。替代比例即有理数
  `output.quantity / input.quantity`。规则的产出可以继续作为其他规则的输入（多级替代）。
- 决策变量（均为非负整数，且有上界）：
  - `p_i`：采购物料 `i` 的包数（≤ `limits.maxPackages`）
  - `y_r`：规则 `r` 的应用次数（≤ `limits.maxApplications`）
- 约束（每种物料的收支平衡，允许多余浪费）：

  ```
  packageSize_i · p_i + Σ_r M_ir · y_r ≥ demand_i
  ```

  其中 `M_ir` 是规则 `r` 对物料 `i` 的净改变量（产出为正，投入与辅料为负）。
- 目标：`min Σ_i packageCost_i · p_i`。

## 凭空增益循环的检测（不做深度截断）

若存在一组非负（有理数）规则应用强度 `y ≠ 0`，使得净效应 `M·y ≥ 0` 且至少一个分量严格大于 0，
则这组规则可以**凭空放大可用量**（从任意种子出发反复循环即可得到无界物料），问题实例必须被拒绝。
由于 `M` 是整数矩阵，有理数见证 `y` 通分后即得到整数次循环，因此"有理数意义下存在"与
"整数次应用意义下存在"完全等价。

检测不遍历规则图、**没有任何深度截断**，而是精确求解线性规划：

```
max  Σ_i (M y)_i
s.t. 0 ≤ (M y)_i ≤ 1   （每种物料 i）
     y ≥ 0
```

该 LP 恒可行（`y = 0`）且有界（目标 ≤ 物料数），最优值 > 0 当且仅当增益组合存在；
此时把最优解中强度为正的规则名列入错误信息。其 Farkas 对偶含义是：
不存在增益循环 ⟺ 存在一套全正物料"价格"，使每条规则都不产生正净价值。

所有计算使用 `math/big.Rat` **精确有理数运算**（两阶段单纯形法 + Bland 轮换规则保证终止），
不会因浮点误差在比例乘积接近 1 时误判。检测能抓到：

- 任意长的增益环（如 6 跳、比例乘积为 2 的环）；
- **没有简单环**的复合增益（例如 `1a→2b`、`1a→2c`、`3b+3c→4a`，任何单条或两条规则
  组合都不增益，但三条联立 `(3,3,2)` 净产 `+2a`）——基于简单环遍历/深度截断的方法无法发现；
- 同时放行无害的守恒环（`1a→1b`、`1b→1a`）与损耗环（`2a→1a`）。

## 优化算法

分支定界（branch-and-bound）：每个节点用精确单纯形法解 LP 松弛得下界，
对第一个分数变量二分分支；所有变量有界故搜索有限。
`limits.maxNodes` 限制节点数：若超限前已找到可行方案，则返回该方案并置 `proven=false`；
否则返回 `ErrNodeLimit`。若在规模上界内不存在满足需求的方案，返回 `ErrInfeasible`。

## 用法

```sh
go run ./cmd/matsolve examples/chain.json     # 文本输出
go run ./cmd/matsolve --json examples/aux.json
matsolve < problem.json                       # 从 stdin 读
go test ./solver/                             # 运行测试
```

检出增益循环时程序以退出码 2 拒绝，并在 stderr 打印涉及的规则名。

## 输入格式

```json
{
  "materials": [
    {"name": "ore",   "packageSize": 4, "packageCost": "6"},
    {"name": "ingot", "packageSize": 1, "packageCost": "100"},
    {"name": "part",  "packageSize": 1, "packageCost": "100"}
  ],
  "demands": [{"material": "part", "quantity": 3}],
  "rules": [
    {"name": "smelt",   "input": {"material": "ore", "quantity": 1},
     "output": {"material": "ingot", "quantity": 2}},
    {"name": "machine", "input": {"material": "ingot", "quantity": 1},
     "output": {"material": "part", "quantity": 1},
     "aux": [{"material": "catalyst", "quantity": 1}]}
  ],
  "limits": {"maxPackages": 1000, "maxApplications": 1000, "maxNodes": 100000}
}
```

- `aux` 可省略；`limits` 整体可省略，各项为 0 时取默认值（1000 / 1000 / 100000）。
- 输出包含：总成本、每种物料的采购包数与单位数、每条规则的应用次数、
  每种物料的收支平衡（采购/产出/消耗/需求/剩余）。

## 代码结构

- `solver/simplex.go` — 精确有理数两阶段单纯形法（Bland 规则）。
- `solver/cycle.go` — 增益循环检测 LP。
- `solver/milp.go` — 分支定界整数求解与模型装配。
- `solver/solver.go` — 类型、校验、编排、方案构造。
- `cmd/matsolve/main.go` — 命令行入口。
- `examples/` — 示例实例（含一个会被拒绝的增益循环）。
