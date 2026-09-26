# storage-scheduler

按给定时段电价与需求，精确安排储能（电池）的充放电计划。纯 Python 3.11
标准库实现（`unittest` 为测试框架），**不调用任何外部求解器**；所有算术
使用 `fractions.Fraction`，对整数/有理数输入给出精确结果。

## 问题模型

- `T` 个时段，每时段电价 `p_t`（可为负）与需求 `demand_t ≥ 0`。
- 需求必须由电池放电满足；超出需求的放电按当时电价卖回电网（收益）。
- 充电从电网买入：买 `g` 单位电网能量，存入 `η_c · g`（**充电方向损耗**）。
- 放电：取出 `d` 单位储能，交付 `η_d · d`（**放电方向损耗**）。
- 约束：容量 `0 ≤ SoC ≤ C`；充电功率（电网侧）≤ `P_c`；放电功率
  （交付侧）≤ `P_d`；初始电量 `E_0`；**末电量目标必须满足**
  `SoC_T ≥ E_final`。
- **禁止同段无意义双向循环**：每时段充电量与放电量不得同时为正
  （`charge_t · discharge_t = 0`）。负电价下同段买卖的"空转"本可凭空
  获利，该约束从结构上杜绝了这一点；同时容量与功率限制使负电价收益
  必然有界（**负价格不能造成无限获益**）。

## 算法（非求解器，手工实现）

1. 建模为**最小费用循环流**（stored-energy 为单位）：充电弧、放电弧
   （带需求下界）、跨时段储能弧（流量即 SoC，容量 C）、初/末电量弧。
2. 可行性：下界变换 + Edmonds-Karp 最大流。
3. 最优性：负费用圈消除（Bellman-Ford 找负圈），有理数据下必有限终止。
4. 同时充放约束：
   - 需求时段必须放电 ⇒ 该时段充电弧直接移除；
   - 非负电价时段，同时充放绝不更优（`η_c·η_d ≤ 1`），且流结构不会产生；
   - 负电价零需求时段的方向选择是离散决策，枚举"只充/只放"两种模式
     （`2^k` 种配置），取最优。

## 结果分类（可行 / 最优 / 预算未决 分开报告）

- `optimal`：已证明的最优调度（含目标值与逐时段计划）。
- `feasible`：已有可行调度，但计算预算耗尽，最优性未证明。
- `infeasible`：已证明无可行调度（含末电量目标不可达）。
- `undecided`：预算不足以判定可行性。
- 预算查询（`--budget B`："是否存在成本 ≤ B 的调度？"）独立回答
  `yes` / `no` / `undecided`（预算未决）。

预算参数：`--iteration-budget`（负圈消除总迭代数）、`--config-budget`
（负电价模式枚举上限）。

## 输入格式（JSON）

数值可写整数、小数或 `"分子/分母"` 形式的有理数字符串：

```json
{
  "capacity": 6,
  "charge_efficiency": "9/10",
  "discharge_efficiency": "9/10",
  "max_charge_power": 5,
  "max_discharge_power": 5,
  "initial_energy": 0,
  "final_energy": 0,
  "prices": [1, 4, 1, 4],
  "demands": [0, 2, 0, 2]
}
```

## 运行样例

```
python -m storage_scheduler examples/arbitrage.json
python -m storage_scheduler examples/arbitrage.json --budget -20
python -m storage_scheduler examples/infeasible.json
python -m storage_scheduler examples/negative_prices.json
python -m storage_scheduler examples/budget.json --budget -1 --iteration-budget 0
python -m storage_scheduler examples/arbitrage.json --json
```

`examples/arbitrage.json` 输出（低买高卖 + 满足需求，目标值精确为 -112/5）：

```
Status: optimal
Net cost (objective): -112/5  (~-22.4000)
Iterations: 2, configs: 1/1
Period | Price | Demand | Charge(grid) | Discharge(delivered) | SoC
-------+-------+--------+--------------+----------------------+----
     1 |     1 |      0 |            5 |                    0 | 9/2
     2 |     4 |      2 |            0 |                81/20 |   0
     3 |     1 |      0 |            5 |                    0 | 9/2
     4 |     4 |      2 |            0 |                81/20 |   0
```

`examples/negative_prices.json`（负电价：只充不空转，收益有界）：

```
Status: optimal
Net cost (objective): -6  (~-6.0000)
Iterations: 3, configs: 4/4
Period | Price | Demand | Charge(grid) | Discharge(delivered) | SoC
-------+-------+--------+--------------+----------------------+----
     1 |    -2 |      0 |            0 |                    0 |   0
     2 |    -3 |      0 |            2 |                    0 |   1
```

`examples/infeasible.json`：`Status: infeasible`（需求超过可用能量，
且需求时段禁止同时充电）。

预算未决示例（`--iteration-budget 0` 时只拿到可行解，无法判定预算）：

```
Status: feasible
Net cost (objective): 0  (~0.0000)
Budget -1: undecided
```

## 测试

```
python -m unittest discover -s tests -v
```

测试结果（19 个用例全部通过）：

```
Ran 19 tests in 0.031s

OK
```

覆盖：低买高卖最优性、容量/功率约束、充放方向分别计损耗、有理数精确
目标值、末电量目标必达（含不可达判 infeasible）、需求不可行、负电价
下同段禁止双向循环且收益有界、负电价模式枚举、预算 yes/no/undecided
三态、迭代与配置预算耗尽时的 feasible/undecided 状态、与整数暴力搜索
交叉验证、CLI 文本与 JSON 输出。

## 目录结构

- `storage_scheduler/model.py` — 实例数据模型与校验（Fraction 精确解析）
- `storage_scheduler/mincostflow.py` — 最小费用循环流（下界变换 + 最大流 + 负圈消除）
- `storage_scheduler/solver.py` — 网络构建、模式枚举、结果分类与校验
- `storage_scheduler/cli.py` — 命令行接口
- `examples/` — 四个示例实例
- `tests/test_solver.py` — 19 个 unittest 用例
