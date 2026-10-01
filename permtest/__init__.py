"""Permutation-calibrated two-sample statistics for discrete samples.

Pure standard library. Public API:

- :func:`frequencies`, :func:`ecdf_points`, :func:`total_variation`,
  :func:`ks_statistic` -- descriptive statistics.
- :func:`grouped_calibration`, :func:`paired_calibration` -- permutation
  calibration (exact enumeration or Monte Carlo, never normal
  approximation).
- :class:`CalibrationResult`, :class:`ExchangeabilityError`.
"""
from .calibration import (
    CalibrationResult,
    ExchangeabilityError,
    clopper_pearson,
    grouped_calibration,
    paired_calibration,
)
from .combinatorics import (
    iter_combination_shard,
    shard_ranges,
    unrank_combination,
)
from .statistics import (
    ecdf_points,
    frequencies,
    ks_statistic,
    total_variation,
)

__all__ = [
    "CalibrationResult",
    "ExchangeabilityError",
    "clopper_pearson",
    "ecdf_points",
    "frequencies",
    "grouped_calibration",
    "iter_combination_shard",
    "ks_statistic",
    "paired_calibration",
    "shard_ranges",
    "total_variation",
    "unrank_combination",
]
