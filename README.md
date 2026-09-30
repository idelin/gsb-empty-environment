# symbreak：有限域 CSP 的对称破缺（字典序代表约束）

纯 Python 3.11 标准库实现（仅 `unittest` 做测试），**不调用任何 SAT/SMT 库**，
求解器为普通回溯枚举，面向小规模可枚举问题。

## 功能与保证

1. **先验证，后破缺**：用户给定的置换必须通过 `verify_permutation` 证明是
   约束与目标的自同构，否则拒绝并给出反例：
   - 特殊角色变量（`Variable(..., special=True)`）必须是不动点；
   - 每个变量与其像的域必须完全相同（域不同直接拒绝对称声明）；
   - 每条约束在置换前后真值一致（只枚举 `scope ∪ p⁻¹(scope)` 上的赋值）；
   - 目标函数（若有）在置换前后取值一致（枚举完整赋值）。
2. **最小反例**：错误置换返回按（非默认取值个数, 域下标元组）序最小的
   不保持约束的赋值。
3. **字典序代表约束只使用已证对称**：
   - `declare_exchangeable` 验证全部相邻对换（生成 Sₙ）后，才追加链式约束
     `x1 <= x2 <= ... <= xn`；
   - `lex_leader_constraint` 为单个已验证置换生成 `x <=lex p(x)`，
     未通过验证直接抛 `ValueError`；
   - 未声明对称的变量绝不施加任何排序约束。
4. **生成器闭包按预算构造**：`group_closure` BFS 构造，超出预算抛
   `BudgetExceeded`。
5. **小问题穷举验证**：`verify_orbit_coverage` 枚举全部解，验证
   每个轨道（等价类）至少保留一个解、每个被删解都可由保留解经群元素恢复；
   `recover_solutions` / `find_recovery` 输出恢复对称解的方法与证人置换。
6. **独立检查器**：`check_solution` 只针对原始约束与域逐条求值，
   与求解器逻辑完全独立；声称有解时应用它复核。

## 目录结构

```
symbreak/
  csp.py         # Variable / Constraint / Problem 建模
  permutation.py # 置换：双射校验、复合、求逆、作用于赋值
  verify.py      # 自同构验证 + 最小反例 + BudgetExceeded
  group.py       # 生成元闭包（按预算）
  lexleader.py   # 可交换组声明 + lex-leader 约束生成
  solver.py      # 回溯求解器 + 独立检查器
  orbits.py      # 轨道、覆盖验证、解恢复
examples/demo.py # 运行样例
tests/test_symbreak.py  # unittest 测试（24 个）
```

## 快速开始

```python
from symbreak import Variable, Constraint, Problem, declare_exchangeable, \
    solve_all, check_solution, verify_orbit_coverage, recover_solutions

names = ["x1", "x2", "x3", "x4"]
problem = Problem(
    variables=tuple(Variable(n, (0, 1, 2)) for n in names),
    constraints=(
        Constraint("sum==4", tuple(names), lambda a, b, c, d: a+b+c+d == 4),
        Constraint("at-most-one-2", tuple(names),
                   lambda a, b, c, d: sum(v == 2 for v in (a, b, c, d)) <= 1),
    ),
    objective=lambda a: sum(a[n]**2 for n in names), sense="min",
)

result = declare_exchangeable(problem, names)   # 先验证相邻对换，再加链约束
assert result.ok                                # 失败时 result.counterexample 给出最小反例

report = verify_orbit_coverage(problem, result.group)
assert report.ok                                # 每轨道至少一解，被删解均可恢复

retained = solve_all(problem, use_lex=True)     # 代表解
assert all(check_solution(problem, s) for s in retained)   # 独立检查器复核
full = recover_solutions(result.group, retained)           # 恢复全部对称解
```

## 运行样例

```
$ python3.11 examples/demo.py
==================================================================
1. 可交换变量组：验证 -> 字典序链约束 -> 轨道覆盖验证 -> 恢复
==================================================================
可交换组验证通过，生成约束: ['lex:x1<=x2', 'lex:x2<=x3', 'lex:x3<=x4']
群闭包大小 |S4| = 24
原始解 13 个，保留 2 个，删除 11 个，轨道数 2
每轨道至少一解且被删解均可恢复: True
保留的代表解:
  {'x1': 0, 'x2': 1, 'x3': 1, 'x4': 2}  (独立检查器: True)
  {'x1': 1, 'x2': 1, 'x3': 1, 'x4': 1}  (独立检查器: True)
被删解示例: {'x1': 0, 'x2': 1, 'x3': 2, 'x4': 1}
  恢复证人: 置换 Perm(x2->x4, x3->x2, x4->x3) 作用于保留解 {'x1': 0, 'x2': 1, 'x3': 1, 'x4': 2}
recover_solutions 恢复出 13 个解，与原始解集一致: True
最优保留解 {'x1': 1, 'x2': 1, 'x3': 1, 'x4': 1}，目标值 4，独立检查器复核: True

==================================================================
2. 错误置换：返回最小不保持约束的反例
==================================================================
约束 x1<x2 下交换 x1,x2 -> 验证通过? False
  最小反例: {'x1': 0, 'x2': 1}  (约束 x1<x2)
  说明: 约束 x1<x2 在赋值 {'x1': 0, 'x2': 1} 下不被保持：原取值为 True，置换后为 False
目标 f=x1 下交换 x1,x2 -> 验证通过? False
  最小反例: {'x1': 0, 'x2': 1}  (kind=objective)

==================================================================
3. 拒绝非法对称声明：不同域 / 特殊角色 / 非双射
==================================================================
域不同的组 -> 接受? False  原因: 变量 b 的域 (0, 1, 2) 与 a 的域 (0, 1) 不同，拒绝对称声明
含特殊角色的组 -> 接受? False  原因: 特殊角色变量不得参与交换: ['boss']
置换特殊角色的置换 -> 通过? False  反例: 特殊角色变量 boss 被置换为 w1，特殊角色不得被交换
非双射映射 -> 构造即拒绝: 给定映射不是双射，拒绝作为对称置换

==================================================================
4. 只使用已证对称 + 闭包预算
==================================================================
未证对称的置换 -> 拒绝生成约束:
  置换未通过自同构验证，拒绝生成字典序约束: Counterexample(kind='objective', ...)
预算 10 -> 群闭包超过预算 10 个元素（已发现 11 个）
预算 100 -> 闭包构造成功，|S4| = 24

==================================================================
5. 未声明对称的变量不被强行排序
==================================================================
仅对 x1,x2 追加约束: ['lex:x1<=x2']
保留解 4 个；其中 y > x1 的解存在: True（y 未被排序）
  示例: {'x1': 0, 'x2': 0, 'y': 2}
```

## 测试结果

```
$ python3.11 -m unittest discover -s tests -v
test_chain_constraints_added_only_after_verification ... ok
test_closure_size_and_budget ... ok
test_deleted_solutions_recoverable ... ok
test_different_domains_rejected ... ok
test_every_orbit_keeps_at_least_one_solution ... ok
test_exhaustive_check_loses_no_class ... ok
test_group_violating_constraint_rejected_with_counterexample ... ok
test_group_with_different_domains_rejected ... ok
test_group_with_special_role_rejected ... ok
test_independent_checker ... ok
test_lex_leader_constraint_for_proven_permutation ... ok
test_lex_leader_requires_proven_symmetry ... ok
test_no_sat_smt_libraries_used ... ok
test_non_bijection_rejected ... ok
test_objective_not_preserved_rejected ... ok
test_orbit_of_solution ... ok
test_special_role_not_swappable ... ok
test_undeclared_variables_not_ordered ... ok
test_valid_automorphism_accepted ... ok
test_verify_budget_exceeded ... ok
test_wrong_permutation_minimal_counterexample ... ok
（共 24 个测试，全部通过）
```
