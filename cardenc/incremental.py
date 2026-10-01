"""Incremental at-most-k encoding with assumption-based retraction.

The sequential-counter structure is built once for a maximum capacity K.
Every concrete bound k <= K gets a dedicated activation literal a_k, and the
bound-enforcing clauses are guarded by it:  (~a_k | ~x_i | ~s_{i-1,k}).

* Tightening: request a smaller bound via ``tighten`` / ``assumptions`` and
  solve under the new activation literal.  No clause is ever removed.
* Retraction (relaxing): simply solve under the activation literal of a
  previously registered, weaker bound.  Clauses guarded by other activation
  literals are automatically satisfied because those literals are false.

This mirrors the standard SAT-solver assumption mechanism; the bundled DPLL
solver accepts the assumption list directly.
"""

from __future__ import annotations

from .core import CNF


class IncrementalAtMost:
    def __init__(self, pool, lits, capacity, tag="inc"):
        n = len(lits)
        if not 0 <= capacity <= max(n, 0):
            raise ValueError("capacity must satisfy 0 <= capacity <= n")
        self.pool = pool
        self.lits = list(lits)
        self.capacity = capacity
        self.cnf = CNF()
        self.aux = []
        self.activation = {}  # bound k -> activation variable
        self.s = {}
        if capacity > 0 and n > 0:
            for i in range(1, n + 1):
                for j in range(1, capacity + 1):
                    var = pool.new_var(f"{tag}.s{i}.{j}")
                    self.s[i, j] = var
                    self.aux.append(var)
            # Bound-independent structural clauses (capacity K).
            self.cnf.add(-self.lits[0], self.s[1, 1])
            for i in range(2, n + 1):
                xi = self.lits[i - 1]
                self.cnf.add(-xi, self.s[i, 1])
                self.cnf.add(-self.s[i - 1, 1], self.s[i, 1])
                for j in range(2, capacity + 1):
                    self.cnf.add(-self.s[i - 1, j], self.s[i, j])
                    self.cnf.add(-xi, -self.s[i - 1, j - 1], self.s[i, j])

    def tighten(self, k):
        """Register bound ``k`` (adding its guarded clauses) and return the
        activation literal.  Tightening to a smaller k only *adds* clauses."""
        if k < 0 or k > self.capacity:
            raise ValueError(f"bound {k} outside capacity {self.capacity}")
        if k in self.activation:
            return self.activation[k]
        a = self.pool.new_var(f"inc.a{k}")
        self.activation[k] = a
        self.aux.append(a)
        n = len(self.lits)
        if k == 0:
            for xi in self.lits:
                self.cnf.add(-a, -xi)
        else:
            for i in range(2, n + 1):
                self.cnf.add(-a, -self.lits[i - 1], -self.s[i - 1, k])
        return a

    def assumptions(self, k):
        """Assumption list enforcing bound k (registers it if needed)."""
        return [self.tighten(k)]

    def lift(self, values, k):
        """Auxiliary witness for a satisfying original assignment."""
        out = {}
        run = 0
        for i, lit in enumerate(self.lits, start=1):
            v = values[abs(lit)]
            if (v if lit > 0 else not v):
                run += 1
            for j in range(1, self.capacity + 1):
                out[self.s[i, j]] = run >= j
        for bound, var in self.activation.items():
            out[var] = bound == k
        return out
