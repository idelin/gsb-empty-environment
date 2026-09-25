"""半边网格拓扑库：诊断、修复建议、事务化编辑。"""
from .mesh import (
    HalfEdge,
    Mesh,
    edge_key,
    exact_midpoint,
    triangle_is_degenerate,
)
from .diagnostics import Diagnostics, analyze, vertex_fan_components
from .repair import RepairPatch, suggest_repairs

__all__ = [
    "HalfEdge",
    "Mesh",
    "edge_key",
    "exact_midpoint",
    "triangle_is_degenerate",
    "Diagnostics",
    "analyze",
    "vertex_fan_components",
    "RepairPatch",
    "suggest_repairs",
]
