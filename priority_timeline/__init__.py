"""带优先级的区间值叠加时间轴。

- 时间精确表示：合成偏移用 Fraction，墙钟用带时区 datetime。
- 区分三种时间概念：WallClock（墙钟时间）、TimePoint（合成时间点）、
  Duration（持续时长），三者之间不允许隐式混用。
- OffsetTable：合成偏移 <-> 墙钟 的分段线性转换表。
- Timeline：把带优先级的区间值叠成不重叠时间轴，最高优先级生效，
  同级冲突显式保留，支持局部更新与来源跟踪。
"""

from .time_model import Duration, OffsetTable, TimePoint, WallClock
from .core import ConflictError, Entry, Segment, Timeline

__all__ = [
    "Duration",
    "OffsetTable",
    "TimePoint",
    "WallClock",
    "ConflictError",
    "Entry",
    "Segment",
    "Timeline",
]
