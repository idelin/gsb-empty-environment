"""equivstats：等价类敏感值不同数与熵指标（仅用于合成数据的统计实验）。"""

from .core import Dataset, NullPolicy, Record
from .hierarchy import Hierarchy
from .metrics import (
    ClassReport,
    Interval,
    ValueContribution,
    Verdict,
    class_report,
    decide_entropy,
    distinct_count,
    entropy_interval,
    evaluate_dataset,
)
from .suppression import (
    all_classes_pass,
    exact_min_suppression,
    failing_classes,
    greedy_suppression,
)
from .verify import merge_shards, recount, verify_incremental, verify_shard_merge

__all__ = [
    "Dataset",
    "NullPolicy",
    "Record",
    "Hierarchy",
    "Interval",
    "Verdict",
    "ClassReport",
    "ValueContribution",
    "distinct_count",
    "entropy_interval",
    "decide_entropy",
    "class_report",
    "evaluate_dataset",
    "failing_classes",
    "all_classes_pass",
    "exact_min_suppression",
    "greedy_suppression",
    "recount",
    "verify_incremental",
    "merge_shards",
    "verify_shard_merge",
]
