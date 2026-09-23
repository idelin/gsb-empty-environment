"""Finite-color vertex coloring with capacities, precoloring, reasons and cores."""

from .core import ConflictCore, find_conflict_core
from .enumerator import (canonical_form, canonical_set, enumerate_all_colorings)
from .graph import Edge, Graph
from .intervals import interval_overlap_graph
from .solver import (BUDGET_EXHAUSTED, FEASIBLE, INFEASIBLE, Conflict, Reason,
                     SolveResult, Solver, solve_lex_min)
from .verifier import verify_coloring

__all__ = [
    "BUDGET_EXHAUSTED",
    "FEASIBLE",
    "INFEASIBLE",
    "Conflict",
    "ConflictCore",
    "Edge",
    "Graph",
    "Reason",
    "SolveResult",
    "Solver",
    "canonical_form",
    "canonical_set",
    "enumerate_all_colorings",
    "find_conflict_core",
    "interval_overlap_graph",
    "solve_lex_min",
    "verify_coloring",
]
