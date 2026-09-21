"""Full-sort recompute reference implementation.

Stateless: sorts every live sample of a source and re-judges all intervals
from scratch. Used as the ground truth that the incremental revision stream
in :mod:`counter_analysis.store` is checked against, step by step.
"""

from __future__ import annotations

from typing import Iterable

from .model import Diff, Sample, SourceConfig, compute_diff


def reference_diffs(samples: Iterable[Sample], config: SourceConfig) -> list[Diff]:
    ordered = sorted(samples, key=lambda s: s.order_key())
    out: list[Diff] = []
    prev: Sample | None = None
    for sample in ordered:
        out.append(compute_diff(prev, sample, config))
        prev = sample
    return out
