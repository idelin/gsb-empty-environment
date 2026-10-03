# tinyreduce

纯 Python 3.11 标准库实现的多轴归约（sum / mean / min / max）与梯度。
不依赖任何自动微分或张量库；自带独立朴素参考实现与小域枚举核验。

## 特性

- **多轴归约**：`axes=None` 归约全部轴；负轴规范化为 `axis + ndim`；
  规范化后的重复轴直接拒绝（`ValueError`）；`keepdims` 保留尺寸 1。
- **空归约语义**（明确且唯一）：
  | 归约集合为空（空维度或全 mask） | 结果 |
  |---|---|
  | `sum` | `0.0`（加法单位元） |
  | `mean` | `UndefinedReductionError`（空集均值不可定义） |
  | `min` / `max` | `UndefinedReductionError` |
- **掩码**：`mask` 与底层缓冲区对齐，被屏蔽元素视为不存在。
- **非连续视图**：`transpose` / `slice_axis` 产生共享缓冲区的 strided 视图，
  归约与梯度直接在视图上定义。
- **分块树式执行**：每组元素按 `chunk_size` 分块折叠，再对块部分和做二叉树
  合并。所有合并算子满足结合律，因此**任意 chunk_size 下结果与梯度逐位一致**；
  峰值工作区（存活部分和个数）由 `WorkspaceTracker` 记录。
- **极值并列梯度策略跨块一致**：梯度在归约完成后对**全局并列集**分配——
  `uniform`（默认，并列者均分）或 `first`（行主序最小逻辑下标独占），
  与分块边界无关。
- **不可微点标记（面向高阶微分）**：min/max 出现并列时只存在次梯度，
  结果上标记 `nondiff_outputs` / `nondiff_inputs`；
  `backward(..., on_nondiff="raise")` 抛出 `NonDifferentiableError`，
  **绝不悄悄用首元素解决所有情形**。
- **别名梯度累加**：`GradStore` 以底层缓冲区身份为键，共享缓冲区的
  重叠视图梯度累加到同一缓冲。
- **轴重排不变性**：`(0, 2)`、`(2, 0)`、`(-1, 0)` 等写法产生相同的
  数学结果与梯度形状。
- NaN 传播到归约值；NaN 元素彼此视为并列。`inf` 参与比较与求和遵循
  IEEE 浮点语义（如 `inf + (-inf) = nan`，`max{inf, inf}` 为并列）。

## 布局

```
tinyreduce/
  tensor.py    # Tensor：扁平缓冲 + shape + strides + mask；视图共享缓冲
  reduce.py    # 归约引擎：分块树式执行、梯度、工作区统计、不可微标记
  verify.py    # 独立朴素参考实现 + 小域枚举核验 + 有限差分
tests/test_reduce.py
examples/demo.py
```

## 用法

```python
from tinyreduce import Tensor, reduce, backward, GradStore

t = Tensor.from_nested([[1.0, 2.0, 3.0], [4.0, 5.0, 6.0]])
r = reduce(t, "mean", axes=-1, keepdims=True)   # shape (2, 1)
g = backward(t, r, upstream=1.0)                # 形状与 t 相同

# 并列：标记 + 显式策略
t2 = Tensor.from_nested([2.0, 1.0, 1.0, 3.0])
r2 = reduce(t2, "min")
r2.nondiff_inputs                              # {1, 2}，不可微点
backward(t2, r2, 1.0)                          # uniform: [0, .5, .5, 0]
backward(t2, r2, 1.0, tie_policy="first")      # first:   [0, 1, 0, 0]
backward(t2, r2, 1.0, on_nondiff="raise")      # 严格模式：抛错而非悄悄解析

# 分块与工作区
r3 = reduce(t2, "min", chunk_size=2)
r3.workspace_peak                              # 峰值存活部分和个数
```

## 独立数值验证

`tinyreduce/verify.py` 中的朴素参考实现（无分块、无树）与引擎对照：

- **小域枚举**：形状 `(), (1,), (3,), (2,2), (0,), (2,0), (0,2)` 全枚举
  （值域含 `-inf, 有限值, +inf`，掩码全子集枚举），形状 `(2,3), (2,2,2)`
  确定性采样；覆盖空维、全 mask、正负无穷、并列、两种并列策略、
  轴排列、极端 chunk size。
- **别名枚举**：重叠视图 `[0:3]` 与 `[2:5]` 的梯度累加对照。
- **有限差分**：中心差分 vs 解析梯度。

实测（Python 3.11.15）：

```
enumeration : 2467 configs, 110776 reductions, 191696 grad checks,
              45436 nondiff checks, failures=0, peak workspace=8
alias enum  : 128 configs, failures=0
finite diff : {'sum': '3.04e-10', 'mean': '6.00e-11',
               'min': '8.23e-11', 'max': '1.40e-10'}
```

## 运行

```bash
python3.11 examples/demo.py          # 运行样例
python3.11 -m unittest discover -s tests -v   # 测试
```

测试结果（28 项）：

```
test_permutation_invariance ... ok
test_overlapping_views_accumulate ... ok
test_same_tensor_reduced_twice_accumulates ... ok
test_engine_matches_naive_reference ... ok      # 枚举核验
test_alias_gradient_accumulation ... ok         # 别名枚举
test_analytic_grad_matches_finite_difference ... ok
... (共 28 项)
----------------------------------------------------------------------
Ran 28 tests in ~3.4s

OK
```
