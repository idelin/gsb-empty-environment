"""ecdfcal: ECDF statistics with exact / Monte Carlo permutation calibration."""
from .ecdf import ECDF
from .statistics import frequency_table, ks_statistic, tv_distance
from .combinatorics import rank_combination, shard_ranges, unrank_combination
from .calibration import (
    CalibrationResult,
    Design,
    Statistic,
    calibrate,
    clopper_pearson_interval,
)

__all__ = [
    "ECDF",
    "frequency_table",
    "ks_statistic",
    "tv_distance",
    "rank_combination",
    "shard_ranges",
    "unrank_combination",
    "CalibrationResult",
    "Design",
    "Statistic",
    "calibrate",
    "clopper_pearson_interval",
]
