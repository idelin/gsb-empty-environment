"""silseg: multi-track silence detection and segment splitting.

Pure standard library. Samples and sample rates come from local fixtures;
no audio devices are involved.
"""

from .constants import RMS_ABS_TOL, RMS_REL_TOL
from .detector import (
    STRATEGY_ALL,
    STRATEGY_ANY,
    StreamingDetector,
    hysteresis_update,
)
from .incremental import IncrementalSegmenter, segment_tracks
from .segments import ExportedSegment, Segment, extract_segments, to_windows
from .track import Track

__all__ = [
    "RMS_ABS_TOL",
    "RMS_REL_TOL",
    "STRATEGY_ALL",
    "STRATEGY_ANY",
    "ExportedSegment",
    "IncrementalSegmenter",
    "Segment",
    "StreamingDetector",
    "Track",
    "extract_segments",
    "hysteresis_update",
    "segment_tracks",
    "to_windows",
]
