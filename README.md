# csp_enum — 有限域 CSP 解枚举器（纯标准库，Python 3.11+）

不调用任何 SAT/SMT 库，用显式栈回溯实现解的枚举、投影去重与暂停/恢复。

## 文件

- `csp_enum.py` — 核心库（问题建模、独立检查器、可恢复枚举器）
- `examples.py` — 运行样例：`python3 examples.py`
- `test_csp_enum.py` — 单元测试：`python3 -m unittest -v`

## 特性与对应机制

- **枚举全部或前 N 个解**：`enumerate_all()` / `first(n)` /
  `search(max_solutions=n)`。
- **阻断子句只排除已输出的完整解**：每输出一个投影，就加入一条仅排除
  该投影的阻断子句（`SolutionEnumerator._blocking`），不影响未输出的解。
- **投影变量去重**：`projection=(...)` 指定输出变量；辅助变量不同但投影
  相同只输出一次（投影变量排在分支顺序最前，阻断子句尽早剪枝）。
- **空投影最多一个解**：`projection=()` 时输出至多一个 `{}`，等价于
  可满足性判定。
- **暂停/恢复不重复输出**：搜索使用显式栈（非递归、非生成器），
  `search(budget=k)` 按步数预算暂停，栈保留在对象内，再次调用即续跑；
  `export_state()` / `restore()` 还可把搜索栈序列化为 JSON 后恢复。
- **独立检查器**：`verify_solution(problem, assignment)` 与求解器传播
  逻辑不共享代码，逐条对照原约束与变量域验证；求解器每次声称有解前
  都会调用它，失败则抛 `SolverIntegrityError`。

## 快速上手

```python
from csp_enum import Problem, SolutionEnumerator, constraint

p = Problem()
for v in "xyz":
    p.add_variable(v, (0, 1, 2))
p.add_constraint(constraint(("x", "y", "z"),
                            lambda a: a["x"] + a["y"] + a["z"] == 3))

enum = SolutionEnumerator(p, projection=("x", "z"))
while True:
    res = enum.search(budget=100)   # 预算耗尽即暂停，可再次调用续跑
    print(res.solutions, res.status)
    if res.status == "exhausted":
        break
```

约束助手：`constraint(scope, func)`、`all_different(*vars)`、
`table(scope, allowed_rows)`；也可自定义 `partial_func` 加强剪枝
（只影响效率，不影响正确性）。
