# fdsolver — 有限域整数最小化求解器（纯标准库）

Python 3.11+，仅使用标准库，不调用任何 SAT/SMT 求解器。对有限域整数变量上的
线性约束（`<=` / `>=` / `==`，整数系数）最小化线性整数目标。

## 保证

- **可行上界 / 可靠下界**：分支定界维护已验证的可行解（上界）与松弛下界。
  下界来自“忽略约束、逐变量取目标项最小值”的松弛，配合只删不可行值的
  界限一致性传播，**下界永远不会超过真实最优值**。
- **预算中断不伪称最优**：`max_nodes` / `max_seconds` 预算耗尽时返回
  `Status.FEASIBLE`（有解但最优性未证明）或 `Status.UNKNOWN`
  （未找到解且未证明不可行），并附带当前上界、下界与 gap。
- **无解 vs 未找到解**：搜索空间耗尽仍无可行解才返回
  `Status.INFEASIBLE`（已证明无解）；预算耗尽而无解是 `Status.UNKNOWN`。
- **独立检查器**：`checker.py` 直接对用户给出的原始约束与原始域重新求值；
  求解器在接受/返回任何解之前必须通过它验证（`SolveResult.verified`）。
- **稳定平局选择**：同目标值的可行解按变量声明顺序取字典序最小者
  （DFS 固定变量顺序 + 值升序，叶子按字典序枚举，结果可复现）。

## 用法

```python
from fdsolver import Problem, solve

p = Problem(
    domains={"x": range(6), "y": range(6)},   # 有限整数域（插入顺序即变量序）
    objective={"x": 3, "y": 2},               # 最小化 3x + 2y
    constraints=[({"x": 1, "y": 1}, ">=", 4)],
)
r = solve(p, max_nodes=10_000, max_seconds=5.0)
print(r.status, r.assignment, r.objective_value, r.lower_bound, r.absolute_gap)
```

## 运行

```bash
python3 examples/sample_run.py          # 运行样例
python3 -m unittest discover -s tests   # 运行测试（含暴力枚举交叉验证）
```

## 文件

- `fdsolver.py` — 分支定界求解器（传播、下界、预算、四种状态）
- `checker.py` — 独立解检查器（针对原始约束/原始域）
- `tests/test_fdsolver.py` — unittest：最优性、平局、预算、下界可靠性、
  检查器、与暴力枚举对照（60 个随机实例）
- `examples/sample_run.py` — 三个演示：证明最优 / 预算中断带 gap / 无解与未知
