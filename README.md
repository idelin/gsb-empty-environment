# 保质期转运网络（Shelf-Life Transshipment）

在有限整数时间轴 `0..horizon` 上，为多个带保质期的货物批次求满足需求的
最小成本运输流。纯 Python 3.11 标准库实现，无第三方依赖。

## 模型规则

- 每个批次有：数量、起点、终点、出发（就绪）时间、失效时间，均为整数。
- 有向运输边有：时长（≥1）、容量、单位整数成本（≥0）。
- 容量按「边 × 出发时刻」时隙计费，由所有批次共享；仓库节点可免费等待。
- 批次可在出发时间或之后离开起点；**到达时刻等于失效时刻即视为过期**，
  因此批次途经的任何时刻必须 `<= expiry - 1`。
- 批次不可混淆：每个单位始终受本批次的失效约束（每批次一个商品的
  多商品流）。

## 求解方法（精确）

时间扩展网络是 DAG。对每批枚举全部节点简单路径（等待隐含在相邻腿之间），
再对整数路径流做带下界剪枝的分支定界。非负成本下最优解无环，任意可行
整数流可分解为路径流，因此路径形式化是完备的：搜索正常结束时结果
**已证最优**（`status = "optimal"`），不可行结论（`"infeasible"`）同样被证明。
若触发 `--max-paths` / `--max-search-nodes` 限制，则状态为
`feasible_unproven` 或 `unknown`，并在 `notes` 中说明未证最优/未证不可行。

## 运行

```bash
python3.11 shelf_life_flow.py problem.json     # 从文件读入
python3.11 shelf_life_flow.py < problem.json   # 从标准输入读入
python3.11 shelf_life_flow.py --example        # 打印示例问题
python3.11 -m unittest test_shelf_life_flow -v # 运行测试
```

## 输入格式（JSON）

```json
{
  "horizon": 5,
  "edges": [
    {"from": "A", "to": "B", "duration": 1, "capacity": 1, "cost": 2},
    {"from": "B", "to": "C", "duration": 1, "capacity": 1, "cost": 3}
  ],
  "batches": [
    {"id": "b1", "origin": "A", "destination": "C",
     "quantity": 1, "depart": 0, "expiry": 3}
  ]
}
```

- `horizon`：时间轴上界（整数，含）。
- `edges[]`：`from`/`to` 节点名，`duration` 时长，`capacity` 每个出发
  时隙的容量，`cost` 单位成本。
- `batches[]`：`quantity` 需求数量，`depart` 出发（就绪）时间，
  `expiry` 失效时间（到达必须严格小于它）。

## 输出格式（JSON）

```json
{
  "status": "optimal",
  "optimal": true,
  "total_cost": 5,
  "batches": [
    {"id": "b1", "quantity": 1, "paths": [
      {"amount": 1, "unit_cost": 5, "legs": [
        {"edge": 0, "from": "A", "to": "B", "depart": 0, "arrive": 1, "cost": 2},
        {"edge": 1, "from": "B", "to": "C", "depart": 1, "arrive": 2, "cost": 3}
      ]}
    ]}
  ],
  "edge_usage": [
    {"edge": 0, "from": "A", "to": "B", "depart": 0, "arrive": 1,
     "used": 1, "capacity": 1}
  ],
  "checks": {"quantities_satisfied": true, "paths_valid": true,
             "expiry_respected": true, "capacities_respected": true,
             "all": true},
  "notes": []
}
```

- `status`：`optimal`（已证最优）/ `infeasible`（已证不可行）/
  `feasible_unproven`（有解但未证最优）/ `unknown`（未找到解且未证不可行）。
- `batches[].paths[]`：每批的路径分解，`amount` 为该路径承载的数量，
  `legs` 为逐腿的起讫节点与出发/到达时刻（相邻腿之间的时间差即等待）。
  路径首尾相接、起点为批次起点、终点为批次终点，即流量守恒。
- `edge_usage[]`：每个「边 × 出发时隙」的用量与容量，用于核对容量守恒。
- `checks`：求解器内置校验（数量满足、路径合法/守恒、失效约束、容量约束）。

## 测试

`test_shelf_life_flow.py` 覆盖：验收边界（到达=失效不可用、两段各 1 时长
在 2 到达可用、容量 1 不能运 2 单位）、容量按出发时隙语义、批次失效约束
不混淆、等待让位、CLI 端到端，以及 60 个随机小例与独立的逐单位暴力枚举
交叉核验最优值。所有数据均为合成数据。
