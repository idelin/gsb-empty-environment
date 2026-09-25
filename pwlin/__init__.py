"""pwlin: 1-D multi-channel piecewise-linear interpolation with inverse
queries, dynamic versioned nodes, measurement error intervals, local index
rebuilds, and immutable snapshots.  Pure standard library."""

from .index import ChannelIndex, Segment, SegmentIndex
from .interval import Interval
from .model import (
    Measurement,
    Node,
    NoDataError,
    OutOfDomainError,
    PwlinError,
    UnknownChannelError,
)
from .table import (
    CurveTable,
    InverseEvidence,
    InverseSolution,
    Snapshot,
    ValueEvidence,
)

__all__ = [
    "ChannelIndex",
    "CurveTable",
    "Interval",
    "InverseEvidence",
    "InverseSolution",
    "Measurement",
    "Node",
    "NoDataError",
    "OutOfDomainError",
    "PwlinError",
    "Segment",
    "SegmentIndex",
    "Snapshot",
    "UnknownChannelError",
    "ValueEvidence",
]
