"""Resource budgets for exact arithmetic.

Two budgets are tracked:

* ``max_bits``    -- maximum bit length allowed for any numerator/denominator
                     appearing in polynomial arithmetic (Sturm sequences etc.).
* ``max_refine``  -- maximum number of isolation/refinement bisection steps.

When a budget is exceeded a :class:`BudgetExceeded` is raised.  The solver
catches it and returns a *partial* solution containing the certified fragments
computed so far plus the still-undecided isolating regions; it never claims a
complete solution set in that case.
"""
from __future__ import annotations

from fractions import Fraction


class BudgetExceeded(Exception):
    """Raised when an arithmetic or refinement budget is exhausted.

    ``frontier``/``found`` are filled in by the root isolator so the caller can
    report pending isolating intervals and already certified roots.
    """

    def __init__(self, kind, message="", frontier=None, found=None):
        super().__init__(message or kind)
        self.kind = kind
        self.frontier = frontier
        self.found = found


class Budget:
    def __init__(self, max_bits=100_000, max_refine=100_000):
        if max_bits < 0 or max_refine < 0:
            raise ValueError("budgets must be non-negative")
        self.max_bits = max_bits
        self.max_refine = max_refine
        self.refine_used = 0

    def tick(self, n=1):
        """Account for ``n`` isolation/refinement steps."""
        self.refine_used += n
        if self.refine_used > self.max_refine:
            raise BudgetExceeded(
                "refine",
                f"refinement budget exceeded ({self.refine_used} > {self.max_refine})",
            )

    def check_value(self, v):
        v = Fraction(v)
        if max(abs(v.numerator), v.denominator).bit_length() > self.max_bits:
            raise BudgetExceeded(
                "bits",
                f"coefficient bit length exceeds budget ({self.max_bits} bits)",
            )

    def check_poly(self, p):
        for c in p:
            self.check_value(c)
