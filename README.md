# 稀疏三角求解器（依赖层级调度）

Python 3.11 标准库实现（不使用 NumPy/SciPy）的稀疏三角矩阵求解，面向
`L x = b`、`U x = b` 以及它们的转置 `L^T x = b` / `U^T x = b`。
计算顺序由**数值依赖**决定，而不是坐标的存储顺序；同一层的节点互不依赖，
构成可并行节点集合。

## 满足的约束

- 支持下三角 / 上三角、单位对角、转置求解。
- 尺寸 `n`、索引基数 `index_base`（0 或 1）显式输入。
- 重复坐标在校验前**先合并（值相加）**，内部不存在重复模式。
- 非单位对角矩阵必须每个对角位置都有条目，否则抛
  `MissingPivotError`（属于 `StructureError`）。
- 单位对角矩阵禁止显式对角条目（隐式取 1）。
- 越界坐标、越出所声明三角区的条目一律抛 `StructureError`，
  结构非三角时**绝不静默丢弃**越界元素。
- 数值上零（或小于 `pivot_tol`）的主元抛 `ZeroPivotError`，
  与“缺主元”分开；求解前统一预检全部主元，结果只写临时缓冲，
  数值故障时**不会发布半个解**（输入 `b` 也保持不变）。
- 符号分析按依赖图做 Kahn 最长路径分层：节点
  `level(i) = 1 + max(level(依赖))`，无依赖为第 0 层。
- 多个右端共享一份分析结构（`solve_multi`），转置视图单独缓存。
- 仅改数值（`set_value`）复用分析；增删结构（`add_entry` /
  `remove_entry`）改变结构令牌，旧的（含转置）分析局部失效并逐出，
  只重新分析新结构。`SolveStats` 记录分析复用率等指标。
- 下三角的转置按上三角处理（反之亦然），通过视图实现，不复制存储。
- 小规模用自写稠密前代/回代（`trisolve/dense.py`）交叉核验。

## 目录

```
trisolve/
  errors.py    # TriSolverError / StructureError / MissingPivotError /
               # ZeroPivotError / DimensionError
  matrix.py    # TriangularMatrix：合并坐标、三角形与主元校验、结构令牌
  analysis.py  # 依赖图 + 层级调度 + 每节点依赖证据
  solver.py    # 多右端编译计划、主元预检、安全提交、复用统计
  dense.py     # 自写稠密三角求解 / 矩阵向量乘 / 残差（仅参考与测试）
tests/
  test_trisolve.py   # unittest，27 个用例
demo.py             # 运行样例
```

## 使用

```python
from trisolve import TriangularMatrix, TriangularSolver, LOWER

entries = [(0, 0, 2.0),
           (1, 0, 1.0), (1, 1, 4.0),
           (2, 1, 2.0), (2, 2, -1.0),
           (3, 0, -1.0), (3, 2, 3.0), (3, 3, 5.0)]
m = TriangularMatrix(
    n=4,
    rows=[e[0] for e in entries],
    cols=[e[1] for e in entries],
    values=[e[2] for e in entries],
    uplo=LOWER,            # 或 UPPER
    unit_diagonal=False,   # True 时禁止显式对角
    index_base=0,          # 可设 1
)
solver = TriangularSolver(m)

x  = solver.solve(b)                    # L x = b
xt = solver.solve(b, transpose=True)    # L^T x = b（按上三角视图）
X  = solver.solve_multi([b1, b2, b3])   # 多右端共享分析

sched = solver.analyze()
print(sched.levels)                     # 每层可并行节点
print(sched.dependency_evidence(3))     # 节点 3 依赖谁、系数在哪
```

层级与证据示例（4x4 手建矩阵）：

```
level 0: [0]
level 1: [1]   1 依赖 x_0*A(1,0)
level 2: [2]   2 依赖 x_1*A(2,1)
level 3: [3]   3 依赖 x_0*A(3,0), x_2*A(3,2)
```

无依赖关系的多个节点会落入同一层，例如节点 1、2 都只依赖 0 时
层级为 `[[0], [1, 2]]`，同层即可并行。

## 运行

```bash
/opt/homebrew/bin/python3.11 -m unittest discover -s tests -v
/opt/homebrew/bin/python3.11 demo.py
```

## 测试结果

`python3.11 -m unittest discover -s tests -v`：

```
Ran 27 tests in 0.009s

OK
```

覆盖点：重复坐标合并、0/1 基索引、越界/越三角拒绝、缺主元与零主元
分类、近零容差、零主元不发布半个解、全部零主元预检、下/上三角与转置
数值正确性、单位对角、多右端共享一次分析、层级与依赖证据、乱序存储不
影响调度、改值复用分析、改结构局部失效、删边后重分层，以及
`n ∈ {1,2,5,8,12}` × `L/U` × `单位/非单位` × `转置/不转置` 的随机矩阵
对稠密参考交叉核验（存储顺序随机打乱）。

## 设计说明

- **依赖方向（四种视图统一）**：非转置时存储条目 `(r,c)` 的系数位于
  节点 `r` 的方程行，节点 `r` 依赖 `c`；转置时有效系统读取转置坐标
  `(c,r)`，节点 `c` 依赖 `r`。系数始终从**存储矩阵**坐标取值，
  因此转置只是视图。
- **为何顺序不能只按存储**：COO 坐标可任意顺序提供（演示与随机测试
  均打乱），只有依赖图分层才能同时给出正确顺序与并行划分。
- **复用率**：统计定义为
  `analyses_reused / (analyses_performed + analyses_reused)`，
  结构令牌只包含维度、三角类型、单位对角标志与排序后的非对角坐标，
  不含数值，因此纯数值更新天然命中缓存。
- **并行**：层内节点在数据上独立，`LevelSchedule.parallel_nodes()`
  即并行划分；本实现顺序执行层内循环，因为 CPython 标准库的线程不提供
  真正的数值并行，接入进程/线程池时只需按层派发同层节点并在层间同步。
