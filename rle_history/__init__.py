"""带来源索引的设备状态历史运行长度编码库。"""

from .core import (
    Event,
    EventRef,
    Segment,
    SegmentPatch,
    StateHistory,
    apply_patch,
    build_segments,
)

__all__ = [
    "Event",
    "EventRef",
    "Segment",
    "SegmentPatch",
    "StateHistory",
    "apply_patch",
    "build_segments",
]

__version__ = "0.1.0"
