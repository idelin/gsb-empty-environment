"""Difference-constraint system used for joint feasibility solving.

Every fact about the merged log is expressed as ``x_v - x_u <= c`` over
exact rationals:

* offset bounds      ``lo_s <= o_s <= hi_s``
* wall-clock reading ``t_e = wall_e + o_source(e)``  (two inequalities)
* local log order    ``t_a <= t_b`` for consecutive events of one source
* cross-source cause ``t_a <= t_b`` for each happens-before edge

Feasibility is decided with Bellman-Ford; on infeasibility the negative
cycle (a concrete ring of contradictory constraints) is extracted and
reported -- times are never edited to force consistency. When feasible,
an all-pairs closure (Floyd-Warshall) yields the tightest entailed bound
for every pair of variables, which is exactly what the order analysis
needs.
"""
from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


@dataclass(frozen=True, slots=True)
class Constraint:
    kind: str
    text: str

    def __str__(self):
        return f"[{self.kind}] {self.text}"


class DifferenceConstraints:
    def __init__(self):
        self._nodes: list[str] = []
        self._index: dict[str, int] = {}
        self._edges: list[tuple[str, str, Fraction, Constraint]] = []

    def _var(self, name: str) -> str:
        if name not in self._index:
            self._index[name] = len(self._nodes)
            self._nodes.append(name)
        return name

    def add(self, upper: str, lower: str, c, constraint: Constraint):
        """Add ``x_upper - x_lower <= c``."""
        self._edges.append(
            (self._var(lower), self._var(upper), Fraction(c), constraint)
        )

    def add_equal(self, a: str, b: str, c, constraint: Constraint):
        """Add ``x_a - x_b == c`` as two inequalities."""
        self.add(a, b, c, constraint)
        self.add(b, a, -Fraction(c), constraint)

    @property
    def nodes(self) -> tuple[str, ...]:
        return tuple(self._nodes)

    def find_contradiction(self) -> list[Constraint] | None:
        """Return the constraint ring proving infeasibility, or None."""
        idx = self._index
        n = len(self._nodes)
        dist = [Fraction(0)] * n  # implicit super-source with zero edges
        pred: list[tuple[int, Constraint] | None] = [None] * n
        last = None
        for _ in range(n):
            last = None
            for u, v, w, cons in self._edges:
                iu, iv = idx[u], idx[v]
                if dist[iu] + w < dist[iv]:
                    dist[iv] = dist[iu] + w
                    pred[iv] = (iu, cons)
                    last = iv
            if last is None:
                return None
        # `last` can reach a negative cycle; walk predecessors to enter it.
        x = last
        for _ in range(n):
            x = pred[x][0]
        ring: list[Constraint] = []
        y = x
        while True:
            p, cons = pred[y]
            ring.append(cons)
            y = p
            if y == x:
                break
        ring.reverse()
        return ring

    def closure(self) -> "Bounds":
        """Tightest entailed bounds for every pair of variables.

        Must only be called when the system is feasible.
        """
        idx = self._index
        n = len(self._nodes)
        inf = None
        d: list[list[Fraction | None]] = [[inf] * n for _ in range(n)]
        for i in range(n):
            d[i][i] = Fraction(0)
        for u, v, w, _ in self._edges:
            iu, iv = idx[u], idx[v]
            if d[iu][iv] is None or w < d[iu][iv]:
                d[iu][iv] = w
        for k in range(n):
            dk = d[k]
            for i in range(n):
                di = d[i]
                dik = di[k]
                if dik is None:
                    continue
                for j in range(n):
                    dkj = dk[j]
                    if dkj is None:
                        continue
                    cand = dik + dkj
                    if di[j] is None or cand < di[j]:
                        di[j] = cand
        return Bounds(self._nodes, d)


class Bounds:
    """Query interface over the all-pairs closure.

    ``d[u][v]`` is the tightest entailed upper bound of ``x_v - x_u``.
    """

    def __init__(self, nodes: list[str], d: list[list[Fraction | None]]):
        self._index = {name: i for i, name in enumerate(nodes)}
        self._d = d

    def ub_diff(self, a: str, b: str) -> Fraction | None:
        """Tightest upper bound of ``x_a - x_b`` (None = unbounded)."""
        return self._d[self._index[b]][self._index[a]]

    def lb(self, a: str, zero: str) -> Fraction:
        """Tightest lower bound of ``x_a - x_zero``."""
        return -self._d[self._index[a]][self._index[zero]]

    def ub(self, a: str, zero: str) -> Fraction:
        """Tightest upper bound of ``x_a - x_zero``."""
        return self._d[self._index[zero]][self._index[a]]
