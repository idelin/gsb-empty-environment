"""Sparse affine forms over shared noise symbols.

A quantity is represented as  center + sum_i coeffs[i] * eps_i  where each
eps_i is a *shared* noise symbol ranging over [-1, 1].  Two forms that mention
the same symbol index are correlated by construction, so  x - x  cancels
exactly while  x - y  (independent symbols) does not.

Stdlib only; no NumPy/SciPy.
"""


class BudgetExceeded(Exception):
    """Raised when allocating a noise symbol would exceed the budget."""


class NoiseSpace:
    """Allocates shared noise-symbol indices under an optional hard budget."""

    def __init__(self, max_symbols=None):
        if max_symbols is not None and max_symbols < 0:
            raise ValueError("max_symbols must be non-negative or None")
        self.max_symbols = max_symbols
        self.names = {}

    def __len__(self):
        return len(self.names)

    def fresh(self, name=None):
        """Allocate the next explicit symbol index, enforcing the budget."""
        if self.max_symbols is not None and len(self.names) >= self.max_symbols:
            raise BudgetExceeded(
                f"noise-symbol budget {self.max_symbols} exceeded "
                f"(needed symbol #{len(self.names)})"
            )
        sid = len(self.names)
        self.names[sid] = name if name is not None else f"eps{sid}"
        return sid


class AffineForm:
    """center + sum(coeffs[i] * eps_i), eps_i in [-1, 1]."""

    __slots__ = ("center", "coeffs")

    def __init__(self, center=0.0, coeffs=None):
        self.center = float(center)
        self.coeffs = {}
        if coeffs:
            for idx, coeff in coeffs.items():
                if coeff != 0.0:
                    self.coeffs[int(idx)] = float(coeff)

    def radius(self):
        return sum(abs(c) for c in self.coeffs.values())

    def interval(self):
        r = self.radius()
        return (self.center - r, self.center + r)

    def is_exact_zero(self):
        return self.center == 0.0 and not self.coeffs

    def shifted(self, c):
        return AffineForm(self.center + c, self.coeffs)

    def scaled(self, k):
        return AffineForm(self.center * k, {i: c * k for i, c in self.coeffs.items()})

    @staticmethod
    def add(a, b, sign_b=1.0):
        coeffs = dict(a.coeffs)
        for idx, coeff in b.coeffs.items():
            coeffs[idx] = coeffs.get(idx, 0.0) + sign_b * coeff
        return AffineForm(a.center + sign_b * b.center, coeffs)

    def __repr__(self):
        return f"AffineForm(center={self.center!r}, coeffs={self.coeffs!r})"
