"""Backtracking solver with forward-checking propagation.

Features:
- Finite colors, precoloring, per-color capacities.
- Every color deletion carries a reason (edge, capacity, precolor, decision,
  assumption). Domains, capacities and the reason graph are restored on
  backtrack via a trail.
- Retractable assumptions (push/pop).
- Color symmetry breaking by value precedence within interchangeable classes.
- Fixed vertex order (sorted ids) + color order => the first solution found
  is the lexicographically smallest feasible coloring.
- Budget in decision nodes; exhaustion is reported distinctly from proven
  infeasibility.
- Deterministic, reproducible search trace; event n occupies the logical
  time interval [n, n+1).
"""

from __future__ import annotations

import hashlib
from dataclasses import dataclass, field
from enum import Enum
from typing import Optional, Union

from .model import Interval, Problem, color_symmetry_classes


class Status(Enum):
    SAT = "sat"
    UNSAT = "unsat"  # proven infeasible
    BUDGET_EXHAUSTED = "budget_exhausted"  # neither sat nor proven unsat


# ---------------------------------------------------------------- reasons

@dataclass(frozen=True)
class PrecolorReason:
    vertex: str


@dataclass(frozen=True)
class EdgeReason:
    edge_id: str
    neighbor: str
    color: str


@dataclass(frozen=True)
class CapacityReason:
    color: str


@dataclass(frozen=True)
class DecisionReason:
    vertex: str
    color: str


@dataclass(frozen=True)
class AssumptionReason:
    vertex: str
    color: str
    sign: bool


Reason = Union[PrecolorReason, EdgeReason, CapacityReason, DecisionReason, AssumptionReason]


def reason_str(r: Reason) -> str:
    if isinstance(r, PrecolorReason):
        return f"precolor({r.vertex})"
    if isinstance(r, EdgeReason):
        return f"edge({r.edge_id}):{r.neighbor}={r.color}"
    if isinstance(r, CapacityReason):
        return f"capacity({r.color})"
    if isinstance(r, DecisionReason):
        return f"decision({r.vertex}={r.color})"
    return f"assumption({r.vertex}{'=' if r.sign else '!='}{r.color})"


# ---------------------------------------------------------------- trace

@dataclass(frozen=True)
class TraceEvent:
    step: int
    kind: str  # decide|remove|assign|conflict|backtrack|solution|assume|retract|init
    detail: str

    @property
    def interval(self) -> Interval:
        """Logical time of the event: [step, step+1), left-closed right-open."""
        return Interval(self.step, self.step + 1)


def trace_digest(trace: list[TraceEvent]) -> str:
    h = hashlib.sha256()
    for e in trace:
        h.update(f"{e.step}|{e.kind}|{e.detail}\n".encode())
    return h.hexdigest()


# ---------------------------------------------------------------- results

@dataclass
class ConflictInfo:
    vertex: str
    reasons: dict[str, Reason]  # color -> why it was removed
    note: str = ""


@dataclass
class SolveResult:
    status: Status
    coloring: Optional[dict[str, str]] = None
    solutions: list[dict[str, str]] = field(default_factory=list)
    conflict: Optional[ConflictInfo] = None
    nodes: int = 0
    trace: list[TraceEvent] = field(default_factory=list)


class _BudgetExhausted(Exception):
    pass


# ---------------------------------------------------------------- solver

class Solver:
    def __init__(
        self,
        problem: Problem,
        use_symmetry: bool = True,
        max_nodes: Optional[int] = None,
    ) -> None:
        problem.validate()
        self.problem = problem
        self.use_symmetry = use_symmetry
        self.max_nodes = max_nodes

        self._vertices = sorted(problem.graph.vertices)
        self._color_order = problem.color_order()
        self._adj = problem.graph.adjacency()
        self._sym_classes = color_symmetry_classes(problem) if use_symmetry else []

        self._domains: dict[str, set[str]] = {
            v: set(problem.colors) for v in self._vertices
        }
        self._reasons: dict[str, dict[str, Reason]] = {v: {} for v in self._vertices}
        self._remaining: dict[str, int] = dict(problem.capacities)
        self._assignment: dict[str, str] = {}
        self._trail: list[tuple] = []
        self._assumption_marks: list[tuple[int, Optional[str]]] = []
        self._pinned: dict[str, int] = {}  # colors pinned by negative assumptions
        self._trace: list[TraceEvent] = []
        self._nodes = 0
        self._init_conflict: Optional[ConflictInfo] = None

        self._setup()

    # ---------------------------------------------------------- tracing

    def _log(self, kind: str, detail: str) -> None:
        self._trace.append(TraceEvent(len(self._trace), kind, detail))

    # ---------------------------------------------------------- trail

    def _mark(self) -> int:
        return len(self._trail)

    def _undo(self, mark: int) -> None:
        while len(self._trail) > mark:
            entry = self._trail.pop()
            kind = entry[0]
            if kind == "rm":
                _, v, c = entry
                self._domains[v].add(c)
                del self._reasons[v][c]
            elif kind == "assign":
                _, v = entry
                del self._assignment[v]
            elif kind == "cap":
                _, c = entry
                self._remaining[c] += 1

    # ---------------------------------------------------------- propagation

    def _remove_color(self, v: str, c: str, reason: Reason) -> bool:
        """Remove c from domain[v], recording its reason. True if removed now."""
        if c not in self._domains[v]:
            return False
        self._domains[v].discard(c)
        self._reasons[v][c] = reason
        self._trail.append(("rm", v, c))
        self._log("remove", f"{v}!={c} <- {reason_str(reason)}")
        return True

    def _assign(self, v: str, c: str, reason: Reason) -> Optional[ConflictInfo]:
        """Assign v=c and propagate. Returns a conflict or None."""
        if v in self._assignment:
            if self._assignment[v] == c:
                return None
            return ConflictInfo(v, dict(self._reasons[v]),
                                f"{v} already assigned {self._assignment[v]}")
        if c not in self._domains[v]:
            return ConflictInfo(v, dict(self._reasons[v]),
                                f"{c} not in domain of {v}")
        self._assignment[v] = c
        self._trail.append(("assign", v))
        self._log("assign", f"{v}={c} <- {reason_str(reason)}")
        for other in list(self._domains[v]):
            if other != c:
                self._remove_color(v, other, reason)

        if c in self._remaining:
            self._remaining[c] -= 1
            self._trail.append(("cap", c))
            if self._remaining[c] == 0:
                for w in self._vertices:
                    if w not in self._assignment:
                        self._remove_color(w, c, CapacityReason(c))

        for eid, w in self._adj[v]:
            if w == v:  # self-loop: vertex adjacent to itself
                self._remove_color(v, c, EdgeReason(eid, v, c))
            elif w not in self._assignment:
                self._remove_color(w, c, EdgeReason(eid, v, c))

        for w in self._vertices:
            if not self._domains[w]:
                return ConflictInfo(w, dict(self._reasons[w]))
        return None

    def _setup(self) -> None:
        p = self.problem
        # Directly locate edges contradicted by the precoloring.
        for e in p.graph.edges:
            cu, cv = p.precoloring.get(e.u), p.precoloring.get(e.v)
            if cu is not None and cu == cv:
                self._init_conflict = ConflictInfo(
                    e.u, {},
                    f"edge {e.id}: {e.u} and {e.v} both precolored {cu}")
                self._log("conflict", self._init_conflict.note)
                return
            if e.u == e.v:
                self._init_conflict = ConflictInfo(
                    e.u, {}, f"edge {e.id}: self-loop on {e.u}")
                self._log("conflict", self._init_conflict.note)
                return
        for c, k in self._remaining.items():
            if k == 0:
                for v in self._vertices:
                    self._remove_color(v, c, CapacityReason(c))
        for v in self._vertices:
            if v in p.precoloring:
                conflict = self._assign(v, p.precoloring[v], PrecolorReason(v))
                if conflict is not None:
                    self._init_conflict = conflict
                    self._log("conflict", f"domain of {conflict.vertex} emptied")
                    return
        for v in self._vertices:
            if not self._domains[v]:
                self._init_conflict = ConflictInfo(v, dict(self._reasons[v]))
                self._log("conflict", f"domain of {v} emptied")
                return

    # ---------------------------------------------------------- search

    def _candidates(self, v: str) -> list[str]:
        dom = [c for c in self.problem.colors if c in self._domains[v]]
        classes = self._effective_classes()
        if not classes:
            return dom
        used = set(self._assignment.values())
        allowed_new = set()
        for cls in classes:
            for c in cls:
                if c not in used:
                    allowed_new.add(c)  # smallest unused color of the class
                    break
        members = set().union(*classes)
        return [c for c in dom
                if c in used or c in allowed_new or c not in members]

    def _effective_classes(self) -> list[tuple[str, ...]]:
        """Symmetry classes minus colors pinned by negative assumptions."""
        if not self._pinned:
            return list(self._sym_classes)
        out = []
        for cls in self._sym_classes:
            rest = tuple(c for c in cls if c not in self._pinned)
            if len(rest) > 1:
                out.append(rest)
        return out

    def _dfs(self, solutions: list[dict[str, str]], limit: Optional[int]) -> None:
        self._nodes += 1
        if self.max_nodes is not None and self._nodes > self.max_nodes:
            raise _BudgetExhausted
        v = next((x for x in self._vertices if x not in self._assignment), None)
        if v is None:
            sol = dict(self._assignment)
            solutions.append(sol)
            self._log("solution", " ".join(f"{x}={sol[x]}" for x in self._vertices))
            return
        for c in self._candidates(v):
            mark = self._mark()
            self._log("decide", f"{v}={c}")
            conflict = self._assign(v, c, DecisionReason(v, c))
            if conflict is None:
                self._dfs(solutions, limit)
            else:
                self._log("conflict", f"domain of {conflict.vertex} emptied")
            self._undo(mark)
            self._log("backtrack", f"retract {v}={c}")
            if limit is not None and len(solutions) >= limit:
                return

    def _run(self, limit: Optional[int]) -> SolveResult:
        result = SolveResult(Status.UNSAT, trace=self._trace)
        if self._init_conflict is not None:
            result.conflict = self._init_conflict
            result.nodes = self._nodes
            return result
        self._nodes = 0
        solutions: list[dict[str, str]] = []
        try:
            self._dfs(solutions, limit)
        except _BudgetExhausted:
            result.status = Status.BUDGET_EXHAUSTED
            result.solutions = solutions
            result.coloring = solutions[0] if solutions else None
            result.nodes = self._nodes
            return result
        result.nodes = self._nodes
        result.solutions = solutions
        if solutions:
            result.status = Status.SAT
            result.coloring = solutions[0]
        return result

    def solve(self) -> SolveResult:
        """First (lexicographically smallest) solution, or UNSAT / budget."""
        return self._run(limit=1)

    def find_all(self, limit: Optional[int] = None) -> SolveResult:
        """All solutions reachable under the current pruning settings."""
        return self._run(limit=limit)

    # ---------------------------------------------------------- assumptions

    @property
    def assumption_level(self) -> int:
        return len(self._assumption_marks)

    def push_assumption(self, vertex: str, color: str, sign: bool = True) -> bool:
        """Assume vertex=color (sign=True) or vertex!=color. False on conflict;
        a conflicting assumption is rolled back immediately."""
        if vertex not in self._domains:
            raise KeyError(f"unknown vertex: {vertex}")
        if color not in self.problem.color_order():
            raise KeyError(f"unknown color: {color}")
        mark = self._mark()
        reason = AssumptionReason(vertex, color, sign)
        pinned = None
        if sign:
            conflict = self._assign(vertex, color, reason)
        else:
            # v!=color distinguishes the color from its symmetry class.
            pinned = color
            self._pinned[color] = self._pinned.get(color, 0) + 1
            self._remove_color(vertex, color, reason)
            conflict = None
            for w in self._vertices:
                if not self._domains[w]:
                    conflict = ConflictInfo(w, dict(self._reasons[w]))
                    break
        if conflict is not None:
            if pinned is not None:
                self._pinned[pinned] -= 1
                if self._pinned[pinned] == 0:
                    del self._pinned[pinned]
            self._undo(mark)
            self._log("conflict", f"assumption {vertex}{'=' if sign else '!='}{color} infeasible")
            return False
        self._assumption_marks.append((mark, pinned))
        self._log("assume", f"{vertex}{'=' if sign else '!='}{color}")
        return True

    def pop_assumption(self) -> None:
        if not self._assumption_marks:
            raise IndexError("no assumption to retract")
        mark, pinned = self._assumption_marks.pop()
        if pinned is not None:
            self._pinned[pinned] -= 1
            if self._pinned[pinned] == 0:
                del self._pinned[pinned]
        self._undo(mark)
        self._log("retract", f"level {len(self._assumption_marks)}")

    # ---------------------------------------------------------- introspection

    def check_invariants(self) -> None:
        """Raise AssertionError if domains/reasons/capacities are inconsistent."""
        for v in self._vertices:
            dom, reasons = self._domains[v], self._reasons[v]
            assert not (dom & reasons.keys()), f"{v}: color both present and removed"
            assert dom | reasons.keys() == set(self.problem.colors), (
                f"{v}: domain + reasons do not cover all colors")
        used = [c for c in self._assignment.values()]
        for c, k in self.problem.capacities.items():
            assert self._remaining[c] == k - used.count(c), (
                f"capacity of {c} out of sync")
        for v, c in self._assignment.items():
            assert self._domains[v] == {c}, f"{v}: assigned but domain not singleton"
