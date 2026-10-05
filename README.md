# 物料采购求解器 (material procurement solver)

在允许**按有理比例多级替代**、且替代可**附带辅料消耗**的前提下，求**最低成本**的
采购与消耗计划。采购数量必须为**整包**（整数），替代规则可按任意非负有理数倍施用。
所有计算使用精确有理数（`math/big.Rat`），无浮点误差。

## 模型

- 物料 `m`：可采购的物料有包规格 `packageSize` 与整包单价 `packageCost`（均须为正）；
  不给出包信息的物料只能通过替代规则获得。
- 规则 `r`：`inputs`（主材与辅料）→ `outputs`，施用量 `x_r ≥ 0` 为有理数。
- 需求 `demand[m] ≥ 0`；每种物料满足流量守恒：
  `采购量 + 产出 − 消耗 ≥ 需求`（允许多余，即免费处置）。
- 目标：最小化 `Σ 整包数 × 包单价`。整包数为整数，规则施用量为有理数，
  构成一个混合整数线性规划（MILP）。

## 求解方法

- **LP 松弛**：自带两阶段单纯形法（Bland 规则保证终止），全程精确有理数运算。
- **整数化**：对整包变量做分支定界。根节点松弛解向上取整得到初始可行解与全局上界
  `b_m ≤ floor( incumbentCost / packageCost_m )`，保证搜索树有限。
- **循环检测（关键）**：若规则的非负组合 `x ≠ 0` 使每种物料的净产出 `Vx ≥ 0`
  且至少一种物料严格为正，则该组合是自增强循环——反复施用可凭空增加可用量，
  使模型退化。求解器在求解前用一个 LP（Farkas 型证书）精确判定：

  ```
  max  Σ_m (Vx)_m
  s.t. Vx ≥ 0,  Σ_r x_r ≤ 1,  x ≥ 0
  ```

  最优值 > 0 当且仅当存在这种循环，此时拒绝整个问题并列出涉事规则
  （证书中 `x_r > 0` 的规则）。这是全局代数判定，**不依赖任何遍历深度截断**；
  恰好守恒的循环（如 1A→1B、1B→1A）不会被误杀。

## 使用

```sh
go build -o mpsolver .
./mpsolver problem.json      # 或从 stdin 读入
```

输入（数量可写数字或 `"3/2"` 这类有理数字符串）：

```json
{
  "materials": [
    {"name": "steel", "packageSize": 10, "packageCost": 42},
    {"name": "girder"}
  ],
  "demands": {"girder": 6},
  "rules": [
    {"name": "weld", "inputs": {"steel": 2, "rivet": 5}, "outputs": {"girder": 1}}
  ]
}
```

输出 `status` 为：

- `optimal`：给出 `totalCost`、各物料整包采购数 `purchases`、规则施用量
  `ruleUsage`、以及每种物料的采购/产出/消耗/需求/结余平衡表 `materials`；
- `rejected`：检测到凭空增产的循环规则，`cycleRules` 列出涉事规则（退出码 2）；
- `infeasible`：需求无法满足（退出码 1）；
- `error`：输入非法（退出码 65）。

示例见 `examples/demo.json`（含辅料与有理比例替代）与
`examples/cycle.json`（会被拒绝的增产循环）。

## 测试

```sh
go test .
```
