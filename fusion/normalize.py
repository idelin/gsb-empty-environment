"""Min-max normalization driven by *explicit* statistics only.

Public rules (mirrored in README.md):

* Normalization uses a :class:`FieldStats` (min/max) that is computed once,
  declared, and frozen for the whole query.  It is NEVER recomputed from the
  candidate set that the TopK algorithm happens to have fetched so far, so
  changing the fetched candidate set cannot silently move the baseline.
* ``max == min`` (a field whose scores are all identical) must not divide by
  zero: every score maps to ``1.0`` if the common value is non-zero, else
  ``0.0``.  Such a field has no discriminative power but stays well-defined.
* Results are clamped to ``[0.0, 1.0]``.
"""

from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class FieldStats:
    """Explicit per-field statistics used as the normalization baseline."""

    minimum: float
    maximum: float

    def __post_init__(self) -> None:
        if self.maximum < self.minimum:
            raise ValueError("FieldStats: maximum < minimum")


def normalize(raw: float, stats: FieldStats) -> float:
    """Map *raw* into ``[0, 1]`` using the explicit *stats* baseline."""
    lo, hi = stats.minimum, stats.maximum
    if hi == lo:
        # All identical scores: avoid division by zero with a public rule.
        return 0.0 if hi == 0 else 1.0
    value = (raw - lo) / (hi - lo)
    if value < 0.0:
        return 0.0
    if value > 1.0:
        return 1.0
    return value
