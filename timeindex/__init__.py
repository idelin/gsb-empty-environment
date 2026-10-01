"""timeindex: exact projection of continuous time intervals onto
piecewise-sampled sequences with rational rates, epochs, gaps and
versioned clock calibration."""
from .exact import Duration, TimePoint, WallClockTime, as_fraction
from .intervals import Interval
from .offset import AffineSegment, Clock, OffsetTable
from .blocks import SampleBlock, SampledSequence
from .projection import (
    MISSING_SAMPLES,
    NO_COVERAGE,
    Gap,
    IndexSlice,
    NonContiguousError,
    Projection,
    RoundingEvidence,
    project,
)
from .projector import Cursor, SequenceProjector

__all__ = [
    "Duration", "TimePoint", "WallClockTime", "as_fraction",
    "Interval",
    "AffineSegment", "Clock", "OffsetTable",
    "SampleBlock", "SampledSequence",
    "Gap", "IndexSlice", "NonContiguousError", "Projection",
    "RoundingEvidence", "project", "MISSING_SAMPLES", "NO_COVERAGE",
    "Cursor", "SequenceProjector",
]
