# 可合并的定宽直方图（纯标准库，Python 3.11+）

边界由调用方固定的定宽直方图，支持精确同边界合并、跨边界并集与同样本调和。
分位数只返回**值区间 + 误差说明**，不伪称精确值；不假定桶内均匀分布，
不使用按比例重分桶估计充当精确贡献。仅依赖标准库，测试使用 unittest。

## 文件

- `histogram.py` — `FixedHistogram`：定宽直方图与分位数区间。
- `constraints.py` — 跨边界操作：`union()`（不同样本并集）与
  `reconcile()`（同一样本调和），基于非负整数计数约束。
- `table.py` — 带列类型的批次输入（`Batch`/`Column`/`ColumnType`）。
- `run_example.py` — 运行样例。
- `test_histogram.py` — unittest 测试套件（29 个用例）。

## 核心约定

- 桶为左闭右开 `[b_i, b_{i+1})`；**落在桶右端的值进入下一桶**；
  小于最小边界进下溢桶，大于等于最大边界进上溢桶。
- `None`（null）与 `NaN` 分别单独计数，且不计入分位数总体。
- 计数为 Python `int`，秩 `r = max(1, ceil(q*n))` 用 `Fraction` 精确计算；
  float 类型的 q 按十进制字面量解释（`0.1` → `1/10`）。
- `merge()` 仅接受相同边界：`merge(h1, h2) == 直接处理合并样本`；
  边界不同直接拒绝（`BoundaryMismatch`）。

## 跨边界合并的两种语义

- **`union(h1, h2, ...)`**：合并**不同样本**（如不同分片）用不同边界统计的
  直方图。每个源桶落入细化原子的拆分无法精确得知，用非负整数变量表达
  未知分布；分位数返回所有相容拆分共同覆盖的可靠界限 `[lo, hi)`，
  并给出达到两个端点原子的具体相容分布作为见证
  （`low_witness`/`high_witness`，可 `expand_witness()` 展开为样本）。
- **`reconcile(h1, h2, ...)`**：调和**同一样本**被不同边界统计的多个直方图。
  总数/null/NaN 不一致，或约束系统不存在非负整数解时，
  抛出 `IncompatibleHistograms`（统计不相容必须拒绝）。

界限求解使用精确的整数可行性求解器（DFS + 区间约束传播），
测试中用**完全独立的暴力枚举**验证界限与枚举结果一致。

## 用法

```python
from histogram import FixedHistogram
from constraints import union, reconcile
from table import Batch, Column, ColumnType, histogram_from_batch

h = FixedHistogram.from_values([1, 5, 10, 15], [0, 10, 20])
qi = h.quantile(0.5)          # QuantileInterval: [0, 10)，含误差说明

u = union([FixedHistogram([0, 10, 20], counts=[6, 4]),
           FixedHistogram([0, 5, 15, 20], counts=[2, 6, 2])])
qb = u.quantile(0.5)          # QuantileBound: [5, 15)，含端点见证
sample = u.expand_witness(qb.low_witness, "left")   # 达到左端点的样本

batch = Batch([Column("v", ColumnType.INT)], [(1,), (None,), (2,)])
h2 = histogram_from_batch(batch, "v", [0, 10])
```

## 运行样例

`python3 run_example.py` 输出（节选）：

```
2. 同边界合并：merge(h1,h2) == 直接处理合并样本
与直接处理一致: True

3. 分位数：值区间 + 误差说明（不伪称精确值）
median: QuantileInterval(q=1/2, rank=3/5, [5.0, 10.0), region=bucket[1])

4. 不同边界不可直接 merge
拒绝: 边界不同的直方图不可直接合并；跨边界请使用 ...

5. 不同样本、不同边界的并集（union）
median 界限: QuantileBound(q=1/2, rank=10/20, [5, 15), atoms=2..3)
低端点见证样本: [0, 0, ..., 15, 15] -> 第 10 个值 = 5
高端点见证样本: [4, 4, ..., 19, 19] -> 第 10 个值 = 14

6. 同一样本、不同边界的调和（reconcile）
median 界限: QuantileBound(q=1/2, rank=5/10, [3, 5), atoms=2..2)
拒绝: 统计不相容：不存在满足全部桶约束的非负整数分布
```

## 测试结果

`python3 -m unittest test_histogram`：

```
Ran 29 tests in ~3s
OK
```

覆盖：右端进下一桶、下溢/上溢、null 与 NaN 区分、非法边界/值拒绝、
合并等于直接处理合并样本（随机化性质测试）、不同边界拒绝直接合并、
分位数区间包含真实值（随机化）、整数精确性（2**70 级）、
批次类型校验、调和不相容拒绝、并集/调和界限与独立暴力枚举一致、
见证样本达到端点、不把按比例重分桶估计当成精确贡献。
