"""silence_split —— 多轨静音检测与片段切分。

延迟与数值容差声明
------------------
* 检测粒度为一个窗口(``window_size`` 个采样点)。
* 静音确认延迟上界 = ``min_silence + window_size`` 个采样点
  (见 :attr:`DetectorConfig.confirmation_latency_samples`)。
* 片段导出延迟上界 = 确认延迟 + ``post_pad``
  (见 :attr:`DetectorConfig.export_latency_samples`)。
* RMS 与门限比较采用相对容差 ``NUMERIC_TOLERANCE``(默认 1e-9);
  落在容差死区内的取值保持原迟滞状态,不翻转。
"""
from .audio import AudioStore, MultiTrackStore
from .constants import NUMERIC_TOLERANCE
from .core import (DetectorConfig, Hysteresis, absorb_short_sound,
                   sound_cores, window_rms)
from .events import (SegmentCreated, SegmentDiscarded, SegmentMerged,
                     SegmentRevised, SegmentSplit)
from .segments import Segment, build_segments, make_segment_id
from .session import Exporter, Session, diff_segments
from .streaming import FlagFinalizer, StreamingDetector

__all__ = [
    "NUMERIC_TOLERANCE",
    "DetectorConfig",
    "Hysteresis",
    "absorb_short_sound",
    "sound_cores",
    "window_rms",
    "Segment",
    "build_segments",
    "make_segment_id",
    "AudioStore",
    "MultiTrackStore",
    "SegmentCreated",
    "SegmentRevised",
    "SegmentMerged",
    "SegmentSplit",
    "SegmentDiscarded",
    "Exporter",
    "Session",
    "diff_segments",
    "FlagFinalizer",
    "StreamingDetector",
]
