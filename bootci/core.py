"""主入口：经验 bootstrap 与精确枚举，元数据（seed、样本量、性质）完整记录。"""
from __future__ import annotations

import math
from dataclasses import dataclass, field

from . import exact as _exact
from .errors import BCANotEstimableError, InvalidResampleError
from .intervals import BCA, PERCENTILE, DegenerateInterval, Interval
from .intervals import bca_interval, percentile_interval
from .jackknife import jackknife
from .resample import (
    CIRCULAR,
    resample_mbb,
    resample_one,
    resample_paired,
    resample_stratified,
    resample_two,
)
from .rng import StreamFactory
from .samples import OneSample, PairedSample, StratifiedSample, TimeSeriesSample, TwoSample

EMPIRICAL = "empirical"
EXACT = "exact"

_INVALID = object()


@dataclass(frozen=True)
class BootstrapReport:
    """一次区间估计的完整记录。

    nature: "empirical"（经验估计，含蒙特卡洛误差）或 "exact"（精确枚举）。
    master_seed: 经验 bootstrap 记录调用方注入的种子；精确枚举无随机性，为 None。
    n_resamples: 请求的重采样次数；精确枚举为 None。
    invalid_resamples: 无效重采样次数（on_invalid="record" 时可能 > 0）。
    """

    sample_id: str
    statistic_name: str
    estimate: float
    intervals: dict          # method -> Interval | DegenerateInterval
    degenerate: bool
    nature: str
    n_resamples: int | None
    invalid_resamples: int
    master_seed: int | None
    sample_sizes: dict
    alpha: float
    quantile_method: str
    support_size: int | None = None
    warnings: tuple = ()


def _observed_args(sample):
    if isinstance(sample, OneSample):
        return (sample.values,)
    if isinstance(sample, (TwoSample, PairedSample)):
        return (sample.x, sample.y)
    if isinstance(sample, StratifiedSample):
        return (sample.strata,)
    if isinstance(sample, TimeSeriesSample):
        return (sample.values,)
    raise TypeError(f"不支持的样本类型: {type(sample).__name__}")


def _resample_args(sample, rng, block_length, boundary):
    if isinstance(sample, OneSample):
        return (resample_one(sample, rng),)
    if isinstance(sample, TwoSample):
        return resample_two(sample, rng)
    if isinstance(sample, PairedSample):
        return resample_paired(sample, rng)
    if isinstance(sample, StratifiedSample):
        return (resample_stratified(sample, rng),)
    if isinstance(sample, TimeSeriesSample):
        return (resample_mbb(sample, block_length, rng, boundary),)
    raise TypeError(f"不支持的样本类型: {type(sample).__name__}")


def _is_valid_value(value):
    return (
        isinstance(value, (int, float))
        and not isinstance(value, bool)
        and math.isfinite(value)
    )


def _try_eval(statistic, args):
    try:
        value = statistic(*args)
    except Exception:
        return _INVALID
    return value if _is_valid_value(value) else _INVALID


def _eval_observed(statistic, args):
    try:
        value = statistic(*args)
    except Exception as exc:
        raise InvalidResampleError(f"观测统计量计算失败: {exc!r}") from exc
    if not _is_valid_value(value):
        raise InvalidResampleError(f"观测统计量无效: {value!r}")
    return float(value)


def bootstrap(sample, statistic, *, n_resamples=2000, alpha=0.05, seed,
              methods=(PERCENTILE, BCA), quantile_method="type7",
              on_invalid="raise", statistic_name=None,
              block_length=None, boundary=CIRCULAR):
    """经验 bootstrap。随机源由调用方通过 seed 注入，按 sample_id 分流。

    - 退化分布（重采样统计量为常数）不套用区间公式，单独以
      DegenerateInterval 报告；
    - 无效重采样默认显式失败（on_invalid="raise"）；设为 "record" 时
      计数并在 warnings 中显式声明，绝不静默丢弃；
    - BCa 的 z0 / 加速度不可估时显式抛 BCANotEstimableError。
    """
    if isinstance(n_resamples, bool) or not isinstance(n_resamples, int) \
            or n_resamples < 2:
        raise ValueError(
            f"n_resamples 必须为 >= 2 的 int，得到 {n_resamples!r}；"
            "无效次数不会被静默修正"
        )
    if not 0.0 < alpha < 1.0:
        raise ValueError(f"alpha 必须在 (0, 1) 内，得到 {alpha!r}")
    if on_invalid not in ("raise", "record"):
        raise ValueError(f"on_invalid 必须为 'raise' 或 'record'，得到 {on_invalid!r}")
    unknown = set(methods) - {PERCENTILE, BCA}
    if unknown:
        raise ValueError(f"未知区间方法: {sorted(unknown)}")
    if isinstance(sample, TimeSeriesSample):
        if block_length is None:
            raise ValueError("时间序列必须显式给出 block_length（重采样单位）")
        if BCA in methods:
            raise BCANotEstimableError(
                "时间序列（移动块）不支持标准留一 jackknife，BCa 不可估；"
                "请使用 methods=('percentile',)"
            )

    observed = _eval_observed(statistic, _observed_args(sample))
    factory = StreamFactory(seed)
    rng = factory.stream(f"{sample.sample_id}#bootstrap")

    boot_stats = []
    invalid = 0
    for b in range(n_resamples):
        args = _resample_args(sample, rng, block_length, boundary)
        value = _try_eval(statistic, args)
        if value is _INVALID:
            invalid += 1
            if on_invalid == "raise":
                raise InvalidResampleError(
                    f"第 {b} 次重采样统计量无效；不会静默丢弃以免改变置信度。"
                    "如确需排除，请显式设置 on_invalid='record'"
                )
            continue
        boot_stats.append(float(value))

    warnings = []
    if invalid > 0:
        warnings.append(
            f"{invalid}/{n_resamples} 次重采样无效，已按 on_invalid='record' 记录并排除；"
            f"区间基于 {len(boot_stats)} 个有效值，名义置信度 {1 - alpha:.4f} "
            "为条件于有效重采样的近似"
        )
    if len(boot_stats) < 2:
        raise InvalidResampleError(
            f"有效重采样仅 {len(boot_stats)} 个，无法构造区间"
        )

    if all(s == boot_stats[0] for s in boot_stats):
        degenerate = True
        point = boot_stats[0]
        intervals = {
            m: DegenerateInterval(
                point, alpha,
                "重采样统计量分布退化为常数（可能源于样本值全部相同），"
                "区间退化为一点，单独报告",
            )
            for m in methods
        }
    else:
        degenerate = False
        intervals = {}
        for m in methods:
            if m == PERCENTILE:
                intervals[m] = percentile_interval(boot_stats, alpha, quantile_method)
            else:
                jk = jackknife(sample, statistic)
                intervals[m] = bca_interval(
                    boot_stats, observed, jk, alpha, quantile_method
                )

    return BootstrapReport(
        sample_id=sample.sample_id,
        statistic_name=statistic_name or getattr(statistic, "__name__", "statistic"),
        estimate=observed,
        intervals=intervals,
        degenerate=degenerate,
        nature=EMPIRICAL,
        n_resamples=n_resamples,
        invalid_resamples=invalid,
        master_seed=seed,
        sample_sizes=sample.sizes,
        alpha=alpha,
        quantile_method=quantile_method,
        support_size=None,
        warnings=tuple(warnings),
    )


def exact_bootstrap(sample, statistic, *, alpha=0.05, statistic_name=None,
                    max_support=_exact.DEFAULT_MAX_SUPPORT):
    """有限小样本精确枚举：结论为精确（exact），无随机性，无需 seed。

    区间基于精确重采样分布的加权 type1 分位数（见 quantile 模块定义）。
    """
    if not 0.0 < alpha < 1.0:
        raise ValueError(f"alpha 必须在 (0, 1) 内，得到 {alpha!r}")
    if isinstance(sample, TimeSeriesSample):
        raise TypeError("时间序列（移动块）不支持精确枚举，请用经验 bootstrap")
    observed = _eval_observed(statistic, _observed_args(sample))
    dist, total = _exact.exact_distribution(sample, statistic, max_support)
    if len(dist) == 1:
        degenerate = True
        point = next(iter(dist))
        intervals = {
            PERCENTILE: DegenerateInterval(
                point, alpha, "精确重采样分布退化为一点，单独报告"
            )
        }
    else:
        degenerate = False
        low, high = _exact.exact_percentile_interval(dist, total, alpha)
        intervals = {
            PERCENTILE: Interval(low, high, alpha, PERCENTILE, "weighted-type1")
        }
    return BootstrapReport(
        sample_id=sample.sample_id,
        statistic_name=statistic_name or getattr(statistic, "__name__", "statistic"),
        estimate=observed,
        intervals=intervals,
        degenerate=degenerate,
        nature=EXACT,
        n_resamples=None,
        invalid_resamples=0,
        master_seed=None,
        sample_sizes=sample.sizes,
        alpha=alpha,
        quantile_method="weighted-type1",
        support_size=len(dist),
        warnings=(),
    )
