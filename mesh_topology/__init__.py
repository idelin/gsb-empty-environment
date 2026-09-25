"""Half-edge mesh topology: build, diagnose, suggest, edit transactionally."""
from .diagnostics import Diagnostics, diagnose, format_diagnostics
from .fuzz import random_edits
from .geometry import exact_point, is_zero_area, triangle_double_area_sq
from .mesh import HalfEdgeMesh, MeshError, edge_key
from .repairs import Op, Patch, PatchError, Precondition, suggest_repairs
from .transactions import Transaction

__all__ = [
    "Diagnostics",
    "HalfEdgeMesh",
    "MeshError",
    "Op",
    "Patch",
    "PatchError",
    "Precondition",
    "Transaction",
    "diagnose",
    "edge_key",
    "exact_point",
    "format_diagnostics",
    "is_zero_area",
    "random_edits",
    "suggest_repairs",
    "triangle_double_area_sq",
]
