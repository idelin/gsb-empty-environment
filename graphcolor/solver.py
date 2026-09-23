"""Constraint solver for finite-domain vertex coloring.

Features:
- precoloring and per-color capacity limits
- three-valued result: feasible / infeasible (proven) / budget exhausted
- reason graph: every color removal records why it happened
- trail-based backtracking that restores domains, capacities and reasons
- retractable assumptions (push/pop)
- optional color-symmetry breaking (valid when capacities are uniform)
- reproducible search trace (deterministic vertex/color ordering)
- lexicographically minimal feasible coloring via iterative assumptions
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass, field

FEASIBLE = "feasible"
INFEASIBLE = "infeasible"
BUDGET_EXHAUSTED = "budget_exhausted"


@dataclass(frozen=True)
class Reason:
    """Why a color left a domain, or why a vertex was assigned."""

    kind: str  # edge | capacity | precolor | assumption | decision | singleton
    edge_id: str | None = None
    color: int | None = None
    vertex: str | None = None

    def describe(self) -> str:
        if self.kind == "edge":
            return f"edge {self.edge_id}: neighbor {self.vertex} uses this color"
        if self.kind == "capacity":
            return f"capacity of color {self.color} is exhausted"
        if self.kind == "precolor":
            return f"vertex {self.vertex} is precolored"
        if self.kind == "assumption":
            return f"assumption on vertex {self.vertex}"
        if self.kind == "decision":
            return f"search decision on vertex {self.vertex}"
        if self.kind == "singleton":
            return f"domain of vertex {self.vertex} became a singleton"
        return self.kind


@dataclass
class Conflict:
    """A vertex whose domain went empty (or whose assignment was blocked)."""

    vertex: str
    color_reasons: dict[int, Reason] = field(default_factory=dict)

    def edge_ids(self) -> list[str]:
        return sorted(
            {r.edge_id for r in self.color_reasons.values() if r.edge_id is not None}
        )

    def describe(self) -> str:
        parts = [
            f"color {c}: {r.describe()}" for c, r in sorted(self.color_reasons.items())
        ]
        return f"conflict at {self.vertex}: " + "; ".join(parts)


class _Conflict(Exception):
    def __init__(self, conflict: Conflict) -> None:
        super().__init__(conflict.describe())
        self.conflict = conflict


class _BudgetExhausted(Exception):
    pass


@dataclass
class SolveResult:
    status: str  # FEASIBLE | INFEASIBLE | BUDGET_EXHAUSTED
    coloring: dict[str, int] | None
    trace: list[str]
    nodes: int
    conflict: Conflict | None = None

    @property
    def ok(self) -> bool:
        return self.status == FEASIBLE


class Solver:
    def __init__(
        self,
        graph,
        num_colors: int,
        capacities: dict[int, int] | None = None,
        precoloring: dict[str, int] | None = None,
        symmetry_breaking: bool = False,
        budget: int = 1_000_000,
        trace: bool = True,
    ) -> None:
        if num_colors < 1:
            raise ValueError("num_colors must be >= 1")
        self.graph = graph
        self.num_colors = num_colors
        self.capacities = dict(capacities or {})
        self.precoloring = dict(precoloring or {})
        known = set(graph.vertices)
        for v, c in self.precoloring.items():
            if v not in known:
                raise ValueError(f"precolored vertex {v!r} not in graph")
            if not 0 <= c < num_colors:
                raise ValueError(f"precolor {c} of {v!r} outside [0, {num_colors})")
        for c, cap in self.capacities.items():
            if not 0 <= c < num_colors:
                raise ValueError(f"capacity color {c} outside [0, {num_colors})")
            if cap < 0:
                raise ValueError("capacities must be >= 0")
        self.symmetry_breaking = symmetry_breaking
        self.budget = budget
        self.trace_enabled = trace

        self.vertices = sorted(graph.vertices)
        self.domains: dict[str, set[int]] = {
            v: set(range(num_colors)) for v in self.vertices
        }
        self.assignment: dict[str, int] = {}
        self.used: dict[int, int] = {c: 0 for c in range(num_colors)}
        self.reasons: dict[tuple[str, int], Reason] = {}
        self.trail: list[tuple] = []
        self.nodes = 0
        self.trace: list[str] = []
        self.last_conflict: Conflict | None = None

        self._queue: deque[str] = deque()
        self._initialized = False
        self._base_mark = 0
        self._assumption_marks: list[int] = []

    # ------------------------------------------------------------------ trace
    def _log(self, message: str) -> None:
        if self.trace_enabled:
            self.trace.append(message)

    # ------------------------------------------------------- state mutation
    def _remove_color(self, v: str, c: int, reason: Reason) -> None:
        if c not in self.domains[v]:
            return
        self.domains[v].discard(c)
        self.reasons[(v, c)] = reason
        self.trail.append(("domain", v, c))
        self._log(f"remove color {c} from {v}: {reason.describe()}")
        if not self.domains[v]:
            raise _Conflict(
                Conflict(v, {k: self.reasons[(v, k)] for k in range(self.num_colors)})
            )

    def _assign(self, v: str, c: int, why: Reason) -> None:
        if v in self.assignment:
            if self.assignment[v] == c:
                return
            raise _Conflict(Conflict(v, {c: why}))
        if c not in self.domains[v]:
            reason = self.reasons.get((v, c), Reason("unknown"))
            raise _Conflict(Conflict(v, {c: reason}))
        self.assignment[v] = c
        self.used[c] += 1
        self.trail.append(("assign", v, c))
        self._queue.append(v)
        self._log(f"assign {v} = {c} ({why.kind})")

    def _propagate(self) -> None:
        while self._queue:
            v = self._queue.popleft()
            c = self.assignment[v]
            for e in self.graph.incident(v):
                u = e.other(v)
                if u == v:
                    raise _Conflict(
                        Conflict(v, {c: Reason("edge", edge_id=e.id, vertex=v)})
                    )
                if u in self.assignment:
                    if self.assignment[u] == c:
                        raise _Conflict(
                            Conflict(u, {c: Reason("edge", edge_id=e.id, vertex=v)})
                        )
                    continue
                self._remove_color(u, c, Reason("edge", edge_id=e.id, vertex=v))
            cap = self.capacities.get(c)
            if cap is not None and self.used[c] >= cap:
                for u in self.vertices:
                    if u not in self.assignment:
                        self._remove_color(u, c, Reason("capacity", color=c))
            for u in self.vertices:
                if u not in self.assignment and len(self.domains[u]) == 1:
                    only = next(iter(self.domains[u]))
                    self._assign(u, only, Reason("singleton", vertex=u))

    def _undo_to(self, mark: int) -> None:
        while len(self.trail) > mark:
            entry = self.trail.pop()
            if entry[0] == "domain":
                _, v, c = entry
                self.domains[v].add(c)
                del self.reasons[(v, c)]
            else:
                _, v, c = entry
                del self.assignment[v]
                self.used[c] -= 1
        self._queue.clear()

    # ------------------------------------------------------- initialization
    def initialize(self) -> None:
        """Apply level-0 constraints: zero capacities and precoloring."""
        if self._initialized:
            return
        for c in range(self.num_colors):
            if self.capacities.get(c) == 0:
                for v in self.vertices:
                    self._remove_color(v, c, Reason("capacity", color=c))
        for v in sorted(self.precoloring):
            self._assign(v, self.precoloring[v], Reason("precolor", vertex=v))
        self._propagate()
        self._initialized = True
        self._base_mark = len(self.trail)

    # ------------------------------------------------------------- assumptions
    def push_assumption(self, vertex: str, color: int) -> None:
        """Add a retractable assumption. Raises _Conflict-derived info via
        ConflictError-free path: on immediate conflict the state is restored
        and the exception propagates."""
        self.initialize()
        mark = len(self.trail)
        try:
            self._assign(vertex, color, Reason("assumption", vertex=vertex))
            self._propagate()
        except _Conflict:
            self._undo_to(mark)
            raise
        self._assumption_marks.append(mark)
        self._base_mark = len(self.trail)

    def pop_assumption(self) -> None:
        if not self._assumption_marks:
            raise RuntimeError("no assumption to pop")
        mark = self._assumption_marks.pop()
        self._undo_to(mark)
        self._base_mark = len(self.trail)

    # ---------------------------------------------------------------- search
    def _branch_colors(self, v: str) -> list[int]:
        colors = sorted(self.domains[v])
        if not self.symmetry_breaking:
            return colors
        used_colors = {c for c in range(self.num_colors) if self.used[c] > 0}
        unused = [c for c in range(self.num_colors) if self.used[c] == 0]
        next_new = min(unused) if unused else None
        return [c for c in colors if c in used_colors or c == next_new]

    def _pick_vertex(self) -> str | None:
        for v in self.vertices:
            if v not in self.assignment:
                return v
        return None

    def _search(self, depth: int = 0) -> dict[str, int] | None:
        self.nodes += 1
        if self.nodes > self.budget:
            raise _BudgetExhausted
        target = self._pick_vertex()
        if target is None:
            return dict(self.assignment)
        mark = len(self.trail)
        for c in self._branch_colors(target):
            self._log(f"decide depth={depth} {target} <- color {c}")
            try:
                self._assign(target, c, Reason("decision", vertex=target))
                self._propagate()
            except _Conflict as con:
                self.last_conflict = con.conflict
                self._log(con.conflict.describe())
                self._undo_to(mark)
                self._log(f"backtrack depth={depth}")
                continue
            result = self._search(depth + 1)
            if result is not None:
                return result
            self._undo_to(mark)
            self._log(f"backtrack depth={depth}")
        return None

    def solve(self) -> SolveResult:
        self.nodes = 0
        try:
            self.initialize()
            coloring = self._search()
        except _Conflict as con:
            self.last_conflict = con.conflict
            self._undo_to(self._base_mark)
            return SolveResult(INFEASIBLE, None, list(self.trace), self.nodes,
                               con.conflict)
        except _BudgetExhausted:
            self._undo_to(self._base_mark)
            return SolveResult(BUDGET_EXHAUSTED, None, list(self.trace), self.nodes,
                               self.last_conflict)
        self._undo_to(self._base_mark)
        if coloring is None:
            return SolveResult(INFEASIBLE, None, list(self.trace), self.nodes,
                               self.last_conflict)
        return SolveResult(FEASIBLE, coloring, list(self.trace), self.nodes)

    def enumerate_solutions(self, limit: int = 100_000):
        """All feasible colorings (canonical representatives only when
        symmetry_breaking is on). Returns (solutions, truncated)."""
        self.initialize()
        self.nodes = 0
        solutions: list[dict[str, int]] = []
        truncated = False

        def rec() -> None:
            nonlocal truncated
            if len(solutions) >= limit:
                truncated = True
                return
            self.nodes += 1
            if self.nodes > self.budget:
                raise _BudgetExhausted
            target = self._pick_vertex()
            if target is None:
                solutions.append(dict(self.assignment))
                return
            mark = len(self.trail)
            for c in self._branch_colors(target):
                try:
                    self._assign(target, c, Reason("decision", vertex=target))
                    self._propagate()
                except _Conflict:
                    self._undo_to(mark)
                    continue
                rec()
                self._undo_to(mark)
                if truncated:
                    return

        try:
            rec()
        except _BudgetExhausted:
            truncated = True
        finally:
            self._undo_to(self._base_mark)
        return solutions, truncated


def solve_lex_min(
    graph,
    num_colors: int,
    capacities: dict[int, int] | None = None,
    precoloring: dict[str, int] | None = None,
    budget: int = 1_000_000,
    vertex_order: list[str] | None = None,
    trace: bool = False,
) -> SolveResult:
    """Lexicographically minimal feasible coloring over `vertex_order`.

    Fixes each vertex in turn to the smallest color that still admits a
    feasible completion, using retractable assumptions. If any feasibility
    check runs out of budget the whole result is BUDGET_EXHAUSTED and no
    minimality is claimed.
    """
    order = list(vertex_order) if vertex_order else sorted(graph.vertices)
    solver = Solver(graph, num_colors, capacities=capacities,
                    precoloring=precoloring, budget=budget, trace=trace)
    try:
        solver.initialize()
    except _Conflict as con:
        return SolveResult(INFEASIBLE, None, list(solver.trace), solver.nodes,
                           con.conflict)
    for v in order:
        if v in solver.assignment:
            continue
        placed = False
        for c in sorted(solver.domains[v]):
            try:
                solver.push_assumption(v, c)
            except _Conflict:
                continue
            res = solver.solve()
            if res.status == FEASIBLE:
                placed = True
                break
            solver.pop_assumption()
            if res.status == BUDGET_EXHAUSTED:
                return SolveResult(BUDGET_EXHAUSTED, None, list(solver.trace),
                                   solver.nodes)
        if not placed:
            return SolveResult(INFEASIBLE, None, list(solver.trace), solver.nodes,
                               solver.last_conflict)
    coloring = {v: solver.assignment[v] for v in order}
    return SolveResult(FEASIBLE, coloring, list(solver.trace), solver.nodes)
