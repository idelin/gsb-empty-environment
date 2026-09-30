# lexopt — 有限域分层词典序约束优化器

纯 Python 标准库实现（3.11+，无 SAT/SMT 依赖），unittest 测试。

## 语义保证

- **硬约束必须满足**：硬约束不带权重，搜索中一旦违反立即剪枝，不可被任何罚分"购买通过"；硬冲突不可满足时返回 `INFEASIBLE`。
- **软约束整数罚分**：软约束携带正整数权重与层级，违反时把权重加到对应层级的目标分量上。
- **分层词典序目标**：目标向量按层比较（第 0 层优先级最高），元组比较即词典序——高层的 1 个单位罚分不可能被任意数量的低层收益抵消。
- **负权拒绝**：软权重必须为正整数；负权、零权、非整权在建模时即抛出 `ValueError`/`TypeError`（否则下界推理不成立）。
- **罚分解释与最佳下界**：`explain()` 逐项列出被违反的软约束及其罚分贡献、每层总罚分、以及经认证的词典序下界；搜索完成时下界等于目标值（最优性得证），节点限额截断时给出前沿下界与分量间隙。
- **独立检查器**：`checker` 模块不复用求解器任何状态，直接在原始约束上重新求值：校验赋值完整性、域合法性、硬约束满足、目标值重算一致；对小搜索空间还用 `itertools.product` 暴力枚举独立确认最优性/不可满足性。

## 结构

- `lexopt/model.py` — 变量（有限域）、约束（硬/软）、模型与权重策略校验
- `lexopt/constraints.py` — 线性约束、`all_different` 构造器
- `lexopt/solver.py` — 回溯 + 分支定界（词典序剪枝、前沿下界、可选节点限额）
- `lexopt/checker.py` — 独立检查器（解校验 + 暴力枚举验证）
- `lexopt/explain.py` — 罚分与下界的人类可读解释
- `examples/demo.py` — 会议排程样例
- `tests/test_lexopt.py` — 25 个 unittest 用例

## 运行样例

```
$ python3 examples/demo.py
=== solver output ===
status: OPTIMAL
nodes explored: 36
assignment: m1=0, m2=2, m3=1, m4=3
objective per level (level 0 is highest priority): (0, 1)
level 0: total penalty = 0
  no soft constraint violated at this level
level 1: total penalty = 1
  - m2_slot1: violated, contributes +1
certified lower bound (per level): (0, 1)
optimality proven: search completed, so the lower bound equals the objective and no lexicographically better solution exists

=== independent checker ===
verified: True
  note: optimality confirmed by independent enumeration
direct re-check of assignment against original constraints: True
```

样例中第 0 层的两条关键偏好（`m3_early`、`vip_m4_late`）全部满足，代价是牺牲第 1 层的 `m2_slot1`——词典序下高层收益不可被低层抵消。

## 用法速览

```python
from lexopt import Model, Solver, linear_constraint, explain, verify_result

model = Model()
model.add_variable("x", (0, 1))
model.add_constraint(linear_constraint("cap", {"x": 1}, "<=", 0))          # 硬
model.add_soft("prefer_x1", ("x",), lambda a: a["x"] == 1, weight=3, level=0)  # 软

result = Solver(model).solve()
print(explain(model, result))
assert verify_result(model, result).ok
```

## 测试结果

```
$ python3 -m unittest discover -s tests -v
...
Ran 25 tests in 0.101s

OK
```

覆盖：硬约束强制与不可购买性、负/零/非整权拒绝、词典序不可抵消性、
罚分精确整数性、逐项解释与紧下界、节点限额下界有效性、检查器对硬违反/
假目标值/越域/缺漏变量/伪造更优目标的检出、`all_different` 与线性约束
构造器、会议排程集成场景。
