"""k_anonymizer：有限泛化树上的 k-匿名全局层级组合搜索。

仅处理合成数据；统计实验结果不构成形式化隐私证明。
"""

from .dataset import generate_synthetic
from .hierarchy import Hierarchy, HierarchyError
from .loss import combination_loss, field_loss
from .report import build_report, verify_report
from .search import (
    SearchResult,
    Status,
    beam_search,
    equivalence_classes,
    exact_search,
    satisfies_k,
)

__all__ = [
    "Hierarchy",
    "HierarchyError",
    "SearchResult",
    "Status",
    "beam_search",
    "build_report",
    "combination_loss",
    "equivalence_classes",
    "exact_search",
    "field_loss",
    "generate_synthetic",
    "satisfies_k",
    "verify_report",
]
