"""Reference implementation: full sort + recompute from scratch.

Used to cross-check the incremental engine: after any sequence of
inserts/retracts, the engine's interval judgements and totals must equal
`recompute(current_samples, config)`.
"""

from __future__ import annotations

from dataclasses import dataclass

from .delta import IntervalResult, judge_pair
from .engine import SourceTotal
from .model import CounterConfig, Sample


@dataclass(frozen=True)
class ReferenceState:
    intervals: dict[tuple[str, str, str], IntervalResult]
    totals: dict[str, SourceTotal]


def recompute(samples: list[Sample], config: CounterConfig) -> ReferenceState:
    by_source: dict[str, list[Sample]] = {}
    for s in samples:
        config.source(s.source)  # validate source exists
        by_source.setdefault(s.source, []).append(s)

    intervals: dict[tuple[str, str, str], IntervalResult] = {}
    totals: dict[str, SourceTotal] = {}
    for source, group in by_source.items():
        group.sort(key=lambda s: s.order_key)
        src_cfg = config.source(source)
        definite = 0
        minimum = 0
        uncertain: list[tuple[str, str, str]] = []
        for left, right in zip(group, group[1:]):
            res = judge_pair(left, right, src_cfg, config.version)
            intervals[res.interval_id] = res
            if res.certain:
                definite += res.delta  # type: ignore[operator]
                minimum += res.delta  # type: ignore[operator]
            else:
                minimum += res.min_delta
                uncertain.append(res.interval_id)
        totals[source] = SourceTotal(definite, minimum, tuple(sorted(uncertain)))
    return ReferenceState(intervals=intervals, totals=totals)
