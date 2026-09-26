# maintopt — 维修窗口选择（合成数据，无求解器）

在维修窗口内选择设备维修集合并排程。全部约束数据为合成整数或有理数；
风险分数仅作题内权重，**不解释为真实失效概率**。不调用任何外部求解器，
仅用 Python 3.11 标准库实现精确分支定界。

## 模型

- 窗口为整数时槽 `0 .. horizon-1`，`blocked_slots` 为关闭槽位。
- 每台设备：维修工时 `duration`（整数槽）、权重 `score`（有理数
  `Fraction`）、每段启动成本 `startup_cost`、所属机组 `group`。
- 同组同时维修数不超过 `group_limits[group]`（逐槽检查）。
- 任务可中断：槽位被分成 k 段则计 k 次启动成本（中断恢复计额外启动）。
- 总启动成本不超过 `budget`；目标为最大化已完整修完设备的分数之和。

## 三种结果状态（互相分开）

- `OPTIMAL`：搜索空间穷尽，返回方案已证最优。
- `FEASIBLE`：节点预算耗尽，返回已找到的最优可行方案（未证最优，
  附带上界 `bound`）。
- `BUDGET_UNDECIDED`：节点预算耗尽且尚未评估出任何可行方案。

可行性校验独立于优化器：`maintopt.verify.check_feasible(instance, plan)`
返回违规列表（空列表即可行）。

## 运行

```bash
python3.11 -m maintopt.demo    # 或 python3.11 main.py
python3.11 -m unittest discover -v
```

## 运行样例（实际输出）

```
instance: horizon=6, limits={mech:1, elec:2}, budget=6, blocked={2}
  pump     group=mech  duration=2 score=5 startup=2
  valve    group=mech  duration=1 score=2 startup=1
  motor    group=elec  duration=3 score=7 startup=2
  sensor   group=elec  duration=2 score=9/2 startup=1
  battery  group=elec  duration=1 score=3/2 startup=1

--- full search (node_budget=1000000) ---
status=OPTIMAL  nodes=581/1000000
score=37/2  cost=6  bound=None
  motor    slots=(3, 4, 5) segments=1
  pump     slots=(0, 1) segments=1
  sensor   slots=(0, 1) segments=1
  valve    slots=(3,) segments=1
verifier: OK

--- tight node budget (node_budget=4) ---
status=FEASIBLE  nodes=5/4
score=12  cost=6  bound=20
  motor    slots=(0, 1, 3) segments=2   # 中断恢复，多计一次启动成本
  pump     slots=(0, 1) segments=1
verifier: OK

--- no node budget (node_budget=0) ---
status=BUDGET_UNDECIDED  nodes=1/0
plan=<undecided>
```

## 测试结果

```
Ran 20 tests in 0.002s
OK
```

覆盖：组同时上限、预算约束、中断分段额外启动成本、不完整任务/越界/
封闭槽拒绝、有理数分数精确求和、权重非概率解释（>1 合法）、已知最优解、
与独立暴力枚举对照、三种状态区分。

## 结构

- `maintopt/model.py` — 数据模型（`Device` / `Instance`）
- `maintopt/verify.py` — 独立可行性校验、成本/分数/段数计算
- `maintopt/planner.py` — 分支定界 `solve()`，三态 `SearchResult`
- `maintopt/demo.py` — 运行样例
- `tests/` — unittest 测试
