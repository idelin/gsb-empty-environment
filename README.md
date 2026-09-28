# 有限域临时假设求解器

这是一个只使用 Python 3.11 标准库的有限域 CSP 示例，不调用 SAT/SMT 求解器或第三方库。

## 功能

- 有限域变量和 Python 谓词约束。
- GAC（广义弧相容）传播和带回溯的枚举搜索。
- 支持多次、嵌套的临时假设，`with` 退出或调用 `cancel_scope()` 即取消。
- 每次域裁剪都记录到当前层级的 trail；取消层级时只恢复该层级造成的裁剪。
- `solve()` 在内部事务中传播和分支，返回解后不会缩小当前作用域的域。
- 内层传播或搜索冲突并退出后，外层作用域仍可继续求解。
- 相同的重复假设不去重，每次都会创建独立作用域层级。
- 找到候选解后，`validate_assignment()` 只依据原始有限域、基础约束和当前活动假设独立复查。

## 运行

```bash
python3.11 example.py
python3.11 -m unittest discover -s tests -v
```

## 关键 API

```python
solver = Solver({"x": {1, 2, 3}, "y": {1, 2, 3}})
solver.add_constraint(["x", "y"], lambda x, y: x != y)

with solver.assume("x", 1):
    result = solver.solve()
```

一个作用域也可以包含多个临时假设：

```python
with solver.assuming((("x", {1, 2}), ("y", {2, 3}))):
    result = solver.solve()
```

## 文件

- `fdcsp/domain.py`：有限域和分层回滚 trail。
- `fdcsp/constraints.py`：谓词约束。
- `fdcsp/assumptions.py`：临时假设。
- `fdcsp/solver.py`：GAC 传播、嵌套作用域和回溯搜索。
- `fdcsp/checker.py`：独立解检查器。
- `tests/test_solver.py`：嵌套、冲突、重复假设、取消和检查器测试。
- `example.py`：可运行演示。
- `TEST_RESULTS.md`：本机 Python 3.11 的实际测试和示例输出。
