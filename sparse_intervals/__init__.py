"""稀疏区间冲突修复：一维规范时间线 + 二维矩形分区。

规则选择顺序（一维与二维一致）：
    优先级高者优先 -> 版本高者优先 -> ID 小者优先。
"""

from .timeline1d import Rule, Segment, Timeline
from .rect2d import Rule2D, RectEngine
from .solver import (
    BudgetExceeded,
    greedy_partition,
    min_rect_partition,
    components_lower_bound,
    verify_grid_partition,
    verify_partition_continuous,
)

__all__ = [
    "Rule",
    "Segment",
    "Timeline",
    "Rule2D",
    "RectEngine",
    "BudgetExceeded",
    "greedy_partition",
    "min_rect_partition",
    "components_lower_bound",
    "verify_grid_partition",
    "verify_partition_continuous",
]
