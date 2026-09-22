"""Source-indexed run-length encoding for device status history."""

from .model import Event, Patch, Segment
from .history import StatusHistory, apply_patch, build_segments, diff_segments

__all__ = [
    "Event",
    "Patch",
    "Segment",
    "StatusHistory",
    "apply_patch",
    "build_segments",
    "diff_segments",
]
