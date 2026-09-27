# 等价类敏感值分布监控（合成数据）

比较每个等价类的敏感值经验分布与总体分布，支持离散总变差（TV）、
有序一维输运距离（W1）与一般成本矩阵输运；数据撤回时增量维护两侧
分布、距离与阈值证据。Python 3.11，仅标准库，测试用 unittest。

> 仅处理合成数据。本工具输出的是分布距离与阈值证据等统计量，
> 不构成任何形式化的隐私保证或隐私证明。

## 定义

设 p 为等价类经验分布、q 为总体经验分布（均为 `Fraction` 精确有理数）：

- **总变差** `TV(p,q) = 1/2 · Σ_v |p(v) − q(v)|`，逐桶贡献为 `|p(v)−q(v)|/2`。
- **有序一维输运** `W1(p,q) = Σ_i |CDF_p(x_i) − CDF_q(x_i)| · (x_{i+1} − x_i)`，
  成本 `c(x,y)=|x−y|`；双指针贪心给出最优输运流，逐区间贡献为 `|累计差|·区间长`。
- **一般成本矩阵**：供给/需求缩放到共同总量 `|P|·|Q|` 的整数后求**真正的
  最小费用流**（`mincostflow.py`，SSP 连续最短路增广），成本与流量除以缩放
  因子还原为有理数。一般成本矩阵不满足一维 Monge 结构，**不能**沿用
  一维累计公式；测试中用穷举所有整数输运方案对小实例逐一核对。

## 参数

`DistributionMonitor(metric, threshold, ordered_values, cost_matrix, cost_fn)`

- `metric`：`"tv"` | `"ordered"` | `"general"`。
- `threshold`：阈值（转 `Fraction`），判定 `distance ≤ threshold`，余量
  `margin = threshold − distance`；距离恰等于阈值视为达标。
- `ordered_values`：有序模式的取值全序（缺省为并支撑集排序）。
- `cost_matrix` / `cost_fn`：一般模式的成本 `(a,b) -> 数值`。

## 增量语义

- `add(class_id, value)` / `remove(class_id, value)` 同步更新等价类与总体
  直方图，并使 `population_version + 1`；撤回计数不足会报错，不产生负计数。
- 每次评估记录所基于的总体版本；**总体基线版本一旦变化，所有已评估类
  全部过期**（`stale_classes()`），需 `evaluate_all()` 重新评估。
- 空等价类（全部撤回）不计算距离：`distance=None`，绝不除零。
- 完全相同分布的距离恒为 0（TV 与 W1 均如此）。

## 输出

每次评估（`Evaluation`）包含：类/总体样本基数、精确距离、逐桶（或逐区间）
贡献、最优输运流（输运模式）、阈值判定与余量。`verify_transport_flow`
作为成本验证器：检查流量非负、行和=类分布、列和=总体分布，并在给定
期望成本时判定最优性。

## 运行

```sh
python3.11 run_sample.py        # 合成数据演示
python3.11 -m unittest discover # 28 个测试
```

## 文件

- `histogram.py`：计数直方图，概率为精确 `Fraction`。
- `distances.py`：TV、有序 W1、一般输运、逐桶/逐区间贡献、成本验证器。
- `mincostflow.py`：一般运输问题的最小费用流。
- `monitor.py`：增量监控、版本管理、阈值证据。
- `run_sample.py`：合成数据运行样例。
- `tests/`：unittest，含最小费用流对小实例穷举方案的核对。
