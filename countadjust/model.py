"""Count-DAG model: nodes with noisy counts plus linear constraints.

Problem definition (all arithmetic exact over the rationals):

    minimize    1/2 * sum_i w_i * (x_i - y_i)^2
    subject to  A x = b          (e.g. parent == sum of children)
                x_i = f_i        (fixed counts, optional)
                x_i >= 0         (optional, when nonneg=True)

Weight semantics
----------------
w_i > 0 : trust in observation y_i; larger means more trusted.
w_i = 0 : the observation is IGNORED; x_i is a free variable determined
          only by the constraints.  If the constraints do not determine it
          either, the problem is singular and the solver reports
          Status.SINGULAR_ZERO_WEIGHT together with a certificate
          direction d != 0, W d = 0, A d = 0.
w_i < 0 : rejected at model construction.
"""

from fractions import Fraction

from .rational_linalg import F


class CountDAG:
    def __init__(self):
        self.names = []
        self._index = {}
        self.weights = []    # w_i (Fraction, >= 0)
        self.observed = []   # y_i (Fraction)
        self.fixed = {}      # index -> Fraction
        # constraints: list of (coeff dict index->Fraction, rhs Fraction, label)
        self.constraints = []

    def add_node(self, name, observed=0, weight=1):
        if name in self._index:
            raise ValueError(f"duplicate node name: {name!r}")
        w = F(weight)
        if w < 0:
            raise ValueError(f"negative weight for node {name!r}")
        self._index[name] = len(self.names)
        self.names.append(name)
        self.weights.append(w)
        self.observed.append(F(observed))
        return self._index[name]

    def fix(self, name, value):
        """Pin node ``name`` to an exact count (a hard equality)."""
        self.fixed[self._index[name]] = F(value)

    def add_parent_equals_children(self, parent, children, label=None):
        """Constraint x_parent - sum(x_child) = 0."""
        coefs = {self._index[parent]: Fraction(1)}
        for child in children:
            idx = self._index[child]
            coefs[idx] = coefs.get(idx, Fraction(0)) - 1
        coefs = {k: v for k, v in coefs.items() if v != 0}
        self.constraints.append(
            (coefs, Fraction(0), label or f"{parent}=sum({','.join(children)})")
        )

    def add_constraint(self, coefs_by_name, rhs=0, label=None):
        """General linear constraint sum(coef_i * x_i) = rhs."""
        coefs = {self._index[k]: F(v) for k, v in coefs_by_name.items()}
        coefs = {k: v for k, v in coefs.items() if v != 0}
        self.constraints.append((coefs, F(rhs), label or "linear"))

    @property
    def nvars(self):
        return len(self.names)

    def build_system(self):
        """Return (A, b) stacking DAG constraints and fixed-count rows."""
        n = self.nvars
        A, b = [], []
        for coefs, rhs, _label in self.constraints:
            row = [Fraction(0)] * n
            for idx, v in coefs.items():
                row[idx] = v
            A.append(row)
            b.append(rhs)
        for idx, value in sorted(self.fixed.items()):
            row = [Fraction(0)] * n
            row[idx] = Fraction(1)
            A.append(row)
            b.append(value)
        return A, b
