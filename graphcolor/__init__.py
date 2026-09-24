"""Finite-color graph coloring with precoloring, capacities, symmetry
breaking, reason-tracking propagation, retractable assumptions, conflict
cores, an independent verifier and a brute-force cross-checker."""

from .model import Edge, Graph, Interval, Problem, coloring_key, color_symmetry_classes
from .solver import (
    AssumptionReason,
    CapacityReason,
    ConflictInfo,
    DecisionReason,
    EdgeReason,
    PrecolorReason,
    SolveResult,
    Solver,
    Status,
    TraceEvent,
    reason_str,
    trace_digest,
)
from .verify import verify
from .enumerate import ComparisonReport, canonical_form, compare_with_solver, legal_colorings
from .core import ConstraintRef, CoreResult, all_constraints, build_subproblem, minimal_conflict_core

__all__ = [
    "Edge", "Graph", "Interval", "Problem", "coloring_key", "color_symmetry_classes",
    "AssumptionReason", "CapacityReason", "ConflictInfo", "DecisionReason",
    "EdgeReason", "PrecolorReason", "SolveResult", "Solver", "Status",
    "TraceEvent", "reason_str", "trace_digest",
    "verify", "ComparisonReport", "canonical_form", "compare_with_solver",
    "legal_colorings", "ConstraintRef", "CoreResult", "all_constraints",
    "build_subproblem", "minimal_conflict_core",
]
