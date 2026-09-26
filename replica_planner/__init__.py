"""副本放置规划器：故障域隔离 + 容量约束 + 传输成本优化（纯标准库）。"""
from .diagnostics import CapacityDiagnostics, RackReport, build_diagnostics
from .migration import MigrationOp, build_migration_plan, simulate_plan
from .model import Node, ObjectSpec, to_fraction, validate_placement
from .solver import SolveResult, Status, solve

__all__ = [
    "CapacityDiagnostics",
    "MigrationOp",
    "Node",
    "ObjectSpec",
    "RackReport",
    "SolveResult",
    "Status",
    "build_diagnostics",
    "build_migration_plan",
    "simulate_plan",
    "solve",
    "to_fraction",
    "validate_placement",
]
