# graphcolor

有限颜色图着色求解器（Python 3.11，仅标准库，unittest 测试）。

## 功能

- **有限颜色 + 预着色 + 颜色容量上限**：`Problem(graph, colors, capacities, precoloring)`。
- **三种求解状态严格区分**：`Status.SAT` / `Status.UNSAT`（已证明不可行）/
  `Status.BUDGET_EXHAUSTED`（搜索预算耗尽，不作任何可行性结论）。
- **独立校验器**：`graphcolor.verify.verify(problem, coloring)` 不复用求解器代码，
  返回违规列表（含边 ID、容量、预着色、缺失/未知顶点等）。
- **可重现搜索轨迹**：求解完全确定（顶点按 ID 排序、颜色按给定顺序），
  `trace_digest(trace)` 给出 SHA-256 摘要；事件 `n` 占据左闭右开逻辑区间 `[n, n+1)`。
- **可撤回假设**：`push_assumption(v, c, sign)` / `pop_assumption()`，支持正负字面量，
  冲突假设立即回滚；负假设会把该颜色钉出其对称类。
- **颜色对称类**：容量相等且未被预着色钉住的颜色可互换，按值优先序
  （value precedence）破除对称；搜索仍返回**词典序最小**可行赋色。
- **理由传播**：每次删色都记录理由（边 / 容量 / 预着色 / 决策 / 假设），
  回溯经 trail 恢复域、剩余容量与理由图；`check_invariants()` 可校验一致性。
- **子集最小冲突核**：`minimal_conflict_core(problem)` 在原约束（边、预着色、容量）
  上做删除式极小化；任何一步预算耗尽则不声称最小性。
- **独立枚举对照**：`compare_with_solver(problem)` 用暴力枚举 + 独立校验器核对
  剪枝未删合法解，并比较对称破除开/关时的解类集合（关=全部合法解，开=各类代表元）。
- **数据模型**：顶点、边均为字符串 ID；孤立点与多重边保留；时间区间左闭右开
  （`Interval`，`t in [start, end)`）。

## 运行

```bash
python3.11 demo.py                 # 运行样例
python3.11 -m unittest discover -s tests -v   # 运行测试
```

## 样例输出（实际运行）

```
== 1. Triangle with 2 colors: proven infeasible + minimal core ==
  status: unsat (nodes=2)
  core: ['edge:e-ab', 'edge:e-ac', 'edge:e-bc']  subset-minimal: True
== 2. Triangle with 3 colors: lexicographically smallest coloring ==
  status: sat  coloring: {'a': 'red', 'b': 'green', 'c': 'blue'}
  verifier violations: []
  trace digest: a233b7c3ce00e82d...
    [  0,1  ) decide    a=red
    [  4,5  ) remove    b!=red <- edge(e-ab):a=red
    ...
== 3. Precoloring conflict: the offending edge is located directly ==
  status: unsat  conflict: edge e-ac: a and c both precolored red
== 4. Isolated vertex still bounded by color capacity ==
  3 vertices / capacity 2 -> status: unsat
  core: ['capacity:green', 'capacity:red']  subset-minimal: True
== 5. Budget exhaustion is not infeasibility ==
  status with max_nodes=1: budget_exhausted
  core status: budget_exhausted, minimal claimed: False
== 6. Retractable assumptions ==
  assume a=blue: True
  solve under assumption: sat {'a': 'blue', 'b': 'red', 'c': 'green'}
  after retract:          sat {'a': 'red', 'b': 'green', 'c': 'blue'}
== 7. Symmetry breaking on/off vs brute force (multi-edge + isolated) ==
  legal colorings: 4, symmetry classes: 2
  solver agrees with enumeration: True
```

## 测试结果（实际运行）

```
$ python3.11 -m unittest discover -s tests
Ran 45 tests in 0.003s
OK
```

覆盖：三角形两色不可行 / 三色可行、词典序最小性（对照暴力枚举）、预着色冲突
直接定位边、孤立点受容量约束、预算耗尽与不可行区分、理由记录与回溯恢复、
假设压栈/撤回、轨迹可重现与左闭右开区间、冲突核子集最小性（及预算不足时
不声称最小）、对称破除开/关与枚举解类一致性、多重边与孤立点保留、校验器
各类违规。

## 代码结构

- `graphcolor/model.py` — `Graph` / `Edge` / `Problem` / `Interval` / 对称类定义
- `graphcolor/solver.py` — 回溯搜索、前向检查传播、理由图、trail、假设栈、轨迹
- `graphcolor/verify.py` — 独立校验器
- `graphcolor/enumerate.py` — 暴力枚举、规范形、开/关对称破除对照
- `graphcolor/core.py` — 子集最小冲突核提取
- `demo.py` — 运行样例；`tests/` — unittest 套件
