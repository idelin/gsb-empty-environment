"""Retractable min-max gap network (a difference-constraints system).

Each constraint has the form::

    lower <= time(target) - time(source) <= upper

with exact rational bounds (``fractions.Fraction``).  Absolute time ranges
are expressed as constraints against a distinguished anchor variable, which
also fixes the uniform-translation degree of freedom of the whole system.

Features:

- push/pop of assumption layers; popping recomputes bounds from scratch so
  any bound that lost its support is relaxed again;
- incremental propagation when constraints are added (SPFA seeded at the
  new edges), full Bellman-Ford after a pop;
- history of versions with per-version bound snapshots;
- negative-cycle (contradiction) evidence reported as original constraint
  IDs;
- a relaxation budget: when the budget is exhausted the status becomes
  UNKNOWN and no (pseudo-)feasible schedule is ever returned;
- unconstrained events are reported separately with their (infinite) range.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from fractions import Fraction
from typing import Dict, List, Optional, Tuple

from .exact_time import Duration

ANCHOR = "@anchor"

FEASIBLE = "FEASIBLE"
INFEASIBLE = "INFEASIBLE"
UNKNOWN = "UNKNOWN"


class BudgetExceeded(Exception):
    """Internal: the relaxation budget was exhausted mid-propagation."""


class UnknownError(Exception):
    """Status is UNKNOWN (budget exhausted); no trustworthy answer exists."""


class InfeasibleError(Exception):
    """The network is infeasible; no schedule exists."""


@dataclass(frozen=True)
class Constraint:
    """lower <= time(target) - time(source) <= upper (None = unbounded)."""

    cid: str
    source: str
    target: str
    lower: Optional[Fraction]
    upper: Optional[Fraction]
    layer: int


@dataclass(frozen=True)
class _Edge:
    u: str
    v: str
    w: Fraction
    cid: str


@dataclass(frozen=True)
class HistoryEntry:
    version: int
    op: str
    status: str
    bounds: Optional[Dict[str, Tuple[Optional[Fraction], Optional[Fraction]]]]


def _edges_of(c: Constraint) -> List[_Edge]:
    out = []
    if c.upper is not None:
        out.append(_Edge(c.source, c.target, c.upper, c.cid))
    if c.lower is not None:
        out.append(_Edge(c.target, c.source, -c.lower, c.cid))
    return out


def _frac(value):
    if value is None:
        return None
    if isinstance(value, Duration):
        return value.as_fraction
    return Fraction(value)


class TemporalNetwork:
    """A retractable min-max gap network with exact rational arithmetic."""

    def __init__(self, budget: int = 1_000_000):
        self.budget = budget
        self._events: List[str] = []
        self._event_set = set()
        self.constraints: Dict[str, Constraint] = {}
        self._layers: List[List[str]] = [[]]
        self._edges: List[_Edge] = []
        self._edges_t: List[_Edge] = []
        self._adj: Dict[str, List[_Edge]] = {}
        self._adj_t: Dict[str, List[_Edge]] = {}
        self._pot: Dict[str, Fraction] = {ANCHOR: Fraction(0)}
        self._latest: Dict[str, Fraction] = {ANCHOR: Fraction(0)}
        self._to_anchor: Dict[str, Fraction] = {ANCHOR: Fraction(0)}
        self.status = FEASIBLE
        self._cycle: Optional[Tuple[List[str], List[str], Fraction]] = None
        self.version = 0
        self.history: List[HistoryEntry] = []
        self._counter = 0
        self._ops = 0
        self._pending: List[_Edge] = []

    # ------------------------------------------------------------------ #
    # construction
    # ------------------------------------------------------------------ #

    def add_event(self, name: str, lower=None, upper=None, cid: Optional[str] = None) -> None:
        """Register an event; optionally give it an absolute time range."""
        if name == ANCHOR:
            raise ValueError("event name reserved")
        if name not in self._event_set:
            self._event_set.add(name)
            self._events.append(name)
            # super-source potential: every vertex starts at 0
            self._pot.setdefault(name, Fraction(0))
        if lower is not None or upper is not None:
            self.set_range(name, lower, upper, cid=cid)

    def set_range(self, event: str, lower=None, upper=None, cid: Optional[str] = None) -> str:
        """Constrain an event to an absolute range [lower, upper]."""
        return self.add_constraint(ANCHOR, event, lower=lower, upper=upper, cid=cid)

    def add_constraint(self, source: str, target: str, lower=None, upper=None,
                       cid: Optional[str] = None) -> str:
        """Add ``lower <= time(target) - time(source) <= upper``."""
        lo, hi = _frac(lower), _frac(upper)
        if lo is None and hi is None:
            raise ValueError("constraint needs at least one bound")
        if lo is not None and hi is not None and lo > hi:
            # still added: the propagation below will prove infeasibility
            pass
        for ev in (source, target):
            if ev != ANCHOR:
                self.add_event(ev)
        self._counter += 1
        cid = cid or f"C{self._counter}"
        if cid in self.constraints:
            raise ValueError(f"duplicate constraint id {cid!r}")
        c = Constraint(cid, source, target, lo, hi, layer=len(self._layers) - 1)
        self.constraints[cid] = c
        self._layers[-1].append(cid)
        new_edges = _edges_of(c)
        self._install_edges(new_edges)
        self._pending = new_edges
        self._after_mutation(f"add {cid}")
        return cid

    def push(self) -> None:
        """Open a new assumption layer."""
        self._layers.append([])
        self._record("push")

    def pop(self) -> List[str]:
        """Retract the top assumption layer and relax unsupported bounds."""
        if len(self._layers) == 1:
            raise ValueError("cannot pop the base layer")
        removed = self._layers.pop()
        for cid in removed:
            self.constraints.pop(cid, None)
        self._rebuild_edges()
        self._ops = 0
        try:
            self._cycle = None
            ok = self._recompute_full()
            self.status = FEASIBLE if ok else INFEASIBLE
        except BudgetExceeded:
            self.status = UNKNOWN
            self._cycle = None
        self._record(f"pop [{', '.join(removed)}]")
        return removed

    # ------------------------------------------------------------------ #
    # queries
    # ------------------------------------------------------------------ #

    def bounds(self, event: str) -> Tuple[Optional[Fraction], Optional[Fraction]]:
        """(earliest, latest) feasible time relative to the anchor.

        ``None`` means unbounded on that side.  Raises if the network is
        infeasible or the budget ran out (no pseudo-answers).
        """
        self._require_known()
        latest = self._latest.get(event)          # None => +inf
        to_a = self._to_anchor.get(event)         # None => -inf
        earliest = -to_a if to_a is not None else None
        return (earliest, latest)

    def bounds_all(self):
        """(constrained_bounds, unconstrained_events) pair of dicts."""
        unconstrained = self.unconstrained_events()
        constrained = {e: self.bounds(e) for e in self._events if e not in unconstrained}
        return constrained, unconstrained

    def unconstrained_events(self) -> Dict[str, Tuple[None, None]]:
        """Events with no incident constraint, each with range (-inf, +inf)."""
        used = set()
        for c in self.constraints.values():
            used.add(c.source)
            used.add(c.target)
        used.discard(ANCHOR)
        return {e: (None, None) for e in self._events if e not in used}

    def schedule(self) -> Dict[str, Fraction]:
        """A concrete feasible assignment (witness). Never pseudo-feasible."""
        self._require_known()
        return {e: self._pot[e] for e in self._events}

    def contradiction(self) -> Optional[List[str]]:
        """Constraint IDs forming the negative cycle, or None."""
        if self._cycle is None:
            return None
        return list(self._cycle[1])

    def contradiction_detail(self):
        """(cycle nodes, constraint IDs, total weight) of the negative cycle."""
        return self._cycle

    def bounds_at(self, version: int):
        """Bound snapshot recorded at a history version."""
        for entry in self.history:
            if entry.version == version:
                return entry.bounds
        raise KeyError(f"no history entry for version {version}")

    # ------------------------------------------------------------------ #
    # internals
    # ------------------------------------------------------------------ #

    def _require_known(self):
        if self.status == UNKNOWN:
            raise UnknownError("budget exhausted; result unknown, not feasible")
        if self.status == INFEASIBLE:
            raise InfeasibleError("network is infeasible; see contradiction()")

    @property
    def _nvertices(self) -> int:
        return 1 + len(self._events)

    def _tick(self):
        self._ops += 1
        if self._ops > self.budget:
            raise BudgetExceeded()

    def _install_edges(self, edges: List[_Edge]) -> None:
        for e in edges:
            self._edges.append(e)
            self._adj.setdefault(e.u, []).append(e)
            t = _Edge(e.v, e.u, e.w, e.cid)
            self._edges_t.append(t)
            self._adj_t.setdefault(t.u, []).append(t)

    def _rebuild_edges(self) -> None:
        self._edges, self._edges_t = [], []
        self._adj, self._adj_t = {}, {}
        for c in self.constraints.values():
            self._install_edges(_edges_of(c))

    def _after_mutation(self, op: str) -> None:
        self._ops = 0
        try:
            if self.status == INFEASIBLE:
                pass  # adding constraints cannot heal infeasibility
            elif self.status == UNKNOWN:
                self._cycle = None
                ok = self._recompute_full()
                self.status = FEASIBLE if ok else INFEASIBLE
            else:
                ok = self._incremental()
                self.status = FEASIBLE if ok else INFEASIBLE
        except BudgetExceeded:
            self.status = UNKNOWN
            self._cycle = None
        self._record(op)

    def _record(self, op: str) -> None:
        self.version += 1
        snapshot = None
        if self.status == FEASIBLE:
            snapshot = {e: self.bounds(e) for e in self._events}
        self.history.append(HistoryEntry(self.version, op, self.status, snapshot))

    def _incremental(self) -> bool:
        new = self._pending
        if not self._propagate(self._pot, self._adj, [e.u for e in new]):
            return False
        if not self._propagate(self._latest, self._adj, [e.u for e in new]):
            return False
        return self._propagate(self._to_anchor, self._adj_t, [e.v for e in new])

    def _propagate(self, dist, adj, seeds) -> bool:
        """Queue-based Bellman-Ford seeded at changed vertices."""
        n = self._nvertices
        parent: Dict[str, Tuple[str, Fraction, str]] = {}
        depth: Dict[str, int] = {}
        queue = deque(s for s in seeds if dist.get(s) is not None)
        in_queue = set(queue)
        while queue:
            u = queue.popleft()
            in_queue.discard(u)
            du = dist[u]
            for e in adj.get(u, ()):
                self._tick()
                candidate = du + e.w
                dv = dist.get(e.v)
                if dv is None or candidate < dv:
                    dist[e.v] = candidate
                    parent[e.v] = (u, e.w, e.cid)
                    depth[e.v] = depth.get(u, 0) + 1
                    if depth[e.v] >= n:
                        # Suspect a negative cycle; arbitrate with a full
                        # round-based Bellman-Ford (textbook-correct).
                        return self._check_feasibility_full()
                    if e.v not in in_queue:
                        queue.append(e.v)
                        in_queue.add(e.v)
        return True

    def _check_feasibility_full(self) -> bool:
        vertices = [ANCHOR, *self._events]
        n = len(vertices)
        pot = {v: Fraction(0) for v in vertices}
        parent: Dict[str, Tuple[str, Fraction, str]] = {}
        for i in range(n):
            changed = False
            for e in self._edges:
                self._tick()
                if pot[e.v] > pot[e.u] + e.w:
                    pot[e.v] = pot[e.u] + e.w
                    parent[e.v] = (e.u, e.w, e.cid)
                    changed = True
                    if i == n - 1:
                        self._cycle = self._extract_cycle(parent, e.v)
                        self._pot = pot
                        return False
            if not changed:
                break
        self._pot = pot
        return True

    def _recompute_full(self) -> bool:
        if not self._check_feasibility_full():
            return False
        self._latest = self._full_shortest(self._edges)
        self._to_anchor = self._full_shortest(self._edges_t)
        return True

    def _full_shortest(self, edges) -> Dict[str, Fraction]:
        dist: Dict[str, Fraction] = {ANCHOR: Fraction(0)}
        for _ in range(self._nvertices - 1):
            changed = False
            for e in edges:
                self._tick()
                du = dist.get(e.u)
                if du is None:
                    continue
                cand = du + e.w
                dv = dist.get(e.v)
                if dv is None or cand < dv:
                    dist[e.v] = cand
                    changed = True
            if not changed:
                break
        return dist

    def _extract_cycle(self, parent, start):
        n = self._nvertices
        x = start
        for _ in range(n):
            nxt = parent.get(x)
            if nxt is None:
                break
            x = nxt[0]
        seen: Dict[str, int] = {}
        cyc: List[str] = []
        cur = x
        while cur not in seen and cur in parent:
            seen[cur] = len(cyc)
            cyc.append(cur)
            cur = parent[cur][0]
        nodes = cyc[seen[cur]:] if cur in seen else cyc
        cids = [parent[v][2] for v in nodes]
        weight = sum((parent[v][1] for v in nodes), Fraction(0))
        return (nodes, cids, weight)
