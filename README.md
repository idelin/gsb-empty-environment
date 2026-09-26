# 双资源背包 + 互斥组子集选择

在两种资源容量与互斥组约束下选择最大收益子集。纯 Python 3.11 标准库实现
（`fractions.Fraction` 精确运算），不调用任何外部求解器；测试使用 `unittest`。

## 规则

- 两种容量必须**同时**满足，只满足一种不算可行。
- 收益、消耗、容量支持整数或有理数（`Fraction`），全程精确比较。
- 互斥组内最多选择一个物品。
- 零成本正收益物品会被选中；负收益、零收益物品可不选。
- 结果区分两种状态，可行性与最优性分开报告：
  - `optimal`：搜索完成，已证明最优；
  - `budget_undecided`：节点预算（`max_nodes`）耗尽，返回预算内最优**可行**解，
    最优性未决，未选原因中相应标注“非已证明最优”。
- 每个未选物品给出原因：`exceeds_capacity`（单件超容量）、`negative_profit`、
  `no_benefit`、`group_conflict`、`insufficient_capacity`、`not_in_optimal`。

## 文件

- `subset_select.py`：求解模块，`solve(items, capacity1, capacity2, max_nodes=None)`。
- `demo.py`：运行样例。
- `test_subset_select.py`：单元测试（含随机数据对暴力枚举对拍）。

## 运行样例

```
$ python3.11 demo.py
== 样例一：整数+有理数混合，证明最优 ==
状态: optimal（可行: True, 已证明最优: True）
总收益: 25  资源占用: 9/10, 5/8  访问节点: 22
选中:
  + alpha: 收益=10 消耗=(4, 3) 组=None
  + beta: 收益=9 消耗=(5, 2) 组=g1
  + epsilon: 收益=6 消耗=(0, 0) 组=None
未选及原因:
  - gamma: [group_conflict] 互斥组 'g1' 已选择 'beta'
  - delta: [insufficient_capacity] 剩余容量不足：资源1剩余 1 < 需要 5/2
  - zeta: [negative_profit] 负收益物品，选择只会降低总收益
  - eta: [exceeds_capacity] 单件即超容量：资源1需要 50 > 容量 10
  - theta: [no_benefit] 零收益物品，选择不增加总收益

== 样例二：24 件合成物品，节点预算 50（预算未决） ==
状态: budget_undecided（可行: True, 已证明最优: False）
总收益: 147  资源占用: 40/40, 31/36  访问节点: 51
（未选原因均标注“基于预算内最优可行解，非已证明最优”）

== 样例三：同一问题不限预算，证明最优 ==
状态: optimal（可行: True, 已证明最优: True）
总收益: 148  资源占用: 40/40, 33/36  访问节点: 373
```

样例二在预算内得到可行收益 147；样例三证明该问题最优为 148，体现
“可行 / 最优 / 预算未决”三者的区分。

## 测试结果

```
$ python3.11 -m unittest -v
test_random_integer_cases ... ok        # 60 组随机整数数据对拍暴力枚举
test_random_rational_cases ... ok       # 30 组随机有理数数据对拍暴力枚举
test_both_capacities_required ... ok    # 只满足一种容量不可行
test_known_optimum ... ok               # 已知最优解 25
test_mutex_group_at_most_one ... ok     # 互斥组最多一个
test_negative_profit_optional ... ok    # 负收益可不选
test_rational_exactness ... ok          # 1/3+1/3+1/3 == 1 精确判定
test_zero_cost_positive_profit_selected ... ok
test_zero_profit_no_benefit ... ok
test_budget_undecided_reason_marked ... ok
test_budget_undecided_status ... ok     # 预算未决：可行、收益不超最优
test_optimal_status_when_unlimited ... ok
test_duplicate_names_rejected ... ok
test_empty_inventory ... ok
test_negative_capacity_rejected ... ok
test_negative_cost_rejected ... ok

----------------------------------------------------------------------
Ran 16 tests in 0.080s

OK
```

## 用法

```python
from fractions import Fraction
from subset_select import Item, solve

items = [
    Item("a", profit=10, cost1=4, cost2=3),
    Item("b", profit=9, cost1=5, cost2=2, group="g1"),
    Item("c", profit=Fraction(15, 2), cost1=Fraction(5, 2), cost2=2),
]
result = solve(items, capacity1=10, capacity2=8, max_nodes=None)
print(result.status, result.total_profit)
for info in result.unselected:
    print(info.item.name, info.reason.value, info.detail)
```
