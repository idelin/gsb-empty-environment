"""Exact equal-arc-length sampling for multi-run polylines with shared seams."""

from .model import BREAK, Path, Segment, SubPath, Vertex
from .rational import NonRationalLengthError, Point, as_fraction, as_point, distance
from .sampling import (
    Cursor,
    Patch,
    PathSampler,
    Sample,
    SampleMove,
    SamplingResult,
    SourceRef,
    sample_path,
)
from .seam import EdgeSample, SeamSampler

__all__ = [
    "BREAK",
    "Cursor",
    "EdgeSample",
    "NonRationalLengthError",
    "Patch",
    "Path",
    "PathSampler",
    "Point",
    "Sample",
    "SampleMove",
    "SamplingResult",
    "SeamSampler",
    "Segment",
    "SourceRef",
    "SubPath",
    "Vertex",
    "as_fraction",
    "as_point",
    "distance",
    "sample_path",
]
