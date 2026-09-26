"""Exact difference-constraint systems:  x_v - x_u <= c  with Fraction c.

Feasibility of such a system is decided by negative-cycle detection
(Bellman-Ford) with exact rational arithmetic.  Variable 0 is reserved as
the constant-zero reference node, so single-variable bounds are also
expressible as difference constraints.
"""

from __future__ import annotations

from fractions import Fraction


class DifferenceConstraints:
    """A system of difference constraints over variables 0..num_vars-1."""

    def __init__(self, num_vars):
        if num_vars < 1:
            raise ValueError("need at least the reference variable")
        self.num_vars = num_vars
        self.edges = []  # (u, v, c) meaning x_v - x_u <= c
        self._ap_cache = None

    def add_difference(self, u, v, c):
        """Add the constraint  x_v - x_u <= c."""
        self.edges.append((u, v, Fraction(c)))
        self._ap_cache = None

    def add_bound(self, v, lo=None, hi=None):
        """Add  lo <= x_v <= hi  (either side optional)."""
        if hi is not None:
            self.add_difference(0, v, hi)
        if lo is not None:
            self.add_difference(v, 0, -Fraction(lo))

    def _bellman_ford(self, dist):
        """Relax until fixpoint; return None on negative cycle."""
        for _ in range(self.num_vars - 1):
            changed = False
            for u, v, c in self.edges:
                if dist[u] is not None and (dist[v] is None or dist[v] > dist[u] + c):
                    dist[v] = dist[u] + c
                    changed = True
            if not changed:
                break
        for u, v, c in self.edges:
            if dist[u] is not None and (dist[v] is None or dist[v] > dist[u] + c):
                return None
        return dist

    def feasible(self):
        # A virtual super-source at distance 0 from every node.
        return self._bellman_ford([Fraction(0)] * self.num_vars) is not None

    def witness(self):
        """Return a feasible assignment (the tightest upper-bound one)."""
        dist = self._bellman_ford([Fraction(0)] * self.num_vars)
        if dist is None:
            raise ValueError("system is infeasible")
        return dist

    def shortest_paths_from(self, source):
        dist = [None] * self.num_vars
        dist[source] = Fraction(0)
        dist = self._bellman_ford(dist)
        if dist is None:
            raise ValueError("negative cycle reachable from source")
        return dist

    def all_pairs(self):
        """All-pairs shortest paths (cached); ap[u][v] bounds x_v - x_u."""
        if self._ap_cache is None:
            self._ap_cache = [self.shortest_paths_from(s) for s in range(self.num_vars)]
        return self._ap_cache

    def tight_bound(self, v):
        """Tightest (lo, hi) with lo <= x_v <= hi implied by the system."""
        ap = self.all_pairs()
        hi, lo = ap[0][v], ap[v][0]
        if hi is None or lo is None:
            raise ValueError("unbounded variable")
        return -lo, hi
