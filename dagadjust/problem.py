"""Problem definition: a counting DAG plus weighted noisy count observations.

Definitions
-----------
- CountDAG: nodes 0..n-1; each internal node p has children(p) and the
  balance constraint  x_p = sum_{c in children(p)} x_c.  Leaves may be
  shared: a node can be a child of several parents (counting DAG, not
  necessarily a tree).
- observed y_i: the noisy observed count of node i (synthetic data only).
- weight w_i >= 0: trust in y_i.  w_i = 0 means the observation is
  IGNORED: x_i is then determined only by the constraints.  If that
  leaves the system underdetermined, the solver reports a singularity
  instead of silently inventing a value.
- fixed: exact known counts x_i = f_i (e.g. already-published totals).
- nonneg: if True, additionally require x >= 0.
"""

from __future__ import annotations

from dataclasses import dataclass
from fractions import Fraction


@dataclass(frozen=True)
class CountDAG:
    n_nodes: int
    children: tuple  # tuple of (parent, (child, ...)), sorted by parent

    @staticmethod
    def from_children(n_nodes, children):
        items = tuple(
            sorted((int(p), tuple(int(c) for c in cs)) for p, cs in children.items())
        )
        dag = CountDAG(n_nodes=int(n_nodes), children=items)
        dag._validate()
        return dag

    def _validate(self):
        adjacency = {}
        for p, cs in self.children:
            if not 0 <= p < self.n_nodes:
                raise ValueError(f"parent index out of range: {p}")
            if not cs:
                raise ValueError(f"parent {p} has no children")
            if len(set(cs)) != len(cs):
                raise ValueError(f"parent {p} lists a child twice")
            for c in cs:
                if not 0 <= c < self.n_nodes:
                    raise ValueError(f"child index out of range: {c}")
            adjacency[p] = cs
        color = {}

        def visit(u):
            color[u] = 1
            for v in adjacency.get(u, ()):
                state = color.get(v)
                if state == 1:
                    raise ValueError("cycle detected: not a DAG")
                if state is None:
                    visit(v)
            color[u] = 2

        for u in range(self.n_nodes):
            if color.get(u) is None:
                visit(u)

    def internal_nodes(self):
        return [p for p, _ in self.children]

    def leaves(self):
        parents = {p for p, _ in self.children}
        return [u for u in range(self.n_nodes) if u not in parents]

    def constraint_rows(self):
        """One row per internal node: x_parent - sum(children) = 0."""
        rows = []
        labels = []
        for p, cs in self.children:
            row = [Fraction(0)] * self.n_nodes
            row[p] = Fraction(1)
            for c in cs:
                row[c] -= 1
            rows.append(row)
            labels.append(f"x{p} = " + "+".join(f"x{c}" for c in cs))
        return rows, labels


@dataclass(frozen=True)
class AdjustmentProblem:
    dag: CountDAG
    observed: tuple  # y_i, one per node
    weights: tuple  # w_i >= 0; 0 means "observation ignored"
    fixed: tuple  # tuple of (index, exact value), sorted by index
    nonneg: bool = False

    @staticmethod
    def create(dag, observed, weights, fixed=None, nonneg=False):
        n = dag.n_nodes
        y = tuple(Fraction(v) for v in observed)
        w = tuple(Fraction(v) for v in weights)
        if len(y) != n or len(w) != n:
            raise ValueError("observed/weights must have one entry per node")
        if any(wi < 0 for wi in w):
            raise ValueError("weights must be >= 0")
        fx = tuple(
            sorted((int(i), Fraction(v)) for i, v in dict(fixed or {}).items())
        )
        for i, _ in fx:
            if not 0 <= i < n:
                raise ValueError(f"fixed index out of range: {i}")
        return AdjustmentProblem(dag, y, w, fx, bool(nonneg))

    def with_observation(self, index, value, weight=None):
        """Local revision: replace one observation (and optionally its
        weight), returning a new problem.  The old result may be used as
        a warm start, but certificates are always recomputed from
        scratch by the solver."""
        y = list(self.observed)
        w = list(self.weights)
        y[index] = Fraction(value)
        if weight is not None:
            w[index] = Fraction(weight)
        return AdjustmentProblem(self.dag, tuple(y), tuple(w), self.fixed, self.nonneg)

    def constraints(self):
        """Full constraint system C x = d: DAG balance rows followed by
        fixed-count rows.  Rows may be linearly redundant; the solver
        detects and reports that."""
        rows, labels = self.dag.constraint_rows()
        rhs = [Fraction(0)] * len(rows)
        for i, v in self.fixed:
            row = [Fraction(0)] * self.dag.n_nodes
            row[i] = Fraction(1)
            rows.append(row)
            labels.append(f"x{i} fixed = {v}")
            rhs.append(v)
        return rows, rhs, labels
