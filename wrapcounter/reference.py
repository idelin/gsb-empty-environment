"""全量排序重算参考实现。

对每个来源：按 (event_time, seq) 排序（这是同时间不同 seq 的决胜方式），
从首个采样的锚点开始逐对判定。该实现不维护任何增量状态，任何时刻都可以
拿它与增量引擎的修订结果逐步对照。
"""

from __future__ import annotations

from collections.abc import Mapping

from .judge import judge_interval
from .model import (
    IntervalResult,
    Sample,
    SourceConfig,
    SourceTotal,
    build_source_total,
)


def order_samples(samples: list[Sample]) -> list[Sample]:
    """同时间按 seq 升序决胜；再相同则按 sample_id 保证全序确定。"""
    return sorted(samples, key=lambda s: (s.event_time, s.seq, s.sample_id))


def recompute_source(
    config: SourceConfig,
    samples: list[Sample],
) -> tuple[list[Sample], list[IntervalResult], SourceTotal]:
    """对单个来源做一次全量排序重算。

    返回 (全序采样, 区间列表, 总量)。区间列表中首个“ROOTSAMPLE”锚点区间
    不参与总量（它没有前序差分）；参与总量的是其后的相邻对区间。
    """

    for s in samples:
        if s.source != config.source:
            raise ValueError(f"采样 {s.sample_id} 的 source 与配置不一致")

    ordered = order_samples(samples)
    intervals: list[IntervalResult] = []
    prev: Sample | None = None
    for curr in ordered:
        iv = judge_interval(prev, curr, config)
        intervals.append(iv)
        prev = curr

    total = build_source_total(
        config.source,
        ordered,
        intervals[1:] if ordered else [],
        config,
    )
    return ordered, intervals, total


def recompute_all(
    configs: Mapping[str, SourceConfig],
    samples: list[Sample],
) -> dict[str, tuple[list[Sample], list[IntervalResult], SourceTotal]]:
    """对所有来源全量重算。samples 允许跨来源混合。"""
    by_source: dict[str, list[Sample]] = {src: [] for src in configs}
    for s in samples:
        if s.source not in configs:
            raise KeyError(f"采样 {s.sample_id} 的 source={s.source} 未注册配置")
        by_source[s.source].append(s)
    return {
        src: recompute_source(configs[src], bucket)
        for src, bucket in by_source.items()
    }
