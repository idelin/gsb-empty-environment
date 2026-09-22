"""Data model for out-of-order wrap-around counter analysis.

Ordering / tie-breaking rules (documented contract):
  Samples of one source are ordered by the key
      (event_time, seq, sample_id)
  i.e. earlier event_time first; identical event_time is decided by the
  *lower* seq; identical (event_time, seq) pairs are decided by sample_id
  so the order is total and deterministic. seq is a per-source sequence
  number assigned at production time, so a lower seq was produced first.
"""

from __future__ import annotations

from dataclasses import dataclass, field
from typing import Any


@dataclass(frozen=True)
class Sample:
    """One counter reading.

    sample_id: globally unique identifier of this reading.
    source:    logical counter source (device/stream) name.
    seq:       per-source production sequence number (tie-breaker).
    event_time: timestamp of the reading (any monotone unit, e.g. seconds).
    count:     raw counter value, must fit the source bit width.
    reset:     explicit reset marker: the device restarted its counter at
               this sample, so `count` is the value accumulated *since*
               the reset (delta from the previous sample is exactly
               `count`, regardless of the sampling gap).
    """

    sample_id: str
    source: str
    seq: int
    event_time: float
    count: int
    reset: bool = False

    @property
    def order_key(self) -> tuple[float, int, str]:
        return (self.event_time, self.seq, self.sample_id)


@dataclass(frozen=True)
class SourceConfig:
    """Per-source judgement parameters.

    bit_width:    counter wraps modulo 2**bit_width.
    max_interval: maximum sampling gap (in event_time units) for which the
                  "at most one wrap inside the gap" assumption holds, making
                  the delta decidable. Larger gaps are undecidable because
                  the counter may have wrapped an unknown number of times.
    """

    bit_width: int
    max_interval: float

    def __post_init__(self) -> None:
        if self.bit_width < 1:
            raise ValueError("bit_width must be >= 1")
        if self.max_interval < 0:
            raise ValueError("max_interval must be >= 0")

    @property
    def modulus(self) -> int:
        return 1 << self.bit_width


@dataclass(frozen=True)
class CounterConfig:
    """Whole-analysis configuration. `version` is stamped onto every
    IntervalResult so downstream consumers can tell which configuration
    produced a judgement."""

    sources: dict[str, SourceConfig]
    version: str = "v1"

    def source(self, name: str) -> SourceConfig:
        try:
            return self.sources[name]
        except KeyError:
            raise KeyError(f"no config for source {name!r}") from None
