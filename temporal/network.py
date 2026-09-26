"""Necessary-before partial order over events with closed time intervals.

Time model
----------
Each event occupies a *closed* interval ``[lo, hi]`` (exact rationals, never
floats). Causal edges impose strict precedence ``cause < effect``. All
constraints are compiled into a simple temporal network (STN) whose weights
live in the lexicographic group ``Q x Z``: a weight ``(r, k)`` means
``r + k*epsilon`` for an infinitesimal ``epsilon > 0``.

Semantics
---------
``a`` is *necessarily before* ``b`` (written ``a << b``) iff every feasible
point assignment satisfies ``t(a) < t(b)``. With the reference variable
``t_REF = 0`` and shortest paths ``d`` over the STN, this holds exactly when
``d(b, a) < 0``. Nothing is ever ordered by interval midpoints: pairs that
are not ordered either way are reported as *incomparable*, and the
incomparability relation is never treated as a transitive equivalence.

Outputs
-------
- ``necessary_pairs()``: all necessary precedences, each with the original
  constraints that support it (evidence).
- ``transitive_reduction()``: the compact representation (Hasse edges).
- ``layers()``: definite order layers; each layer is an antichain.
- ``contradictions()``: negative cycles with their original-constraint
  evidence.
- ``witness()``: one feasible exact time assignment.
- ``retract()``: removes a constraint; derived relations that lose their
  support disappear on the next query.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction
from typing import Optional

from .clocks import as_fraction

REF = "@epoch"  # reference variable, pinned to 0
Weight = tuple  # (Fraction, int), lexicographic: r + k*epsilon
ZERO: Weight = (Fraction(0), 0)


def _wadd(a: Weight, b: Weight) -> Weight:
    return (a[0] + b[0], a[1] + b[1])


@dataclass(frozen=True)
class CycleReport:
    """A contradiction cycle with the original constraints that cause it."""

    events: tuple  # cycle nodes in order (edges close back to the first)
    weight: Weight
    evidence: frozenset  # ids of the original constraints on the cycle

    def __str__(self) -> str:
        names = ["epoch0" if e == REF else e for e in list(self.events) + [self.events[0]]]
        loop = " -> ".join(names)
        return (
            f"contradiction cycle [{loop}] weight={self.weight} "
            f"evidence={sorted(self.evidence)}"
        )


class ContradictionError(Exception):
    """Raised when a query needs a consistent network but cycles exist."""

    def __init__(self, cycles):
        self.cycles = list(cycles)
        msg = "; ".join(str(c) for c in self.cycles)
        super().__init__(msg or "temporal constraints are contradictory")


@dataclass(frozen=True)
class IntervalBound:
    """Closed interval [lo, hi] for one event. Correctable via re-adding."""

    event: str
    lo: Fraction
    hi: Fraction

    def __post_init__(self) -> None:
        object.__setattr__(self, "lo", as_fraction(self.lo))
        object.__setattr__(self, "hi", as_fraction(self.hi))
        if self.lo > self.hi:
            raise ValueError(f"empty interval for {self.event}: [{self.lo}, {self.hi}]")


@dataclass(frozen=True)
class CausalEdge:
    """Explicit strict precedence: cause happens before effect."""

    cause: str
    effect: str


def _extract_cycles(walk: list) -> list:
    """Decompose a node walk into its simple cycles."""
    cycles = []
    pos = {}
    cur: list = []
    for node in walk:
        if node in pos:
            start = pos[node]
            cycles.append(cur[start:])
            cur = cur[:start]
            pos = {n: i for i, n in enumerate(cur)}
        pos[node] = len(cur)
        cur.append(node)
    return cycles


def _canonical_cycle(cyc: list) -> tuple:
    i = cyc.index(min(cyc))
    return tuple(cyc[i:] + cyc[:i])


class TemporalNetwork:
    """Events + closed intervals + causal edges -> necessary-before order."""

    def __init__(self) -> None:
        self._intervals: dict[str, IntervalBound] = {}
        self._causal: dict[tuple, CausalEdge] = {}
        self._cache: Optional[tuple] = None

    # ------------------------------------------------------------------
    # mutation
    # ------------------------------------------------------------------
    def add_interval(self, event: str, lo, hi) -> str:
        """Add (or correct) the closed interval [lo, hi] of ``event``."""
        self._intervals[event] = IntervalBound(event, lo, hi)
        self._cache = None
        return f"interval:{event}"

    def correct_interval(self, event: str, lo, hi) -> str:
        """Correct an event's time interval (replaces the previous one)."""
        return self.add_interval(event, lo, hi)

    def add_causal(self, cause: str, effect: str) -> str:
        """Add an explicit causal edge: cause strictly precedes effect."""
        self._causal[(cause, effect)] = CausalEdge(cause, effect)
        self._cache = None
        return f"causal:{cause}->{effect}"

    def retract(self, constraint_id: str) -> None:
        """Withdraw one original constraint by id.

        Derived precedences that lose all supporting constraints disappear
        from every subsequent query.
        """
        kind, _, rest = constraint_id.partition(":")
        if kind == "interval" and rest in self._intervals:
            del self._intervals[rest]
        elif kind == "causal":
            cause, _, effect = rest.partition("->")
            if (cause, effect) not in self._causal:
                raise KeyError(self._unknown_msg(constraint_id))
            del self._causal[(cause, effect)]
        else:
            raise KeyError(self._unknown_msg(constraint_id))
        self._cache = None

    def _unknown_msg(self, constraint_id: str) -> str:
        return f"unknown constraint {constraint_id!r}; known: {self.constraint_ids()}"

    # ------------------------------------------------------------------
    # inspection of the original constraints
    # ------------------------------------------------------------------
    def constraint_ids(self) -> list:
        ids = [f"interval:{e}" for e in sorted(self._intervals)]
        ids += [f"causal:{c}->{e}" for (c, e) in sorted(self._causal)]
        return ids

    def events(self) -> list:
        names = set(self._intervals)
        for cause, effect in self._causal:
            names.add(cause)
            names.add(effect)
        return sorted(names)

    def intervals(self) -> dict:
        return dict(self._intervals)

    def causal_edges(self) -> list:
        return [self._causal[k] for k in sorted(self._causal)]

    # ------------------------------------------------------------------
    # STN compilation and shortest paths
    # ------------------------------------------------------------------
    def _graph(self) -> dict:
        """Compile constraints to STN edges (u, v) -> [weight, support].

        Constraint ``t(v) - t(u) <= w`` becomes edge ``u -> v`` of weight ``w``:
        - ``t(v) >= lo``  -> edge ``v -> REF`` weight ``(-lo, 0)``
        - ``t(v) <= hi``  -> edge ``REF -> v`` weight ``(hi, 0)``
        - causal c << e   -> edge ``e -> c``  weight ``(0, -1)``  (t(c) - t(e) <= -eps)
        """
        W: dict[tuple, list] = {}

        def relax(u: str, v: str, w: Weight, support: set) -> None:
            key = (u, v)
            if key not in W or w < W[key][0]:
                W[key] = [w, frozenset(support)]

        for iv in self._intervals.values():
            cid = f"interval:{iv.event}"
            relax(iv.event, REF, (-iv.lo, 0), {cid})
            relax(REF, iv.event, (iv.hi, 0), {cid})
        for ce in self._causal.values():
            relax(ce.effect, ce.cause, (0, -1), {f"causal:{ce.cause}->{ce.effect}"})
        return W

    def _solve(self) -> tuple:
        """Floyd-Warshall over the lexicographic weight group (cached)."""
        if self._cache is not None:
            return self._cache
        W = self._graph()
        nodes = self.events() + [REF]
        dist = {u: {} for u in nodes}
        nxt = {}
        for u in nodes:
            dist[u][u] = ZERO
        for (u, v), (w, _s) in W.items():
            cur = dist[u].get(v)
            if cur is None or w < cur:
                dist[u][v] = w
                nxt[(u, v)] = v
        for k in nodes:
            dk = dist[k]
            for u in nodes:
                duk = dist[u].get(k)
                if duk is None:
                    continue
                du = dist[u]
                for v, dkv in dk.items():
                    cand = _wadd(duk, dkv)
                    cur = du.get(v)
                    if cur is None or cand < cur:
                        du[v] = cand
                        nxt[(u, v)] = nxt[(u, k)]
        self._cache = (dist, nxt, W)
        return self._cache

    def _path(self, u: str, v: str) -> Optional[list]:
        dist, nxt, _W = self._solve()
        if (u, v) not in nxt:
            return None
        cap = 2 * (len(dist) + 2)
        path = [u]
        while u != v and len(path) <= cap:
            u = nxt[(u, v)]
            path.append(u)
        return path

    def _path_support(self, path: list) -> frozenset:
        _dist, _nxt, W = self._solve()
        support: set = set()
        for a, b in zip(path, path[1:]):
            support |= W[(a, b)][1]
        return frozenset(support)

    # ------------------------------------------------------------------
    # contradictions
    # ------------------------------------------------------------------
    def is_consistent(self) -> bool:
        dist, _nxt, _W = self._solve()
        return all(not (dist[u].get(u, ZERO) < ZERO) for u in dist)

    def contradictions(self) -> list:
        """Negative cycles, each carrying its original-constraint evidence."""
        dist, nxt, W = self._solve()
        cap = 2 * (len(dist) + 2)
        reports = []
        seen = set()
        for start in dist:
            d = dist[start].get(start)
            if d is None or not d < ZERO:
                continue
            walk = [start]
            node = start
            while len(walk) <= cap:
                hop = nxt.get((node, start))
                if hop is None:
                    break
                walk.append(hop)
                node = hop
                if node == start:
                    break
            for cyc in _extract_cycles(walk):
                weight = ZERO
                evidence: set = set()
                ok = True
                for a, b in zip(cyc, cyc[1:] + cyc[:1]):
                    edge = W.get((a, b))
                    if edge is None:
                        ok = False
                        break
                    weight = _wadd(weight, edge[0])
                    evidence |= edge[1]
                if not ok or not weight < ZERO:
                    continue
                key = _canonical_cycle(cyc)
                if key in seen:
                    continue
                seen.add(key)
                reports.append(CycleReport(tuple(cyc), weight, frozenset(evidence)))
        return reports

    def _ensure_consistent(self) -> None:
        if not self.is_consistent():
            raise ContradictionError(self.contradictions())

    # ------------------------------------------------------------------
    # the necessary-before partial order
    # ------------------------------------------------------------------
    def necessary_pairs(self) -> dict:
        """All pairs (a, b) with a necessarily strictly before b.

        Maps each pair to the frozenset of original constraint ids that
        support it. This is the full relation; use transitive_reduction()
        for the compact representation.
        """
        self._ensure_consistent()
        dist, _nxt, _W = self._solve()
        pairs = {}
        for a in self.events():
            for b in self.events():
                if a == b:
                    continue
                d = dist[b].get(a)  # t(a) - t(b) <= d(b, a) < 0  <=>  a << b
                if d is not None and d < ZERO:
                    pairs[(a, b)] = self._path_support(self._path(b, a))
        return pairs

    def is_necessarily_before(self, a: str, b: str) -> bool:
        self._ensure_consistent()
        dist, _nxt, _W = self._solve()
        d = dist[b].get(a)
        return d is not None and d < ZERO

    def incomparable(self, a: str, b: str) -> bool:
        return not self.is_necessarily_before(a, b) and not self.is_necessarily_before(b, a)

    def incomparable_pairs(self) -> list:
        """Unordered pairs ordered in neither direction.

        Incomparability is *not* transitive; this list is never collapsed
        into equivalence classes.
        """
        self._ensure_consistent()
        dist, _nxt, _W = self._solve()
        events = self.events()
        out = []
        for i, a in enumerate(events):
            for b in events[i + 1 :]:
                dab = dist[b].get(a)
                dba = dist[a].get(b)
                fwd = dab is not None and dab < ZERO
                bwd = dba is not None and dba < ZERO
                if not fwd and not bwd:
                    out.append((a, b))
        return out

    def is_total(self) -> bool:
        """False whenever any pair is incomparable (no fake total order)."""
        return not self.incomparable_pairs()

    def transitive_reduction(self) -> dict:
        """Compact representation: Hasse edges of the partial order."""
        pairs = self.necessary_pairs()
        events = self.events()
        red = {}
        for (a, b), support in pairs.items():
            covered = any(
                c != a and c != b and (a, c) in pairs and (c, b) in pairs
                for c in events
            )
            if not covered:
                red[(a, b)] = support
        return red

    def layers(self) -> list:
        """Definite order layers (longest-path layering of the reduction).

        Events in the same layer are pairwise incomparable.
        """
        red = self.transitive_reduction()
        events = self.events()
        preds = {e: [] for e in events}
        for a, b in red:
            preds[b].append(a)
        level: dict = {}

        def lvl(v: str) -> int:
            if v not in level:
                level[v] = 0 if not preds[v] else 1 + max(lvl(p) for p in preds[v])
            return level[v]

        for e in events:
            lvl(e)
        grouped: dict = {}
        for e, lv in level.items():
            grouped.setdefault(lv, []).append(e)
        return [tuple(sorted(grouped[lv])) for lv in sorted(grouped)]

    def incomparable_groups(self) -> list:
        """Antichains of the order (one per layer).

        These are genuine antichains: every two members are incomparable.
        They are deliberately *not* equivalence classes of the
        incomparability relation, which is not transitive.
        """
        return self.layers()

    # ------------------------------------------------------------------
    # feasible time witness
    # ------------------------------------------------------------------
    def witness(self) -> dict:
        """One feasible exact assignment of a time point to every event.

        Computed as shortest paths from the reference in the (r, k*epsilon)
        group, then instantiated with the largest epsilon that keeps every
        original constraint satisfied. All values are exact Fractions.
        """
        self._ensure_consistent()
        dist, _nxt, W = self._solve()
        t = {u: ZERO for u in dist}
        for v, d in dist[REF].items():
            t[v] = d
        eps: Optional[Fraction] = None
        for (u, v), (w, _s) in W.items():
            dr = t[v][0] - t[u][0] - w[0]
            dk = t[v][1] - t[u][1] - w[1]
            if dr > 0 or (dr == 0 and dk > 0):
                raise AssertionError("inconsistent STN escaped detection")
            if dr < 0 and dk > 0:
                cand = -dr / Fraction(dk)
                if eps is None or cand < eps:
                    eps = cand
        if eps is None:
            eps = Fraction(1)
        return {e: t[e][0] + t[e][1] * eps for e in self.events()}
