"""Delta judgement between two consecutive samples of one source."""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum

from .model import CounterConfig, Sample, SourceConfig


class Reason(str, Enum):
    """Why a delta judgement came out the way it did."""

    RESET = "reset"  # right sample carries an explicit reset marker
    SINGLE_WRAP_BOUND = "single_wrap_bound"  # gap <= max_interval: <=1 wrap
    GAP_UNDECIDABLE = "gap_undecidable"  # gap > max_interval: k wraps unknown


@dataclass(frozen=True)
class IntervalResult:
    """Judgement for one adjacent pair (left -> right) in the sorted order.

    delta:   definite increment, or None when undecidable.
    min_delta: the increment assuming zero extra wraps (lower bound); always
               available for non-reset pairs. For reset pairs it equals delta.
    """

    source: str
    left_id: str
    right_id: str
    delta: int | None
    min_delta: int
    reason: Reason
    config_version: str

    @property
    def interval_id(self) -> tuple[str, str, str]:
        return (self.source, self.left_id, self.right_id)

    @property
    def certain(self) -> bool:
        return self.delta is not None


def judge_pair(
    left: Sample, right: Sample, src_cfg: SourceConfig, config_version: str
) -> IntervalResult:
    """Decide the increment between two *adjacent* samples.

    Rules:
      * right.reset  -> delta = right.count, definite (counter restarted).
      * gap <= max_interval -> at most one wrap: delta = (r-l) mod 2**w.
      * otherwise -> undecidable: delta is None, min_delta = (r-l) mod 2**w
        (the true value is min_delta + k * 2**w for some unknown k >= 0).
    """
    if left.source != right.source:
        raise ValueError("pair must belong to the same source")
    if not (0 <= left.count < src_cfg.modulus and 0 <= right.count < src_cfg.modulus):
        raise ValueError("count out of range for configured bit width")

    base = IntervalResult(
        source=left.source,
        left_id=left.sample_id,
        right_id=right.sample_id,
        delta=None,
        min_delta=0,
        reason=Reason.GAP_UNDECIDABLE,
        config_version=config_version,
    )

    if right.reset:
        return IntervalResult(
            **{**base.__dict__, "delta": right.count, "min_delta": right.count,
               "reason": Reason.RESET}
        )

    delta_mod = (right.count - left.count) % src_cfg.modulus
    gap = right.event_time - left.event_time
    if gap <= src_cfg.max_interval:
        return IntervalResult(
            **{**base.__dict__, "delta": delta_mod, "min_delta": delta_mod,
               "reason": Reason.SINGLE_WRAP_BOUND}
        )
    return IntervalResult(
        **{**base.__dict__, "min_delta": delta_mod, "reason": Reason.GAP_UNDECIDABLE}
    )
