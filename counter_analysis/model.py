"""Core data model for out-of-order counter analysis.

Ordering / tie-break rule (documented contract):
    Samples within one source are ordered by ``(event_time, seq, sample_id)``.
    - Primary key is ``event_time``.
    - Samples sharing the same ``event_time`` are ordered by ``seq``; the
      larger ``seq`` is considered the *later* observation (``seq`` is a
      per-source, monotonically increasing emission counter).
    - If both are equal, ``sample_id`` lexicographic order is the final
      deterministic fallback. Producers must not emit two samples with the
      same id.
"""

from __future__ import annotations

from dataclasses import dataclass
from enum import Enum


class DiffStatus(str, Enum):
    """How the increment between two adjacent samples was explained."""

    ORIGIN = "origin"          # first sample of the stream; baseline unknown
    DETERMINED = "determined"  # at most one wrap possible inside the interval
    RESET = "reset"            # explicit reset flag on the right sample
    UNCERTAIN = "uncertain"    # interval too long: multiple wraps/resets possible


@dataclass(frozen=True)
class SourceConfig:
    """Per-source judgement configuration.

    ``max_interval`` is the largest sampling gap (in event-time units) for
    which the "at most one wrap" assumption holds, i.e. the longest interval
    in which the physical counter can increment by less than one full modulus.
    """

    bit_width: int
    max_interval: float
    config_version: str = "v1"

    @property
    def modulus(self) -> int:
        return 1 << self.bit_width


@dataclass(frozen=True)
class Sample:
    sample_id: str
    source: str
    event_time: float
    seq: int
    count: int
    reset: bool = False

    def order_key(self) -> tuple[float, int, str]:
        return (self.event_time, self.seq, self.sample_id)


@dataclass(frozen=True)
class Diff:
    """Increment judgement over the interval (left, right].

    ``left_id``/``right_id`` are the ids of the samples used for the
    judgement and ``config_version`` records the configuration that produced
    it, so downstream consumers can audit every number.
    """

    left_id: str | None
    right_id: str
    status: DiffStatus
    delta: int | None
    reason: str
    config_version: str

    @property
    def key(self) -> tuple[str | None, str]:
        return (self.left_id, self.right_id)


def compute_diff(left: Sample | None, right: Sample, config: SourceConfig) -> Diff:
    """Judge the increment between adjacent samples ``left`` -> ``right``.

    Rules, in priority order:
      1. ``right.reset`` is set: trust the explicit flag. The counter
         restarted at 0 and counted up to ``right.count``; the increment is
         exactly ``right.count`` regardless of the gap.
      2. ``left is None``: stream origin, no baseline, delta unknown.
      3. gap > ``max_interval``: the "at most one wrap" assumption is
         violated, so the increment is UNCERTAIN (delta ``None``).
      4. Otherwise the increment is DETERMINED and equals
         ``(right.count - left.count) mod 2**bit_width``.
    """
    version = config.config_version
    if right.reset:
        return Diff(
            left.sample_id if left else None,
            right.sample_id,
            DiffStatus.RESET,
            right.count,
            "explicit reset flag: counter restarted at 0",
            version,
        )
    if left is None:
        return Diff(
            None,
            right.sample_id,
            DiffStatus.ORIGIN,
            None,
            "first sample of stream; baseline unknown",
            version,
        )
    gap = right.event_time - left.event_time
    if gap > config.max_interval:
        return Diff(
            left.sample_id,
            right.sample_id,
            DiffStatus.UNCERTAIN,
            None,
            f"gap {gap:g} > max_interval {config.max_interval:g}; "
            "multiple wraps or resets possible",
            version,
        )
    delta = (right.count - left.count) % config.modulus
    return Diff(
        left.sample_id,
        right.sample_id,
        DiffStatus.DETERMINED,
        delta,
        "gap within max_interval: at most one wrap",
        version,
    )
