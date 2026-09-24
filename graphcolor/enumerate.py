"""Independent brute-force enumeration of legal colorings, used to check
that solver pruning never removes a legal solution, and to compare solution
classes with symmetry breaking on vs off."""

from __future__ import annotations

from dataclasses import dataclass, field
from itertools import product

from .model import Problem, color_symmetry_classes
from .verify import verify


def legal_colorings(problem: Problem) -> list[dict[str, str]]:
    """All legal colorings by exhaustive enumeration + independent verify."""
    problem.validate()
    vertices = sorted(problem.graph.vertices)
    domains = []
    for v in vertices:
        if v in problem.precoloring:
            domains.append((problem.precoloring[v],))
        else:
            domains.append(tuple(problem.colors))
    out = []
    for combo in product(*domains):
        coloring = dict(zip(vertices, combo))
        if not verify(problem, coloring):
            out.append(coloring)
    return out


def canonical_form(problem: Problem, coloring: dict[str, str]) -> tuple[str, ...]:
    """Canonical representative of a coloring's symmetry class: rename colors
    within each interchangeable class by order of first appearance (vertices
    scanned in sorted-id order)."""
    classes = color_symmetry_classes(problem)
    member: dict[str, tuple[str, ...]] = {}
    for cls in classes:
        for c in cls:
            member[c] = cls
    rename: dict[str, str] = {}
    used_in_class: dict[tuple[str, ...], list[str]] = {cls: [] for cls in classes}
    key = []
    for v in sorted(problem.graph.vertices):
        c = coloring[v]
        cls = member.get(c)
        if cls is None:
            key.append(c)
            continue
        if c not in rename:
            nxt = next(x for x in cls if x not in used_in_class[cls])
            rename[c] = nxt
            used_in_class[cls].append(nxt)
        key.append(rename[c])
    return tuple(key)


@dataclass
class ComparisonReport:
    ok: bool
    num_legal: int = 0
    num_classes: int = 0
    solver_missing: list[tuple[str, ...]] = field(default_factory=list)
    solver_extra: list[tuple[str, ...]] = field(default_factory=list)
    broken_not_canonical: int = 0
    notes: list[str] = field(default_factory=list)


def compare_with_solver(problem: Problem) -> ComparisonReport:
    """Cross-check the solver against brute-force enumeration.

    - With symmetry breaking OFF, the solver must find exactly the legal set
      (pruning removed no legal solution).
    - With symmetry breaking ON, the solver's solutions must be exactly the
      canonical representatives of the legal set's symmetry classes.
    """
    from .solver import Solver  # local import: this module stays solver-free

    legal = legal_colorings(problem)
    legal_keys = {_key(problem, c) for c in legal}

    off = Solver(problem, use_symmetry=False).find_all()
    on = Solver(problem, use_symmetry=True).find_all()

    report = ComparisonReport(ok=True, num_legal=len(legal))
    off_keys = {_key(problem, c) for c in off.solutions}
    if off_keys != legal_keys:
        report.ok = False
        report.solver_missing = sorted(legal_keys - off_keys)
        report.solver_extra = sorted(off_keys - legal_keys)
        report.notes.append("solver (symmetry off) != brute-force legal set")

    classes = {canonical_form(problem, c) for c in legal}
    report.num_classes = len(classes)
    on_keys = {_key(problem, c) for c in on.solutions}
    if on_keys != classes:
        report.ok = False
        report.notes.append("solver (symmetry on) != canonical class reps")
    for c in on.solutions:
        if _key(problem, c) != canonical_form(problem, c):
            report.broken_not_canonical += 1
            report.ok = False
    return report


def _key(problem: Problem, coloring: dict[str, str]) -> tuple[str, ...]:
    return tuple(coloring[v] for v in sorted(problem.graph.vertices))
