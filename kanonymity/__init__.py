"""k-匿名合成数据工具：等价类、层级泛化、抑制试算、联合优化与增量修复。

仅用于合成数据。报告的信息损失仅为声明指标（NCP + 抑制惩罚），
不构成绝对匿名或完整隐私保障的声明。
"""
from .core import Evaluation, Record, compute_classes, evaluate
from .hierarchy import NULL, ROOT, Hierarchy
from .incremental import IncrementalAnonymizer
from .optimize import Evidence, SearchResult, Solution, optimize
from .verify import brute_force_best_loss, check_optimal

__all__ = [
    "Evaluation",
    "Evidence",
    "Hierarchy",
    "IncrementalAnonymizer",
    "NULL",
    "ROOT",
    "Record",
    "SearchResult",
    "Solution",
    "brute_force_best_loss",
    "check_optimal",
    "compute_classes",
    "evaluate",
    "optimize",
]
