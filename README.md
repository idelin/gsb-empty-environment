# 电池充放电调度（精确 DP，无求解器）

按给定时段价格与需求安排电池充放电，使总电费最小。仅使用 Python 3.11
标准库（`fractions.Fraction` 精确有理数运算），不调用任何外部求解器。

## 模型约定

每个时段 `t`（时长 1 小时，功率数值即电量数值）：

- `charge c_t`：从电网买入用于充电的电量，电池实际存入 `η_c · c_t`；
- `discharge s_t`：从电池放出的电量（储能侧），实际送达负荷/电网
  `η_d · s_t`；**两个方向的效率损耗分别计量**；
- 电网购电 `g_t = demand_t + c_t − η_d · s_t`（负值表示向电网售电）；
- 费用 `cost_t = price_t · g_t`（负价格意味着买电反而获得补偿）；
- 电量守恒 `e_{t+1} = e_t + η_c·c_t − s_t`，`0 ≤ e_t ≤ capacity`；
- `e_0 = initial_energy`，**末电量必须精确等于 `final_energy`**；
- 同一时段只允许 {空闲, 充电, 放电} 之一，**禁止同段无意义双向循环**。

### 为什么禁止同段双向循环

负价格下“买电”是赚钱的。若允许同时充放，可以在同一时段充电 `c`、
放电 `s = η_c·c`，电池净电量不变，却凭空赚取
`|p|·(c − η_d·s) = |p|·c·(1 − η_c·η_d) > 0`——即“付费浪费电”的套利。
禁止同段双向循环后，每个时段的获益被功率上限严格限制，**负价格无法
造成无限获益**（总利润上界为 `Σ_t |p_t|·(demand_t + P_c)`）。

## 求解方法（精确，非近似）

1. 换元到储能侧：`c'_t = η_c·c_t`，电量平衡变为
   `e_{t+1} = e_t + c'_t − s_t`，约束均为区间约束。
2. 取所有有理数边界（容量、两个功率界、初末电量）分母的最小公倍数
   `D`，把状态离散为 `1/D` 的整数网格。固定每个时段的充/放/空闲模式
   后，可行域是整数界的最小费用流多胞形（全幺模），故最优解必落在
   该整数网格上——DP 是**精确**的。
3. 每个时段的转移是线性费用的 min-plus 卷积，用单调队列 O(状态数)
   完成；总复杂度 `O(T · 状态数)`。
4. 全程 `Fraction` 运算，结果为精确有理数。

## 结果状态（可行 / 最优 / 预算未决分离）

- `Status.OPTIMAL`：找到可证明的最优调度，附 `optimal_cost` 与逐时段
  `schedule`；
- `Status.INFEASIBLE`：容量/功率/效率约束下无法达到末电量目标；
- `BudgetStatus`（仅当传入 `budget`）：`WITHIN_BUDGET` /
  `OVER_BUDGET`；问题不可行时预算问题无意义，返回 **`UNDECIDED`
  （预算未决）**；未给预算时该字段为 `None`。

## 运行

```bash
python3.11 -m battery          # 运行内置样例（3 个场景）
python3.11 -m unittest discover -s tests -v   # 运行测试
```

代码接口：

```python
from fractions import Fraction
from battery import build_problem, solve

problem = build_problem(
    prices=[4, 2, 1, -2, 1, 3, 6, 5],   # 可为 int / float / str / Fraction
    demands=[3, 2, 2, 1, 4, 5, 6, 4],
    capacity=12, max_charge_power=6, max_discharge_power=5,
    charge_efficiency=Fraction(9, 10), discharge_efficiency="0.9",
    initial_energy=3, final_energy=3, budget=75,
)
result = solve(problem)
print(result.status, result.optimal_cost, result.budget_status)
```

## 运行样例输出

```
=== Scenario 1: 8-period arbitrage, negative price, budget ===
status        : optimal
         t      price     demand     charge  discharge  delivered       grid     energy       cost
         1          4          3          0          3      27/10       3/10          0        6/5
         2          2          2          0          0          0          2          0          4
         3          1          2          6          0          0          8       27/5          8
         4         -2          1          6          0          0          7       54/5        -14
         5          1          4        4/3          0          0       16/3         12       16/3
         6          3          5          0          0          0          5         12         15
         7          6          6          0          5        9/2        3/2          7          9
         8          5          4          0          4        18/5        2/5          3          2
optimal cost  : 458/15 (= 30.5333)
budget status : within_budget (budget=75)

=== Scenario 2: infeasible final energy (budget undecided) ===
status        : infeasible
message       : no schedule reaches the required final energy under the capacity, power and efficiency constraints
budget status : undecided (budget=0)

=== Scenario 3: negative price, cycling forbidden ===
status        : optimal
         t      price     demand     charge  discharge  delivered       grid     energy       cost
         1         -5          0          0          0          0          0          0          0
optimal cost  : 0 (= 0.0000)
note: with same-period cycling allowed, charging 5 and releasing the
stored 5/2 in the same period would earn 5*(5 - 5/4) = 75/4 from thin
air; the no-cycling rule keeps the optimum at 0.
```

## 测试结果

`python3.11 -m unittest discover -s tests -v`：**18 个测试全部通过**，覆盖：

- 手算用例：无损套利（−10）、有损往返不划算（0）、双方向效率损耗
  分别计量、需求必须满足、末电量精确达标、末电量不可达判 INFEASIBLE；
- 负价格与禁循环：同时充放永不出现、负价格利润有界（精确 −15/2）、
  负价格时段不外送电；
- 预算判定：within / over / 不可行时 undecided / 未给预算为 None；
- 输入：字符串与 Fraction 有理数、非法参数校验、float 按十进制解释；
- 调度不变量：电量守恒、功率与容量界、单行 `cost = price·grid`、
  合计等于报告最优值；
- 与独立暴力枚举在 60 个随机小算例（含负价格、有理数效率）上交叉
  验证最优值完全一致。

```
Ran 18 tests in 0.014s

OK
```

## 文件结构

- `battery/model.py` — 数据模型、输入校验、结果与状态枚举；
- `battery/solver.py` — 精确 DP 求解器（缩放 + 单调队列 + 回溯）；
- `battery/__main__.py` — 三个演示场景；
- `tests/test_battery.py` — unittest 测试（含暴力枚举交叉验证）。
