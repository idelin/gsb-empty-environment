# subsample_amp：Poisson 子采样放大公式的稳定数值计算

按给定公式 **ε′ = log(1 + q·(exp(ε) − 1))** 计算参数，提供：

- 数值稳定的 float 求值（**不**出现"直接 exp 溢出再截断"）；
- 参数表达式 DAG + 带**向外舍入**（directed rounding）的区间评估；
- 对查询目标预算 ε_target **反求最大 q**，给出包围区间与残差证据；
- 独立高精度级数参考，对边界与主路径交叉验证；
- 严格的适用条件校验：输入不满足抽样模型时**拒绝**套公式。

仅使用 Python 3.11 标准库（`math` / `decimal` / `dataclasses` / `enum` / `unittest`）。

## 定义与适用假设

实现的唯一恒等式是 Poisson 子采样放大界：

```
ε′ = log(1 + q·(exp(ε) − 1))
```

仅在以下**全部**条件成立时适用：

1. 基础机制为**纯 ε-DP**（δ = 0），ε ≥ 0；
2. 子采样为 **Poisson 采样**：每条记录以概率 q 独立保留，0 ≤ q ≤ 1；
3. 邻接关系为 add/remove。

不满足时（如无放回均匀采样、δ > 0 的 (ε,δ)-DP、q∉[0,1]、ε<0、非有限输入），
`validate_subsample_applicability` 抛出 `ApplicabilityError`，组合（`compose_rounds`）
同样拒绝构造。多轮组合采用基本组合（basic composition）：总预算为各轮 ε′ 之和。

**边界精确**：q = 0 → 0；ε = 0 → 0；q = 1 → ε（均精确返回）。

## 数值方法

- 稳定形式：ε ≤ 700 用 `log1p(q·expm1(ε))`；ε > 700 用等价形式
  `ε + log(q + (1−q)·e^{−ε})`——不计算 `exp(ε)`，`e^{−ε}` 下溢到 0 也安全，
  任意大的 ε 都不会溢出（ε = 1e300 亦可）。
- 区间评估：`decimal.Decimal` + 专用 `ROUND_FLOOR` / `ROUND_CEILING` 上下文（60 位），
  每个初等运算向外舍入；超越函数额外外扩 1 ulp；转 float 时用 `nextafter` 外扩，
  保证区间严格包含精确数学值。
- 反解：q* = expm1(ε_target)/expm1(ε) 在对数域计算
  （`log(expm1(x)) = x + log(1 − e^{−x})`），大 ε 不溢出；
  输出 Decimal 有向舍入包围区间、float 包围区间、以及两端点残差证据。
- 独立参考：`reference.py` 用泰勒/atanh 级数自行实现 exp/ln（不调用
  `Decimal.exp/ln`），与主路径无公共代码，用于交叉验证。

## 免责声明（务必阅读）

- 本包只处理**合成参数**，不接触真实数据。
- 数值计算的准确性**不等于**隐私证明；统计实验不构成形式化隐私证明。

## 目录结构

```
subsample_amp/
  validation.py   # 适用条件校验（不满足即拒绝）
  core.py         # 稳定 float 求值 + log_expm1
  intervals.py    # 向外舍入的 Decimal 区间算术
  dag.py          # 参数表达式 DAG 与多轮组合
  invert.py       # 反求最大 q（包围区间 + 残差证据）
  reference.py    # 独立高精度级数参考
tests/            # unittest 测试
examples/run_examples.py
```

## 用法

```python
from subsample_amp import (
    subsampled_epsilon, SubsampleAmp, Param, compose_rounds, max_q_for_budget,
)

subsampled_epsilon(1.0, 0.01)                 # 0.017036863236176553
subsampled_epsilon(1000.0, 0.01)              # 995.3948298140119（不溢出）
subsampled_epsilon(3.7, 0.0)                  # 0.0（精确边界）
subsampled_epsilon(3.7, 1.0)                  # 3.7（精确边界）

node = SubsampleAmp(Param("eps"), Param("q"))
iv = node.evaluate({"eps": 1.0, "q": 0.01})   # 严格包围精确值的 Decimal 区间
iv.to_float()                                 # 外扩 1 ulp 的 float 包围对

total = compose_rounds([(0.5, 0.1)] * 10).evaluate()   # 多轮基本组合

res = max_q_for_budget(eps=1.0, target=0.3)   # 反求最大 q
res.q_lo, res.q_hi                            # float 包围区间
res.q_interval                                # Decimal 有向舍入包围区间
res.bracket_verified                          # 区间证据是否自洽
print(res.evidence())                         # 残差证据
```

## 运行样例

`python3 examples/run_examples.py` 输出（节选）：

```
1. Stable evaluation of eps' = log(1 + q*(exp(eps) - 1))
  eps=1.0      q=0.0      -> 0.0                      # q = 0 boundary (exact 0)
  eps=1.0      q=1.0      -> 1.0                      # q = 1 boundary (exact eps)
  eps=1.0      q=0.01     -> 0.017036863236176553     # moderate
  eps=2.0      q=1e-09    -> 6.3890560785206315e-09   # tiny q (first-order regime)
  eps=1000.0   q=0.01     -> 995.3948298140119        # eps beyond float exp range
  eps=1000.0   q=1e-300   -> 309.2244721017863        # huge eps, tiny q
  eps=1e+300   q=0.25     -> 1e+300                   # astronomical eps, no overflow

2. Expression DAG with outward-rounded interval evaluation
  eps=1000.0, q=0.01
    decimal interval : [995.394829814011908652780698802352956501073772055881304445371,
                        995.394829814011908652780698802352956501073772055881304445372]
    float hull       : [995.3948298140118, 995.394829814012]
    series reference : ...444537112451098058640857204 (inside interval: True)

4. Inversion: largest q for a target budget, with evidence
  --- eps=1.0, target=0.3
      q* in [0.203609676702311628610121313305339336230110001277107206990158,
             0.203609676702311628610121313305339336230110001277107206990170]
      float bracket: [0.2036096767023116, 0.20360967670231164]
      amp(q_lo) - target = -5.551115123125783e-17 (must be <= 0)
      amp(q_hi) - target = 5.551115123125783e-17 (must be >= 0)
      interval amp([q_lo, q_hi]) contains target: True
      bracket verified: True
  --- eps=1000.0, target=5.0
      q* in [7.48263136562597403321400867092742869714639822959725480839834E-433, ...]
      note: q* is below the smallest float subnormal; the float bracket
            degenerates to [0, 5e-324] -- use the Decimal interval instead

5. Applicability enforcement (formula rejected outside its model)
  rejected q = 1.5: q must be in [0, 1], got 1.5
  rejected eps = -0.1: eps must be >= 0, got -0.1
  rejected model = 'uniform_without_replacement': ... only applies to Poisson subsampling
  rejected base_delta = 1e-5: the formula requires a pure eps-DP base mechanism ...
  rejected compose with invalid round: q must be in [0, 1], got 2.0
```

## 测试结果

`python3 -m unittest discover -s tests`：

```
Ran 54 tests in 0.099s
OK
```

覆盖：精确边界（q=0 / q=1 / ε=0）、极小 q 一阶 regime、大 ε（700/1000/1e300）
不溢出、多轮组合、区间向外舍入的包含性、反解包围区间与残差证据、
级数参考交叉验证、以及全部非法输入拒绝路径。

## 已知限制

- 反解的 Decimal 区间证据在 q* 小于最小可表示 Decimal（约 1e-1000000058，
  对应 ε 约 1e300 量级）时退化为 [0, Etiny]；float 包围区间在 q* 小于最小
  次正规数时退化为 [0, 5e-324]，此时以 Decimal 区间为准（结果中附 note）。
- 区间路径中 `exp(x)` 要求 x ≥ −1e9 量级（Decimal 指数范围），覆盖全部
  现实的 DP 参数。
