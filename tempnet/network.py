"""Retractable min/max difference-constraint network.

Model
-----
Every event ``v`` has an integer time ``t(v)``.  Each constraint becomes one
or two difference inequalities ``t(v) - t(u) <= w``, i.e. a weighted edge
``u -> v`` of weight ``w`` in a constraint graph.

* interval ``lo <= b - a <= hi``   -> ``a -> b`` weight ``hi``
                                     and ``b -> a`` weight ``-lo``
* one-sided ``b - a >= lo``        -> ``b -> a`` weight ``-lo``
  (``hi`` may be ``None``: no upper bound)
* one-sided ``b - a <= hi``        -> ``a -> b`` weight ``hi``
* absolute window ``lo <= a <= hi``-> same edges against the anchor node Z
  with ``t(Z) = 0``

Feasibility, earliest/latest bounds and solutions come from the all-pairs
shortest-path (difference-constraints) closure:

* a feasible assignment exists iff the graph has no negative cycle;
* with an anchor ``a`` of known value ``t(a)``, the tight earliest/latest
  window of every reachable event ``v`` is
  ``t(a) - D[v][a] <= t(v) <= t(a) + D[a][v]``.

All weights are ints.  Shortest paths use plain integer comparison; there is
no epsilon, so a one-tick contradiction is detected exactly.

Incremental propagation and retraction
--------------------------------------
New edges are propagated into the maintained closure by dynamic-APSP edge
insertion (each improved path relaxes every other row/column).  Relaxations
are counted against an integer ``budget``: if the budget is exhausted the
network is rolled back to its previous stable state and ``BudgetExceeded`` is
raised -- a budget interruption never leaves a half-propagated (and therefore
potentially pseudo-feasible) network.

Constraints pushed inside a ``push()`` level are discarded by ``pop()``;
retraction recomputes the closure *from scratch* using only the surviving
constraints, which necessarily loosens every bound that had relied on a
removed constraint.  Named immutable versions support ``save_version`` /
``restore_version``.
"""

from __future__ import annotations

from collections import deque
from dataclasses import dataclass
from typing import Dict, Iterable, List, Optional, Sequence, Tuple

__all__ = [
    "Constraint",
    "NegativeCycle",
    "Bounds",
    "MinMaxNetwork",
    "InconsistentNetwork",
    "BudgetExceeded",
    "NoSolution",
]

INF: Optional[int] = None  # unbounded sentinel in returned bounds

_ANCHOR = "Z"  # internal anchor node; user event names must not start with #


class MinMaxNetworkError(Exception):
    """Base class for network errors."""


class InconsistentNetwork(MinMaxNetworkError):
    """The accumulated constraints contain a negative cycle (infeasible)."""

    def __init__(self, cycle: "NegativeCycle"):
        self.cycle = cycle
        super().__init__(f"inconsistent network: {cycle.describe()}")


class BudgetExceeded(MinMaxNetworkError):
    """Propagation used more than the allowed number of relaxations.

    The network is left in its prior stable, feasible state; no partial
    propagation is visible and no pseudo-feasible schedule can be read out.
    """

    def __init__(self, used: int, budget: int):
        self.used = used
        self.budget = budget
        super().__init__(f"relaxation budget {budget} exhausted (used {used})")


class NoSolution(MinMaxNetworkError):
    """No anchored/translated solution could be produced as requested."""


@dataclass(frozen=True)
class Constraint:
    """A user constraint: ``lo <= b - a <= hi`` (int ticks).

    ``a is None`` denotes an absolute window ``lo <= b <= hi`` against the
    anchor ``Z``.  ``lo``/``hi`` may be ``None`` for a one-sided constraint.
    """

    cid: str
    a: Optional[str]
    b: str
    lo: Optional[int]
    hi: Optional[int]
    level: int = 0

    def edges(self) -> Tuple["Edge", ...]:
        out: List[Edge] = []
        if self.hi is not None:  # t(b) - t(a) <= hi
            out.append(Edge(self._u_hi(), self.b, self.hi, self.cid))
        if self.lo is not None:  # t(a) - t(b) <= -lo
            out.append(Edge(self.b, self._u_lo(), -self.lo, self.cid))
        return out

    def _u_hi(self) -> str:
        return self.a if self.a is not None else _ANCHOR

    def _u_lo(self) -> str:
        return self.a if self.a is not None else _ANCHOR

    def describe(self) -> str:
        a = self.a if self.a is not None else "Z"
        lo = "-inf" if self.lo is None else self.lo
        hi = "+inf" if self.hi is None else self.hi
        return f"[{self.cid}] {lo} <= {self.b}-{a} <= {hi}"


@dataclass(frozen=True)
class Edge:
    u: str
    v: str
    w: int
    cid: str


@dataclass(frozen=True)
class NegativeCycle:
    """Evidence of infeasibility, traced back to originating constraint IDs."""

    nodes: Tuple[str, ...]          # nodes along the cycle, first repeated at end
    edges: Tuple[Edge, ...]         # one edge per step
    total: int                      # negative total weight

    @property
    def constraint_ids(self) -> Tuple[str, ...]:
        seen: List[str] = []
        for e in self.edges:
            if e.cid not in seen:
                seen.append(e.cid)
        return tuple(seen)

    def describe(self) -> str:
        chain = " -> ".join(self.nodes)
        ids = ", ".join(self.constraint_ids)
        return f"negative cycle {chain} (sum={self.total}) from constraints: {ids}"


@dataclass(frozen=True)
class Bounds:
    """Earliest/latest feasible window of one event, in int ticks.

    ``earliest`` / ``latest`` are ``None`` when unbounded.  ``fixed`` reports
    an exact value when the two coincide.
    """

    event: str
    earliest: Optional[int]
    latest: Optional[int]

    @property
    def fixed(self) -> Optional[int]:
        if self.earliest is not None and self.earliest == self.latest:
            return self.earliest
        return None

    @property
    def unbounded(self) -> bool:
        return self.earliest is None and self.latest is None

    def contains(self, value: int) -> bool:
        if self.earliest is not None and value < self.earliest:
            return False
        if self.latest is not None and value > self.latest:
            return False
        return True

    def describe(self) -> str:
        lo = "-inf" if self.earliest is None else self.earliest
        hi = "+inf" if self.latest is None else self.latest
        return f"{self.event} in [{lo}, {hi}]"


class MinMaxNetwork:
    """Retractable integer min/max difference-constraint network."""

    def __init__(self, events: Iterable[str] = ()):
        self._nodes: List[str] = []
        self._index: Dict[str, int] = {}
        # D[i][j] is the maintained shortest-path closure (int), None = +inf;
        # D[i][i] = 0.  Always reflects exactly the currently active
        # constraints whenever _infeasible is None.
        self._d: List[List[Optional[int]]] = []
        self._edges: List[Edge] = []
        self._constraints: Dict[str, Constraint] = {}
        self._levels: List[List[str]] = []       # cids per open push level
        self._versions: Dict[str, "_Snapshot"] = {}
        self._infeasible: Optional[NegativeCycle] = None
        for ev in events:
            self.add_event(ev)

    # ------------------------------------------------------------------ #
    # Basic structure
    # ------------------------------------------------------------------ #
    @property
    def events(self) -> Tuple[str, ...]:
        """User events (the internal anchor ``Z`` is excluded)."""
        return tuple(n for n in self._nodes if n != _ANCHOR)

    @property
    def all_nodes(self) -> Tuple[str, ...]:
        return tuple(self._nodes)

    @property
    def constraints(self) -> Tuple[Constraint, ...]:
        return tuple(self._constraints[cid] for cid in self._constraints)

    def constraint(self, cid: str) -> Constraint:
        return self._constraints[cid]

    @property
    def infeasible(self) -> bool:
        return self._infeasible is not None

    def infeasible_cycle(self) -> Optional[NegativeCycle]:
        return self._infeasible

    def add_event(self, name: str) -> None:
        if name == _ANCHOR or name.startswith("#"):
            raise ValueError(f"event name {name!r} is reserved")
        if name in self._index:
            return
        idx = len(self._nodes)
        self._index[name] = idx
        self._nodes.append(name)
        for row in self._d:
            row.append(None)
        self._d.append([None] * (idx + 1))
        self._d[idx][idx] = 0

    def _ensure_anchor(self) -> int:
        if _ANCHOR not in self._index:
            idx = len(self._nodes)
            self._index[_ANCHOR] = idx
            self._nodes.append(_ANCHOR)
            for row in self._d:
                row.append(None)
            self._d.append([None] * (idx + 1))
            self._d[idx][idx] = 0
        return self._index[_ANCHOR]

    # ------------------------------------------------------------------ #
    # Constraint construction
    # ------------------------------------------------------------------ #
    def add_interval(
        self,
        cid: str,
        a: Optional[str],
        b: str,
        lo: Optional[int],
        hi: Optional[int],
        budget: Optional[int] = None,
    ) -> NegativeCycle:
        """Add ``lo <= b - a <= hi``.

        ``a=None`` makes it an absolute window against anchor ``Z``.
        Either bound may be ``None``.  Raises ``InconsistentNetwork`` (the
        network becoming infeasible) or ``BudgetExceeded`` (fully rolled back).
        Returns the found cycle only when the network was already infeasible;
        on a normal feasible insertion returns ``None``.
        """
        if lo is not None and hi is not None and lo > hi:
            raise ValueError(f"empty interval [{lo}, {hi}] for {cid!r}")
        if lo is not None and not isinstance(lo, int):
            raise TypeError("bounds must be ints")
        if hi is not None and not isinstance(hi, int):
            raise TypeError("bounds must be ints")
        if cid in self._constraints:
            raise KeyError(f"constraint id {cid!r} already exists")
        if a is not None:
            self.add_event(a)
        else:
            self._ensure_anchor()
        self.add_event(b)

        con = Constraint(cid, a, b, lo, hi,
                         level=len(self._levels) if self._levels else 0)
        new_edges = con.edges()
        return self._install(con, new_edges, budget)

    def add_min(self, cid: str, a: str, b: str, lo: int,
                budget: Optional[int] = None) -> None:
        """Add ``b - a >= lo`` (minimum gap)."""
        result = self.add_interval(cid, a, b, lo, None, budget)
        assert result is None

    def add_max(self, cid: str, a: str, b: str, hi: int,
                budget: Optional[int] = None) -> None:
        """Add ``b - a <= hi`` (maximum gap)."""
        result = self.add_interval(cid, a, b, None, hi, budget)
        assert result is None

    def add_exact(self, cid: str, a: Optional[str], b: str, value: int,
                  budget: Optional[int] = None) -> None:
        """Add ``b - a == value``; ``a=None`` anchors ``b`` at ``value``."""
        result = self.add_interval(cid, a, b, value, value, budget)
        assert result is None

    def anchor(self, event: Optional[str] = None, value: int = 0,
               cid: str = "#anchor", budget: Optional[int] = None) -> None:
        """Pin an event to an absolute value: ``t(event) = value``.

        With ``event=None`` (default) only the zero anchor ``Z`` is created,
        fixing the translational freedom at ``t(Z) = 0`` without constraining
        any user event.
        """
        self._ensure_anchor()
        if event is None or event == _ANCHOR:
            if value != 0:
                raise ValueError("the zero anchor Z is always 0; anchor an event instead")
            return
        self.add_event(event)
        result = self.add_interval(cid, None, event, value, value, budget)
        assert result is None

    # ------------------------------------------------------------------ #
    # Installation with incremental propagation + atomic budget rollback
    # ------------------------------------------------------------------ #
    def _install(self, con: Constraint, new_edges: Sequence[Edge],
                 budget: Optional[int]) -> Optional[NegativeCycle]:
        if self._infeasible is not None:
            raise InconsistentNetwork(self._infeasible)

        snapshot = [row[:] for row in self._d]
        old_edge_count = len(self._edges)
        self._constraints[con.cid] = con
        self._edges.extend(new_edges)
        if self._levels:
            self._levels[-1].append(con.cid)

        try:
            cycle = self._propagate(new_edges, budget)
        except BudgetExceeded:
            # Full rollback: no partial bounds survive.
            self._d = snapshot
            del self._edges[old_edge_count:]
            self._constraints.pop(con.cid, None)
            if self._levels and self._levels[-1] and self._levels[-1][-1] == con.cid:
                self._levels[-1].pop()
            raise
        if cycle is not None:
            # Contradiction introduced.  Keep the offending constraint and
            # edges so the cycle evidence (constraint IDs) stays valid; the
            # network is now infeasible.  Wrap in push()/pop() if the
            # contradiction should instead be retracted as a rejected
            # hypothesis.
            self._infeasible = cycle
            raise InconsistentNetwork(cycle)
        return None

    def _propagate(self, seed_edges: Sequence[Edge],
                   budget: Optional[int]) -> Optional[NegativeCycle]:
        """Dynamic-APSP insertion of seed edges into ``self._d``.

        Each successful relaxation counts against ``budget``.  On exhaustion
        ``BudgetExceeded`` is raised by the caller after rollback; here we
        raise it directly with ``self._d`` partially updated (restored by the
        caller's snapshot).  Returns a :class:`NegativeCycle` if a negative
        diagonal appears, else ``None``.
        """
        n = len(self._nodes)
        used = 0
        queue: deque = deque()
        pending: Dict[Tuple[int, int], int] = {}
        idx = self._index

        def offer(i: int, j: int, cand: int) -> bool:
            nonlocal used
            old = self._d[i][j]
            if old is not None and old <= cand:
                return False
            key = (i, j)
            if key in pending:
                if cand >= pending[key]:
                    return False
            else:
                queue.append(key)
            pending[key] = cand
            used += 1
            if budget is not None and used > budget:
                raise BudgetExceeded(used, budget)
            return True

        for e in seed_edges:
            offer(idx[e.u], idx[e.v], e.w)

        while queue:
            i, j = queue.popleft()
            cand = pending.pop((i, j))
            old = self._d[i][j]
            if old is not None and old <= cand:
                continue
            self._d[i][j] = cand
            if i == j:
                return self._find_negative_cycle()
            # Relax with the improved path using closure composition.
            # new paths: (i,j) then (j,k); and (k,i) then (i,j).
            for k in range(n):
                djk = self._d[j][k]
                if djk is not None:
                    offer(i, k, cand + djk)
                dki = self._d[k][i]
                if dki is not None:
                    offer(k, j, dki + cand)
        return None

    # ------------------------------------------------------------------ #
    # Negative-cycle witness (Bellman-Ford over original edges)
    # ------------------------------------------------------------------ #
    def _find_negative_cycle(self) -> NegativeCycle:
        """Independently extract a negative cycle with constraint IDs.

        Bellman-Ford is run from a virtual source at distance 0 to every
        node (so every cycle is reachable).  Predecessors from the n-th
        relaxation round reconstruct the cycle, which is then rotated to a
        canonical start.  Edge ties are broken deterministically.
        """
        n = len(self._nodes)
        dist = [0] * n  # virtual source connects to every node with weight 0
        prev: List[Optional[int]] = [None] * n
        prev_edge: List[Optional[Edge]] = [None] * n
        edges = sorted(self._edges, key=lambda e: (e.u, e.v, e.w, e.cid))
        ei = [(self._index[e.u], self._index[e.v], e) for e in edges]

        relaxed_nth: Optional[int] = None
        for _ in range(n):
            relaxed_nth = None
            for u, v, e in ei:
                nd = dist[u] + e.w
                if nd < dist[v]:
                    dist[v] = nd
                    prev[v] = u
                    prev_edge[v] = e
                    relaxed_nth = v
        if relaxed_nth is None:
            # Closure said negative but BF disagrees: fall back to a negative
            # diagonal cell and reconstruct via closure predecessors.  This
            # should not happen with integer exact arithmetic, but stay safe.
            return self._cycle_from_closure()

        # Walk predecessors n steps to land on the cycle, then loop it.
        v = relaxed_nth
        for _ in range(n):
            v = prev[v]  # type: ignore[assignment]
        start = v
        cycle_nodes = [start]
        cycle_edges: List[Edge] = []
        cur = start
        while True:
            e = prev_edge[cur]
            p = prev[cur]
            assert e is not None and p is not None
            cycle_edges.append(e)
            cur = p
            cycle_nodes.append(cur)
            if cur == start:
                break
        cycle_nodes.reverse()
        cycle_edges.reverse()
        named = [self._nodes[i] for i in cycle_nodes]
        total = sum(e.w for e in cycle_edges)
        return NegativeCycle(tuple(named), tuple(cycle_edges), total)

    def _cycle_from_closure(self) -> NegativeCycle:
        n = len(self._nodes)
        for i in range(n):
            if self._d[i][i] is not None and self._d[i][i] < 0:
                # Build synthetic self-edge evidence via original edge list.
                best = min(
                    (e for e in self._edges if e.u == self._nodes[i]),
                    key=lambda e: e.w,
                    default=None,
                )
                edges = (best,) if best is not None else ()
                return NegativeCycle(
                    (self._nodes[i], self._nodes[i]),
                    edges,
                    self._d[i][i],
                )
        raise RuntimeError("no negative cycle found despite infeasible closure")

    # ------------------------------------------------------------------ #
    # Full recomputation (used after retraction): exact Floyd-Warshall
    # ------------------------------------------------------------------ #
    def _rebuild(self) -> None:
        """Recompute the closure from the surviving original edges.

        Bounds that lost their only supporting constraint are necessarily
        loosened here because the edges are rebuilt from scratch.
        """
        n = len(self._nodes)
        d: List[List[Optional[int]]] = [[None] * n for _ in range(n)]
        for i in range(n):
            d[i][i] = 0
        for e in self._edges:
            i = self._index[e.u]
            j = self._index[e.v]
            if d[i][j] is None or e.w < d[i][j]:
                d[i][j] = e.w

        for k in range(n):
            dk = d[k]
            for i in range(n):
                dik = d[i][k]
                if dik is None:
                    continue
                di = d[i]
                for j in range(n):
                    dkj = dk[j]
                    if dkj is None:
                        continue
                    cand = dik + dkj
                    if di[j] is None or cand < di[j]:
                        di[j] = cand

        self._d = d
        self._infeasible = None
        for i in range(n):
            if d[i][i] is not None and d[i][i] < 0:
                self._infeasible = self._find_negative_cycle()
                break

    # ------------------------------------------------------------------ #
    # Retraction: push/pop assumptions and named versions
    # ------------------------------------------------------------------ #
    def push(self) -> None:
        """Open an assumption level; constraints added until the matching
        ``pop`` can be withdrawn together."""
        self._levels.append([])

    def pop(self) -> None:
        """Withdraw every constraint added at the topmost push level and
        relax any bounds that have lost support (full recomputation)."""
        if not self._levels:
            raise RuntimeError("pop() with no matching push()")
        cids = self._levels.pop()
        self._remove_ids(cids)

    def commit(self) -> None:
        """Close the topmost level, keeping its constraints permanently."""
        if not self._levels:
            raise RuntimeError("commit() with no matching push()")
        top = self._levels.pop()
        if self._levels:
            self._levels[-1].extend(top)

    def depth(self) -> int:
        return len(self._levels)

    def remove_constraint(self, cid: str) -> None:
        """Remove a single constraint by id and recompute closure."""
        if cid not in self._constraints:
            raise KeyError(f"unknown constraint {cid!r}")
        for level in self._levels:
            if cid in level:
                level.remove(cid)
        self._remove_ids([cid])

    def _remove_ids(self, cids: Sequence[str]) -> None:
        drop = set(cids)
        if not drop:
            self._rebuild()
            return
        self._edges = [e for e in self._edges if e.cid not in drop]
        for cid in drop:
            self._constraints.pop(cid, None)
        self._rebuild()

    def save_version(self, name: str) -> None:
        """Persist an immutable snapshot of the current constraint set."""
        self._versions[name] = _Snapshot(
            nodes=list(self._nodes),
            constraints=[self._constraints[c] for c in self._constraints],
            cid_order=list(self._constraints),
            infeasible=self._infeasible,
        )

    def restore_version(self, name: str) -> None:
        snap = self._versions[name]
        self._nodes = list(snap.nodes)
        self._index = {n: i for i, n in enumerate(self._nodes)}
        self._constraints = {c.cid: c for c in snap.constraints}
        self._edges = []
        for c in snap.constraints:
            self._edges.extend(c.edges())
        self._levels = []
        self._rebuild()

    def versions(self) -> Tuple[str, ...]:
        return tuple(self._versions)

    # ------------------------------------------------------------------ #
    # Queries: earliest/latest windows, feasible schedules, explanations
    # ------------------------------------------------------------------ #
    def shortest(self, u: str, v: str) -> Optional[int]:
        """Raw closure value D[u][v] (tightest ``t(v)-t(u)`` upper bound)."""
        if self._infeasible is not None:
            raise InconsistentNetwork(self._infeasible)
        return self._d[self._index[u]][self._index[v]]

    def bounds(self, event: str, anchor: str = _ANCHOR,
               anchor_value: int = 0) -> Bounds:
        """Earliest/latest feasible window relative to an anchor.

        With an anchor ``a`` fixed to ``anchor_value``:
        ``anchor_value - D[event][a] <= t(event) <= anchor_value + D[a][event]``.
        A disconnected event has no bound on that side (``None``); with no
        anchor present at all every user event is returned unbounded.
        """
        if self._infeasible is not None:
            raise InconsistentNetwork(self._infeasible)
        i = self._index[event]
        if anchor not in self._index:
            return Bounds(event, None, None)
        a = self._index[anchor]
        d_ae = self._d[a][i]
        d_ea = self._d[i][a]
        latest = anchor_value + d_ae if d_ae is not None else None
        earliest = anchor_value - d_ea if d_ea is not None else None
        return Bounds(event, earliest, latest)

    def all_bounds(self, anchor: str = _ANCHOR,
                   anchor_value: int = 0) -> Dict[str, Bounds]:
        return {ev: self.bounds(ev, anchor, anchor_value) for ev in self.events}

    def explain(self) -> Optional[NegativeCycle]:
        """Return the infeasibility cycle with constraint IDs (None if feasible)."""
        return self._infeasible

    def solve(self, anchor: Optional[str] = _ANCHOR, anchor_value: int = 0,
              strict: bool = False) -> Dict[str, int]:
        """Return one feasible integer schedule.

        * If the network is anchored (default anchor ``Z`` exists, or an
          explicit ``anchor`` is given and reachable), anchored events are
          fixed consistently with ``anchor_value``.
        * Every connected component not linked to an anchor is free to
          translate; it is normalized to start at 0 (its earliest feasible
          point), making the translational freedom explicit and the result
          deterministic.
        * A completely unconstrained event is returned on its own at 0;
          consult ``bounds`` to see its full (-inf, +inf) range.

        ``strict=True`` raises :class:`NoSolution` if any event is outside
        the anchor's reachable component instead of translating it.
        """
        if self._infeasible is not None:
            raise InconsistentNetwork(self._infeasible)

        n = len(self._nodes)
        # Bellman-Ford from a virtual zero source yields a feasible potential
        # p with p[v] <= p[u] + w for every edge u -> v.
        p = [0] * n
        edges = sorted(self._edges, key=lambda e: (e.u, e.v, e.w, e.cid))
        ei = [(self._index[e.u], self._index[e.v], e.w) for e in edges]
        for _ in range(n - 1):
            changed = False
            for u, v, w in ei:
                if p[u] + w < p[v]:
                    p[v] = p[u] + w
                    changed = True
            if not changed:
                break

        # Undirected connected components over active edges.
        parent = list(range(n))

        def find(x: int) -> int:
            while parent[x] != x:
                parent[x] = parent[parent[x]]
                x = parent[x]
            return x

        def union(x: int, y: int) -> None:
            rx, ry = find(x), find(y)
            if rx != ry:
                parent[ry] = rx

        for u, v, _ in ei:
            union(u, v)

        anchor_idx = self._index[anchor] if anchor in self._index else None
        anchored_components = set()
        shifts: Dict[int, int] = {}
        if anchor_idx is not None:
            anchored_components.add(find(anchor_idx))
            # Shift the anchor's component so t(anchor) = anchor_value.
            shifts[find(anchor_idx)] = anchor_value - p[anchor_idx]

        result: Dict[str, int] = {}
        for name in self._nodes:
            if name == _ANCHOR:
                continue
            i = self._index[name]
            comp = find(i)
            if comp in anchored_components:
                result[name] = p[i] + shifts[comp]
            else:
                if strict:
                    raise NoSolution(
                        f"event {name!r} cannot be anchored to {anchor!r}"
                    )
                if comp not in shifts:
                    members = [k for k in range(n) if find(k) == comp]
                    shifts[comp] = -min(p[k] for k in members)
                result[name] = p[i] + shifts[comp]
        return result

    def verify_assignment(self, assignment: Dict[str, int]) -> List[str]:
        """Check an assignment against every active constraint; return a
        list of violated constraint descriptions (empty == valid)."""
        values = dict(assignment)
        values[_ANCHOR] = 0
        violated: List[str] = []
        for c in self._constraints.values():
            a = c.a if c.a is not None else _ANCHOR
            if c.b not in values or a not in values:
                violated.append(f"{c.describe()} (event missing)")
                continue
            delta = values[c.b] - values[a]
            if c.lo is not None and delta < c.lo:
                violated.append(f"{c.describe()} violated: delta={delta}")
            if c.hi is not None and delta > c.hi:
                violated.append(f"{c.describe()} violated: delta={delta}")
        return violated


class _Snapshot:
    __slots__ = ("nodes", "constraints", "cid_order", "infeasible")

    def __init__(self, nodes, constraints, cid_order, infeasible):
        self.nodes = nodes
        self.constraints = constraints
        self.cid_order = cid_order
        self.infeasible = infeasible
