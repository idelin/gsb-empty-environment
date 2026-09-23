"""Extended Euclidean algorithm and a denominator-growth-controlled
polynomial remainder sequence (subresultant PRS) over Q[x].

Everything is exact (fractions.Fraction).  The PRS computer supports
pause/resume, JSON-able checkpoints, and a coefficient budget: when the
budget is exceeded the machine stops in a non-terminal ``budget_exceeded``
state and can be resumed later (e.g. with a raised budget).
"""
from __future__ import annotations

from dataclasses import dataclass, field
from fractions import Fraction

from .core import (
    Poly, trim, deg, lc, monic, is_zero,
    add, sub, mul, scale, divmod_q, pseudo_divmod,
)

STATUS_RUNNING = "running"
STATUS_PAUSED = "paused"
STATUS_COMPLETED = "completed"
STATUS_BUDGET_EXCEEDED = "budget_exceeded"


# ---------------------------------------------------------------------------
# Plain extended Euclidean algorithm over Q[x]
# ---------------------------------------------------------------------------

def extended_gcd(a: Poly, b: Poly):
    """Return (g, s, t) with s*a + t*b == g exactly and g monic.

    Conventions for degenerate inputs (valid over a field):
      * gcd(0, 0) = 0 with s = t = 0;
      * gcd(a, 0) = monic(a), gcd(0, b) = monic(b);
      * a nonzero constant gcd is normalised to 1.
    """
    a, b = trim(a), trim(b)
    if not a and not b:
        return [], [], []
    if not a:
        c = lc(b)
        return monic(b), [], [Fraction(1, 1) / c]
    if not b:
        c = lc(a)
        return monic(a), [Fraction(1, 1) / c], []
    r0, r1 = a, b
    s0, s1 = [Fraction(1)], []
    t0, t1 = [], [Fraction(1)]
    while r1:
        q, r = divmod_q(r0, r1)
        r0, r1 = r1, r
        s0, s1 = s1, sub(s0, mul(q, s1))
        t0, t1 = t1, sub(t0, mul(q, t1))
    c = lc(r0)
    inv = Fraction(1, 1) / c
    return scale(r0, inv), scale(s0, inv), scale(t0, inv)


# ---------------------------------------------------------------------------
# Subresultant PRS with per-step Bezout certificates
# ---------------------------------------------------------------------------

def _coeff_size(*polys: Poly) -> int:
    """Budget unit: total decimal digits of all numerators/denominators."""
    total = 0
    for p in polys:
        for c in p:
            total += len(str(abs(c.numerator))) + len(str(c.denominator))
    return total


@dataclass
class PRSStep:
    """One subresultant-PRS transition r_prev, r_cur -> r_next.

    Invariants (all exact over Q):
      lc(r_cur)**(delta+1) * r_prev == pseudo_quotient*r_cur + pseudo_remainder
      remainder == pseudo_remainder / beta
      remainder == bezout_s * A0 + bezout_t * B0     (Bezout certificate)
    """
    index: int
    dividend: Poly
    divisor: Poly
    pseudo_quotient: Poly
    pseudo_remainder: Poly
    beta: Fraction
    remainder: Poly
    bezout_s: Poly
    bezout_t: Poly
    note: str


class PRSComputer:
    """Incremental subresultant PRS for gcd(A0, B0) over Q[x].

    The subresultant scaling factor beta keeps coefficient/denominator
    growth near-linear instead of exponential (Brown--Traub).  Each step
    records its pseudo-quotient/remainder and a Bezout certificate.
    """

    def __init__(self, a: Poly, b: Poly, budget: int | None = None):
        a, b = trim(a), trim(b)
        self._swapped = deg(a) < deg(b)
        if self._swapped:
            a, b = b, a
        self.A0, self.B0 = a, b
        self.budget = budget
        self.steps: list[PRSStep] = []
        self.status = STATUS_RUNNING
        self.gcd: Poly | None = None
        self.bezout: tuple[Poly, Poly] | None = None
        # loop state
        self._r_prev, self._r_cur = a, b
        self._s_prev, self._s_cur = [Fraction(1)], []
        self._t_prev, self._t_cur = [], [Fraction(1)]
        self._g = Fraction(1)   # subresultant scalar g
        self._h = Fraction(1)   # subresultant scalar h
        self._first = True
        if not b:
            self._finish_degenerate()

    # -- termination -------------------------------------------------------

    def _finish_degenerate(self) -> None:
        # B0 == 0: gcd(A0, 0) = monic(A0); gcd(0, 0) = 0.
        if not self.A0:
            self.gcd, self.bezout = [], ([], [])
        else:
            c = lc(self.A0)
            self.gcd = monic(self.A0)
            self.bezout = ([Fraction(1, 1) / c], [])
        self._unswap()
        self.status = STATUS_COMPLETED

    def _finish(self) -> None:
        if not self._r_cur:
            # last nonzero remainder is r_prev
            c = lc(self._r_prev)
            inv = Fraction(1, 1) / c
            self.gcd = scale(self._r_prev, inv)
            self.bezout = (scale(self._s_prev, inv), scale(self._t_prev, inv))
        else:
            # r_cur is a nonzero constant: gcd is 1
            c = lc(self._r_cur)
            inv = Fraction(1, 1) / c
            self.gcd = [Fraction(1)]
            self.bezout = (scale(self._s_cur, inv), scale(self._t_cur, inv))
        self._unswap()
        self.status = STATUS_COMPLETED

    def _unswap(self) -> None:
        """Re-orient Bezout coefficients to the caller's original (a, b)."""
        if self._swapped and self.bezout is not None:
            self.bezout = (self.bezout[1], self.bezout[0])

    # -- budget ------------------------------------------------------------

    def _state_size(self) -> int:
        return _coeff_size(self._r_prev, self._r_cur,
                           self._s_prev, self._s_cur,
                           self._t_prev, self._t_cur)

    def _budget_ok(self) -> bool:
        return self.budget is None or self._state_size() <= self.budget

    # -- stepping ----------------------------------------------------------

    def step(self) -> bool:
        """Perform one PRS transition.  Returns True if a step was taken."""
        if self.status == STATUS_PAUSED:
            self.status = STATUS_RUNNING
        if self.status == STATUS_BUDGET_EXCEEDED and self._budget_ok():
            self.status = STATUS_RUNNING   # budget was raised: resume
        if self.status != STATUS_RUNNING:
            return False
        if not self._budget_ok():
            self.status = STATUS_BUDGET_EXCEEDED
            return False
        if not self._r_cur or deg(self._r_cur) <= 0:
            self._finish()
            return False

        r_prev, r_cur = self._r_prev, self._r_cur
        delta = deg(r_prev) - deg(r_cur)
        q, prem, lead_pow = pseudo_divmod(r_prev, r_cur)
        if self._first:
            beta = Fraction((-1) ** (delta + 1))
        else:
            beta = -self._g * self._h ** delta
        r_next = scale(prem, Fraction(1, 1) / beta)

        # Bezout propagation: prem = lead_pow*r_prev - q*r_cur (with the
        # exact factor returned by pseudo_divmod), and r_next = prem/beta,
        # so the same affine update applies to the certificates s, t.
        inv_beta = Fraction(1, 1) / beta
        s_next = scale(sub(scale(self._s_prev, lead_pow),
                           mul(q, self._s_cur)), inv_beta)
        t_next = scale(sub(scale(self._t_prev, lead_pow),
                           mul(q, self._t_cur)), inv_beta)

        note = (
            f"pseudo-division by lc^(delta+1) with delta={delta}: ring "
            f"operations only, valid over any integral domain (here Q[x]); "
            f"division by subresultant factor beta={beta}: exact over the "
            f"field Q, controls coefficient/denominator growth"
        )
        self.steps.append(PRSStep(
            index=len(self.steps),
            dividend=r_prev, divisor=r_cur,
            pseudo_quotient=q, pseudo_remainder=prem,
            beta=beta, remainder=r_next,
            bezout_s=s_next, bezout_t=t_next, note=note,
        ))

        # advance loop state
        self._r_prev, self._r_cur = r_cur, r_next
        self._s_prev, self._s_cur = self._s_cur, s_next
        self._t_prev, self._t_cur = self._t_cur, t_next
        self._g = lc(self._r_prev)
        self._h = self._g ** delta / self._h ** (delta - 1) if delta >= 1 else self._h
        self._first = False

        if not self._budget_ok():
            self.status = STATUS_BUDGET_EXCEEDED
        return True

    def run(self, max_steps: int | None = None) -> str:
        """Run until completion, budget exhaustion, or max_steps pauses."""
        taken = 0
        while self.status in (STATUS_RUNNING, STATUS_PAUSED,
                              STATUS_BUDGET_EXCEEDED):
            if max_steps is not None and taken >= max_steps:
                if self.status == STATUS_RUNNING:
                    self.status = STATUS_PAUSED
                break
            if not self.step():
                break
            taken += 1
        return self.status

    # -- checkpoint / restore ----------------------------------------------

    def checkpoint(self) -> dict:
        """JSON-serialisable snapshot of the full machine state."""
        def p2s(p):
            return [str(c) for c in p]
        return {
            "A0": p2s(self.A0), "B0": p2s(self.B0),
            "budget": self.budget, "status": self.status,
            "r_prev": p2s(self._r_prev), "r_cur": p2s(self._r_cur),
            "s_prev": p2s(self._s_prev), "s_cur": p2s(self._s_cur),
            "t_prev": p2s(self._t_prev), "t_cur": p2s(self._t_cur),
            "g": str(self._g), "h": str(self._h),
            "first": self._first, "swapped": self._swapped,
            "steps": [
                {
                    "index": st.index,
                    "dividend": p2s(st.dividend), "divisor": p2s(st.divisor),
                    "pseudo_quotient": p2s(st.pseudo_quotient),
                    "pseudo_remainder": p2s(st.pseudo_remainder),
                    "beta": str(st.beta), "remainder": p2s(st.remainder),
                    "bezout_s": p2s(st.bezout_s), "bezout_t": p2s(st.bezout_t),
                    "note": st.note,
                }
                for st in self.steps
            ],
            "gcd": p2s(self.gcd) if self.gcd is not None else None,
            "bezout": [p2s(self.bezout[0]), p2s(self.bezout[1])]
                      if self.bezout is not None else None,
        }

    @classmethod
    def restore(cls, state: dict) -> "PRSComputer":
        def s2p(lst):
            return [Fraction(c) for c in lst]
        obj = cls.__new__(cls)
        obj.A0, obj.B0 = s2p(state["A0"]), s2p(state["B0"])
        obj.budget = state["budget"]
        obj.status = state["status"]
        obj._r_prev, obj._r_cur = s2p(state["r_prev"]), s2p(state["r_cur"])
        obj._s_prev, obj._s_cur = s2p(state["s_prev"]), s2p(state["s_cur"])
        obj._t_prev, obj._t_cur = s2p(state["t_prev"]), s2p(state["t_cur"])
        obj._g, obj._h = Fraction(state["g"]), Fraction(state["h"])
        obj._first = state["first"]
        obj._swapped = state["swapped"]
        obj.steps = [
            PRSStep(
                index=d["index"],
                dividend=s2p(d["dividend"]), divisor=s2p(d["divisor"]),
                pseudo_quotient=s2p(d["pseudo_quotient"]),
                pseudo_remainder=s2p(d["pseudo_remainder"]),
                beta=Fraction(d["beta"]), remainder=s2p(d["remainder"]),
                bezout_s=s2p(d["bezout_s"]), bezout_t=s2p(d["bezout_t"]),
                note=d["note"],
            )
            for d in state["steps"]
        ]
        obj.gcd = s2p(state["gcd"]) if state["gcd"] is not None else None
        obj.bezout = (tuple(s2p(p) for p in state["bezout"])
                      if state["bezout"] is not None else None)
        return obj
