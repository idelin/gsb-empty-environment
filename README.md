# 事务化稀疏切片赋值（Python 3.11 标准库实现）

一个零第三方依赖（不使用 NumPy/SciPy）的小型 CSR 风格稀疏矩阵，提供
**事务化切片赋值**、**历史只读快照视图**，并用一个**稠密快照参考模型**对随机
重叠赋值做差分核验。

## 设计要点

- **规范存储（canonical form）**：每行是一个不可变 `RowBlock`，列索引严格
  递增、无重复；显式零（赋值 `0`）作为条目保留，不被裁剪，可用
  `canonical_check()` 自检。尺寸与索引均显式输入。
- **非连续行列索引切片**：选择子支持 `int`、`slice`（含负步长、负索引、边界
  裁剪）、任意整数序列（list/tuple/range，允许重复与乱序）。
- **索引配对语义**：当行、列都是等长整数序列时按位置配对（NumPy 高级索引
  语义）；其余组合构成笛卡尔网格。
- **重复目标策略**：同一目标单元格被多次命中时按求值顺序**后写胜出**，
  统计在 `AssignmentStats.repeated_targets`。
- **广播**：右侧支持标量、嵌套行优先列表（NumPy 式左对齐广播）、稀疏矩阵或
  区域选择；形状不兼容/列表错位在写入前抛出 `ShapeMismatchError`。
- **撤回**：`WITHDRAW` 删除结构条目（区别于写入显式零 `0`）。
- **事务化赋值（失败不留半更新）**：流程为
  校验选择子/RHS → 预赋值快照读取 → 差异合并、构造新行块 → 配额检查 →
  单点提交。任何异常发生在任何行块被替换之前。
- **原地重叠赋值**：右侧与左侧共享底层存储时（含矩阵自身），RHS 在提交前
  被急切物化为不可变 `Selection`，读到的是**赋值前快照**。
- **历史只读视图**：`snapshot()` 返回 `SnapshotView`，共享不可变行块，提交
  后旧视图保持旧值；写操作抛 `ReadOnlyError`。
- **内存额度**：`MemoryQuota(limit)` 以“将被复制的不同行块数”计量；额度不足
  在任何写入前抛 `QuotaExceededError`，不产生部分写入；
  `row_blocks_copied` 统计**实际**复制的行块（无变化的行不复制）。
- **稠密快照对照模型**：`DenseReference` 用稠密存储 + `ABSENT` 结构标记，
  复用同一套赋值引擎。差分测试对两个模型同步施加 500 轮随机重叠赋值，
  逐格比较数值、结构存在性和统计量。

## 文件结构

```
sparse_assign/
  core.py     # 选择子规范化、广播、合并、事务引擎与公共类型
  matrix.py   # SparseMatrix / SnapshotView / RowBlock
  dense.py    # DenseReference 稠密快照参考模型
  __init__.py
tests/
  test_sparse_assign.py  # 29 个语义/原子性/边界单元测试
  test_differential.py   # 4 个差分测试（500 轮随机重叠 + 配额/历史/显式零）
demo.py                  # 可直接运行的演示脚本
```

## 运行

```bash
python3.11 -m unittest discover -s tests -v
python3.11 demo.py
```

## 运行样例

`python3.11 demo.py` 的输出：

```
== 1. non-contiguous fancy selectors; repeated targets, last wins ==
   paired fancy -> ((1, 0, 7, 0), (0, 0, 7, 9)) stats: AssignmentStats(cells_written=2, cells_withdrawn=0, cells_unchanged=0, repeated_targets=0, row_blocks_copied=2, explicit_zeros_written=0)
   repeated last-wins -> ((20, 30, 7, 0), (0, 0, 7, 9)) repeated: 1

== 2. overlapping RHS shares storage: pre-assignment snapshot ==
-- right shift reads the pre-state
    (1, 1, 2, 3, 4)

== 3. broadcast + explicit zero vs. withdraw ==
   stored entries: 8 (9 grid cells, 1 withdrawn)
-- broadcast rows
    (0, 1, 1)
    (2, 0, 2)
    (3, 3, 3)

== 4. memory quota refuses before any partial write ==
   rejected: row block budget 2 exceeded (need 4)
   untouched: True | quota.used: 0
   accepted, row blocks actually copied: 2 | quota.used: 2

== 5. out-of-range rejected before mutation ==
   rejected: row index 4 out of range for size 4

== 6. history snapshot stays read-only and immutable ==
   current: ((9, 9), (9, 9))
   history: ((1, 0), (0, 0)) version: 0
   history rejects writes: snapshot views are read-only

== 7. dense snapshot reference agrees on a random overlap ==
   sparse == dense: True
-- final state
    (0, 0, 1, 0)
    (1, 0, 1, 0)
    (1, 0, 0, 0)
    (0, 1, 0, 0)
```

`python3.11 -m unittest discover -s tests` 的结果：

```
Ran 33 tests in 0.093s

OK
```

## 快速上手

```python
from sparse_assign import SparseMatrix, MemoryQuota, WITHDRAW

m = SparseMatrix(3, 3, [(0, 0, 1), (2, 2, 0)])   # 显式零被保留
m[[0, 2], [1, 1]] = 7            # 等长 fancy 序列：配对赋值 (0,1)/(2,1)
m[0:3:2, 0:2] = [[8], [9]]       # 切片笛卡尔网格 + 行广播
m[1, 1] = WITHDRAW               # 结构删除；而 m[1, 1] = 0 是显式零
view = m.snapshot()              # 历史只读视图，后续提交不影响它
stats = m.assign([0, 1], 0, 5, quota=MemoryQuota(2))
stats.row_blocks_copied          # 实际复制的行块数
```
