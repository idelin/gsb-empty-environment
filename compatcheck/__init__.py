"""compatcheck: offline API compatibility impact analysis.

Compares two exported-symbol snapshots of a package (local JSON only),
classifies each change as breaking or compatible, and propagates breaking
changes along dependency call edges to the application roots.
"""
from .core import (
    Change,
    Edge,
    Param,
    Report,
    Symbol,
    TypeEnv,
    analyze,
    diff_symbols,
    propagate,
    required_bump,
)
from .semver import Version, VersionError, bump_level

__all__ = [
    "Change",
    "Edge",
    "Param",
    "Report",
    "Symbol",
    "TypeEnv",
    "Version",
    "VersionError",
    "analyze",
    "bump_level",
    "diff_symbols",
    "propagate",
    "required_bump",
]

__version__ = "0.1.0"
