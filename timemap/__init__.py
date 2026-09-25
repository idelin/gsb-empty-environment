"""timemap：含暂停/倍速/反向段的分段仿射时间映射库。

- 精确有理数时间（Fraction），区分墙钟时间、逻辑时间点与持续时长
- 像与逆像返回完整区间集合，端点开闭不丢失
- 常量（暂停）段的逆像返回整段原像
- 映射复合、局部修订（版本化）、来源证据
- 独立有理点枚举检验复合关系；巨大区间按断点处理
"""
from .checker import sample_points, verify_composition
from .intervals import Interval, IntervalSet
from .mapping import (
    Contribution,
    MappingResult,
    PiecewiseAffineMap,
    Segment,
)
from .quantities import Duration, TimePoint, WallClock, to_fraction
from .table import OffsetTable
from .versioned import VersionedTimeline

__all__ = [
    "Contribution",
    "Duration",
    "Interval",
    "IntervalSet",
    "MappingResult",
    "OffsetTable",
    "PiecewiseAffineMap",
    "Segment",
    "TimePoint",
    "VersionedTimeline",
    "WallClock",
    "sample_points",
    "to_fraction",
    "verify_composition",
]
