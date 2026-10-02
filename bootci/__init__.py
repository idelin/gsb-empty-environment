"""bootci：按观测单位重采样的统计量区间估计（仅标准库）。

- 重采样单位显式：单观测 / 成组 / 配对（成对抽）/ 分层 / 移动块；
- 百分位与 BCa 区间分开实现；jackknife 退化或加速度不可估时显式失败；
- 退化分布单独报告（DegenerateInterval）；
- 分位数定义显式（type7 / type1 / 加权 type1）；
- 随机源由调用方注入（seed），按样本 ID 分流，并行顺序不改结果；
- 有限小样本精确枚举用于参考验证；精确结论与经验估计分别标注。
"""
from .core import BCA, EMPIRICAL, EXACT, PERCENTILE, BootstrapReport
from .core import bootstrap, exact_bootstrap
from .errors import (
    AccelerationNotEstimableError,
    BCANotEstimableError,
    BootCIError,
    EnumerationTooLargeError,
    InvalidResampleError,
)
from .intervals import DegenerateInterval, Interval
from .quantile import quantile, weighted_quantile_type1
from .resample import CIRCULAR, TRUNCATED
from .rng import StreamFactory, derive_seed
from .samples import (
    OneSample,
    PairedSample,
    StratifiedSample,
    TimeSeriesSample,
    TwoSample,
)

__all__ = [
    "AccelerationNotEstimableError",
    "BCA",
    "BCANotEstimableError",
    "BootCIError",
    "BootstrapReport",
    "CIRCULAR",
    "DegenerateInterval",
    "EMPIRICAL",
    "EXACT",
    "EnumerationTooLargeError",
    "Interval",
    "InvalidResampleError",
    "OneSample",
    "PERCENTILE",
    "PairedSample",
    "StratifiedSample",
    "StreamFactory",
    "TRUNCATED",
    "TimeSeriesSample",
    "TwoSample",
    "bootstrap",
    "derive_seed",
    "exact_bootstrap",
    "quantile",
    "weighted_quantile_type1",
]

__version__ = "0.1.0"
