# bootci：按观测单位重采样的统计量区间估计

Python 3.11，仅标准库，测试用 unittest。

## 设计要点

**重采样单位显式**（`bootci/samples.py`, `bootci/resample.py`）
- `OneSample`：单位为单个观测；
- `TwoSample`：成组（独立）两样本，两组各自组内重采样，组大小固定；
- `PairedSample`：配对样本，单位为“对”，抽下标后成对取出，绝不拆对、绝不逐行独立抽样；
- `StratifiedSample`：分层样本，层内重采样、层大小固定、层间不串扰；
- `TimeSeriesSample`：移动块 bootstrap，单位为长度 `block_length` 的重叠块，
  边界规则显式二选一：`circular`（环形回绕，起点 0..n-1）或
  `truncated`（块完整落在序列内，起点 0..n-b），拼接后截断至原长度 n。

**区间方法分开实现**（`bootci/intervals.py`）
- 百分位法：`[Q(alpha/2), Q(1-alpha/2)]`，无任何修正；
- BCa：偏差修正 z0 + jackknife 加速度 a；z0 无穷、调整分母为零、
  修正分位数越界、jackknife 退化（留一值全同/不足）时显式抛
  `BCANotEstimableError` / `AccelerationNotEstimableError`，绝不返回占位结果。

**分位数定义显式**（`bootci/quantile.py`）
- `type7`：Hyndman & Fan type 7 线性插值（默认，与 numpy/R 一致）；
- `type1`：经验 CDF 逆；精确枚举用加权 type1（整数权重，无抽样误差）。

**退化分布单独报告**
- 重采样统计量为常数（如相同值样本）时不套用区间公式，
  以 `DegenerateInterval(point)` 单独报告，`report.degenerate=True`。

**无效重采样不静默丢弃**
- 默认 `on_invalid="raise"`：首次无效即抛 `InvalidResampleError`；
- `on_invalid="record"`：计数并写入 `report.warnings`，显式声明名义置信度
  变为条件于有效重采样的近似；`n_resamples` 参数非法（非 int / < 2）直接报错。

**随机源注入与分流**（`bootci/rng.py`）
- 调用方必须显式传入 `seed`；按 `sample_id` 经 SHA-256 派生独立子流，
  样本以任意顺序、任意并行度处理结果完全一致；
- 报告记录 `master_seed`、`n_resamples`、`sample_sizes`。

**精确 vs 经验分别标注**
- `bootstrap()` → `nature="empirical"`（含蒙特卡洛误差）；
- `exact_bootstrap()` → `nature="exact"`，有限小样本枚举全部重采样
  （多项式整数权重），`master_seed=None`，超限显式抛
  `EnumerationTooLargeError`；用作经验估计的参考验证。

## 运行

```bash
python3.11 -m unittest discover -s tests -t .   # 54 个测试
python3.11 examples/run_examples.py             # 运行样例（输出见 examples/output.txt）
```

## 最小示例

```python
from bootci import OneSample, PairedSample, bootstrap, exact_bootstrap

def mean(v):
    return sum(v) / len(v)

# 经验 bootstrap：百分位 + BCa，seed 由调用方注入
report = bootstrap(OneSample([2.1, 3.5, 2.8, 4.2, 3.0], sample_id="s1"),
                   mean, n_resamples=5000, seed=42,
                   methods=("percentile", "bca"))
print(report.intervals["percentile"])   # Interval(low=..., high=..., ...)

# 配对样本：成对抽取
report = bootstrap(PairedSample(before, after), lambda x, y: mean(y) - mean(x),
                   n_resamples=5000, seed=42)

# 小样本精确枚举：结论为精确（exact）
report = exact_bootstrap(OneSample([1.0, 2.0, 3.0]), mean, alpha=0.05)
assert report.nature == "exact"
```
