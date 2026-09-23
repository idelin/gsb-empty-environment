"""Exact rational piecewise function toolkit (standard library only)."""

from .interval import Interval
from .continuity import (
    BoundaryReport,
    ContinuityReport,
    SingularityReport,
    check_continuity,
)
from .piecewise import Piece, PiecewiseModel, PointCell, SegCell
from .poly import Poly
from .rational import RationalFunc, SideLimit
from .roots import (
    IsolatedRoot,
    RationalRoot,
    RootAnalysis,
    UnknownCluster,
    analyze_roots,
)

__all__ = [
    "BoundaryReport",
    "ContinuityReport",
    "SingularityReport",
    "check_continuity",
    "Interval",
    "Piece",
    "PiecewiseModel",
    "PointCell",
    "SegCell",
    "Poly",
    "RationalFunc",
    "SideLimit",
    "IsolatedRoot",
    "RationalRoot",
    "RootAnalysis",
    "UnknownCluster",
    "analyze_roots",
]
