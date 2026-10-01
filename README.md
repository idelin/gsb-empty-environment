# 保质期转运网络（Perishable Transshipment Network）

在时间展开的整数时间轴上，求满足全部批次需求的最小成本运输流。
纯 Python 3.11 标准库实现，精确（指数级）求解，适合小规模实例与枚举核验。

## 模型

- **有向运输边**：正整数时长 `duration`、每单位时间槽容量 `capacity`、
  非负整数成本 `cost`（按每单位货物计）。时刻 `t` 出发，`t + duration` 到达。
- **批次货物**：数量 `quantity`、出发时间 `departure`、失效时间 `expiry`。
  到达目的地的时刻必须**严格小于**失效时间（到达 == 失效视为过期）。
- **等待**：货物可在任意节点（含起点）免费、无限量等待。
- **容量**：按 `(边, 出发时刻)` 计，所有批次共享；容量 1 的时间槽不能运输 2 单位。
- **批次不混淆**：每批保留各自失效约束，求解按批分别枚举路径再统一分配容量。

## 运行

```bash
python3.11 perishable_flow.py solve examples/acceptance.json
python3.11 perishable_flow.py solve input.json -o solution.json --max-paths 100000
python3.11 perishable_flow.py verify input.json solution.json
python3.11 -m unittest test_perishable_flow -v
```

退出码：`solve` 最优=0，不可行=1，中止/输入非法=2；`verify` 通过=0，失败=1。

## 输入格式（JSON）

```json
{
  "horizon": 10,            // 可选，时间轴上界；默认取 max(expiry)-1
  "edges": [
    {"id": "e1", "from": "A", "to": "B", "duration": 1, "capacity": 1, "cost": 1}
  ],
  "batches": [
    {"id": "b0", "origin": "A", "destination": "C", "quantity": 1,
     "departure": 0, "expiry": 3}
  ]
}
```

## 输出格式（JSON）

- `status`: `optimal`（已证最优）/ `infeasible`（已证不可行）/ `aborted`
  （路径数超限中止，**未证最优**）。
- `total_cost`: 总成本；`optimal`/`verified`: 最优性与核验标记。
- `batches[].routes[]`: 每批路径，`amount` 单位数，`steps` 为
  `{edge, from, to, depart, arrive}` 序列（等待由时间差隐含），`arrival` 到达时刻。
- `edge_usage[]`: 每条 `(边, 出发时刻)` 的 `used`/`capacity`，`capacity_ok` 为容量守恒结论。

示例输出节选（`examples/acceptance.json`）：直达边时长 3 等于失效时刻不可用，
最优解走 `e1 -> e2`，时刻 2 到达，`total_cost = 2`。

## 算法与最优性

1. 时间展开 DAG（时间严格递增，无环）上为每批枚举全部可行路径；
2. 深度优先 + 分支限界在“单位货物 -> 路径”分配空间上精确搜索，下界为各批最小路径成本；
3. 搜索完整结束即证最优；`--max-paths` 触发中止时输出 `aborted` 并明确标注未证最优。

复杂度为指数级（多商品整数流本身 NP 难），适用于小规模精确求解。
测试中用独立的暴力枚举（`itertools` 笛卡尔积 + 可重复组合）对 60 个随机小例
逐一交叉验证求解器结果，并对每个解运行独立核验器（需求守恒、路径连通与时序、
过期约束、容量守恒、总成本一致性）。

所有示例与测试数据均为合成数据。
