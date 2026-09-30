"""kanonym：合成数据上的 k-匿名试算工具（非形式化隐私证明）。"""
from .dataset import Dataset, Record
from .hierarchy import NULL_LABEL, Hierarchy
from .incremental import Anonymizer
from .solver import (
    Evaluation,
    Result,
    below_k_records,
    build_evidence,
    evaluate,
    group_records,
    record_loss,
    solve_exact,
    subject_count,
)

__all__ = [
    "Anonymizer",
    "Dataset",
    "Evaluation",
    "Hierarchy",
    "NULL_LABEL",
    "Record",
    "Result",
    "below_k_records",
    "build_evidence",
    "evaluate",
    "group_records",
    "record_loss",
    "solve_exact",
    "subject_count",
]
