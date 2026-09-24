"""hyperproj: incremental hierarchical graph projection.

Collapse groups of vertices into super-nodes, aggregate cross-group edges
into super-edges with full original-edge evidence, and keep nested member
counts, reverse lookups and multiplicities consistent under moves, undo,
named snapshots and independent forked views.
"""

from .errors import (
    HierarchyCycleError,
    IdConflictError,
    InvalidIntervalError,
    ProjectionError,
    UnknownIdError,
)
from .model import (
    AddEdge,
    AddGroup,
    AddVertex,
    Collapse,
    Edge,
    Expand,
    MoveGroup,
    MoveVertex,
)
from .projection import Projection

__all__ = [
    "AddEdge",
    "AddGroup",
    "AddVertex",
    "Collapse",
    "Edge",
    "Expand",
    "HierarchyCycleError",
    "IdConflictError",
    "InvalidIntervalError",
    "MoveGroup",
    "MoveVertex",
    "Projection",
    "ProjectionError",
    "UnknownIdError",
]
