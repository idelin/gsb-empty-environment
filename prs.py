"""Subresultant polynomial remainder sequence (PRS) over Q[x].

Features:
  - denominator/coefficient growth control via exact division by the
    subresultant beta factors (over Q every nonzero scalar division is
    exact; over Z this keeps coefficients at polynomial growth);
  - handles non-monic inputs, multiple common factors, and zero inputs;
  - every step records pseudo-quotient, pseudo-remainder, the growth
    control factor beta, the next remainder, and an exact Bezout
    certificate (s, t) with s*F + t*G == remainder;
  - resumable state machine with pause/resume and serializable snapshots;
  - optional coefficient budget: exceeding it leaves the computation in
    the unfinished "budget_exceeded" state, resumable with a new budget.
"""
from __future__ import annotations

from fractions import Fraction

from polygcd import (add, as_poly, deg, lc, monic, mul, scalar_div, scale,
                     shift, sub, trim)


class BudgetExceeded(Exception):
    """Raised when a coefficient exceeds the configured budget.

    The computation keeps its (unfinished) state and may be resumed
    with a larger budget."""

    def __init__(self, cost, limit):
        super().__init__(f"coefficient cost {cost} exceeds budget {limit}")
        self.cost = cost
        self.limit = limit


class CoefficientBudget:
    """Budget on coefficient size.

    Cost of a polynomial = max over its coefficients of
    digits(|numerator|) + digits(denominator)."""

    def __init__(self, max_digits):
        self.max_digits = int(max_digits)

    def cost(self, p):
        worst = 0
        for c in p:
            worst = max(worst, len(str(abs(c.numerator))) + len(str(c.denominator)))
        return worst

    def check(self, *polys):
        for p in polys:
            c = self.cost(p)
            if c > self.max_digits:
                raise BudgetExceeded(c, self.max_digits)


def pseudo_divmod(f, g):
    """Pseudo-division in Q[x] (Q[x] is an integral domain).

    Returns (q, r, mult) with mult * f == q*g + r and deg r < deg g,
    where mult = lc(g) ** (deg f - deg g + 1) (mult = 1 if deg f < deg g).
    Raises ZeroDivisionError if g is the zero polynomial.
    """
    f = as_poly(f)
    g = as_poly(g)
    if not g:
        raise ZeroDivisionError("pseudo-division by the zero polynomial")
    delta = deg(f) - deg(g)
    if delta < 0:
        return [], f, Fraction(1)
    b = lc(g)
    q = []
    r = list(f)
    e = delta + 1
    while r and deg(r) >= deg(g):
        d = deg(r) - deg(g)
        s = shift([lc(r)], d)
        q = add(scale(q, b), s)
        r = trim(sub(scale(r, b), mul(s, g)))
        e -= 1
    if e:
        q = scale(q, b ** e)
        r = scale(r, b ** e)
    return q, r, b ** (delta + 1)


class PRSComputation:
    """Resumable subresultant-PRS gcd computation over Q[x].

    Lifecycle: pending -> running -> {paused, budget_exceeded, done}.
    run(max_steps=n) pauses after n steps; resume() continues;
    snapshot()/from_snapshot() serialize the exact resumable state.
    When done, self.result = (d, s, t) with d monic and s*f + t*g == d.
    """

    PENDING = "pending"
    RUNNING = "running"
    PAUSED = "paused"
    BUDGET_EXCEEDED = "budget_exceeded"
    DONE = "done"

    def __init__(self, f, g, budget=None):
        self.f = as_poly(f)
        self.g = as_poly(g)
        self.budget = budget
        self.status = self.PENDING
        self.steps = []
        self.result = None
        self._budget_error = None
        self._R = []
        self._S = []
        self._T = []
        self._delta = 0
        self._psi = Fraction(-1)
        self._beta = Fraction(1)
        self._setup()

    def _setup(self):
        f, g = self.f, self.g
        if not f and not g:
            self.steps.append({
                "kind": "degenerate",
                "note": "gcd(0, 0) = 0 by convention; Bezout 0*f + 0*g == 0",
                "domain": "Q[x]",
            })
            self.result = ([], [], [])
            self.status = self.DONE
            return
        if not g:
            c = lc(f)
            self.steps.append({
                "kind": "degenerate",
                "note": "g == 0: gcd(f, 0) = monic(f)",
                "domain": "Q[x]; exact scaling by 1/lc(f) in Q (field)",
            })
            self.result = (monic(f), [Fraction(1) / c], [])
            self.status = self.DONE
            return
        if not f:
            c = lc(g)
            self.steps.append({
                "kind": "degenerate",
                "note": "f == 0: gcd(0, g) = monic(g)",
                "domain": "Q[x]; exact scaling by 1/lc(g) in Q (field)",
            })
            self.result = (monic(g), [], [Fraction(1) / c])
            self.status = self.DONE
            return
        # Order so that deg R0 >= deg R1; cofactors always refer to the
        # ORIGINAL (f, g), so a swap is reflected in the initial basis.
        if deg(f) >= deg(g):
            self._R = [f, g]
            self._S = [[Fraction(1)], []]
            self._T = [[], [Fraction(1)]]
        else:
            self._R = [g, f]
            self._S = [[], [Fraction(1)]]
            self._T = [[Fraction(1)], []]
        self._delta = deg(self._R[0]) - deg(self._R[1])
        self._psi = Fraction(-1)
        self._beta = Fraction((-1) ** (self._delta + 1))

    @property
    def remainders(self):
        """The current remainder sequence R0, R1, ... (copies)."""
        return [list(p) for p in self._R]

    def step(self):
        """Advance one PRS step. Returns True if more steps remain."""
        if self.status == self.DONE:
            return False
        if self.status == self.BUDGET_EXCEEDED:
            raise self._budget_error or BudgetExceeded(0, 0)
        self.status = self.RUNNING
        A, B = self._R[-2], self._R[-1]
        sa, sb = self._S[-2], self._S[-1]
        ta, tb = self._T[-2], self._T[-1]
        q, r, mult = pseudo_divmod(A, B)
        if not r:
            self._finalize()
            return False
        beta = self._beta
        nxt = scalar_div(r, beta)
        # Bezout update: mult*A = q*B + r  ==>
        #   r = (mult*sa - q*sb)*F + (mult*ta - q*tb)*G, then divide by beta.
        s_new = scalar_div(sub(scale(sa, mult), mul(q, sb)), beta)
        t_new = scalar_div(sub(scale(ta, mult), mul(q, tb)), beta)
        if self.budget is not None:
            try:
                self.budget.check(nxt, s_new, t_new)
            except BudgetExceeded as exc:
                # Keep the unfinished state; nothing is committed.
                self._budget_error = exc
                self.status = self.BUDGET_EXCEEDED
                raise
        self._R.append(nxt)
        self._S.append(s_new)
        self._T.append(t_new)
        # Subresultant recursion (growth control), exact in Q:
        #   psi' = (-lc(B))^delta / psi^(delta-1);  beta' = -lc(B)*psi'^delta'
        delta_new = deg(B) - deg(nxt)
        psi_new = ((-lc(B)) ** self._delta) / (self._psi ** (self._delta - 1))
        beta_new = -lc(B) * (psi_new ** delta_new)
        self._delta, self._psi, self._beta = delta_new, psi_new, beta_new
        self.steps.append({
            "kind": "prs-step",
            "index": len(self._R) - 1,
            "dividend": A,
            "divisor": B,
            "pseudo_quotient": q,
            "pseudo_remainder": r,
            "multiplier": mult,
            "beta": beta,
            "remainder": nxt,
            "bezout_s": s_new,
            "bezout_t": t_new,
            "domains": [
                "pseudo-division in Q[x] (integral domain): lc(B)^(d+1)*A = Q*B + R",
                "exact scalar division by beta in Q (field): subresultant growth control",
                "Bezout cofactor update: exact Q[x]-linear combination",
            ],
        })
        return True

    def _finalize(self):
        last = self._R[-1]
        c = lc(last)
        d = scalar_div(last, c)
        s = scalar_div(self._S[-1], c)
        t = scalar_div(self._T[-1], c)
        self.steps.append({
            "kind": "monic-normalization",
            "note": "gcd := last nonzero PRS remainder made monic; cofactors scaled likewise",
            "domain": "exact scalar division by lc in Q (field)",
        })
        self.result = (d, s, t)
        self.status = self.DONE

    def run(self, max_steps=None):
        """Run until done, paused (max_steps reached), or budget exceeded."""
        taken = 0
        while self.status != self.DONE:
            if max_steps is not None and taken >= max_steps:
                self.status = self.PAUSED
                break
            try:
                more = self.step()
            except BudgetExceeded:
                break
            taken += 1
            if not more:
                break
        return self.status

    def resume(self, max_steps=None, budget=None):
        """Continue a paused / budget-exceeded / pending computation.

        A larger budget may be supplied to escape the budget_exceeded
        state; the unfinished state is otherwise preserved as-is."""
        if budget is not None:
            self.budget = budget
        if self.status in (self.PAUSED, self.BUDGET_EXCEEDED,
                           self.PENDING, self.RUNNING):
            self.status = self.PENDING
        return self.run(max_steps=max_steps)

    def snapshot(self):
        """JSON-serializable snapshot of the exact resumable state."""
        def enc(p):
            return [str(c) for c in p]
        return {
            "status": self.status,
            "f": enc(self.f),
            "g": enc(self.g),
            "R": [enc(p) for p in self._R],
            "S": [enc(p) for p in self._S],
            "T": [enc(p) for p in self._T],
            "delta": self._delta,
            "psi": str(self._psi),
            "beta": str(self._beta),
            "result": None if self.result is None else [enc(p) for p in self.result],
            "budget": None if self.budget is None else self.budget.max_digits,
        }

    @classmethod
    def from_snapshot(cls, snap):
        """Restore a computation from snapshot(); it can be resumed."""
        def dec(p):
            return [Fraction(c) for c in p]
        budget = None if snap["budget"] is None else CoefficientBudget(snap["budget"])
        obj = cls(dec(snap["f"]), dec(snap["g"]), budget=budget)
        obj.status = snap["status"]
        obj._R = [dec(p) for p in snap["R"]]
        obj._S = [dec(p) for p in snap["S"]]
        obj._T = [dec(p) for p in snap["T"]]
        obj._delta = snap["delta"]
        obj._psi = Fraction(snap["psi"])
        obj._beta = Fraction(snap["beta"])
        obj.result = None if snap["result"] is None else tuple(dec(p) for p in snap["result"])
        obj.steps = []
        return obj


def subresultant_gcd(f, g, budget=None):
    """Compute (d, s, t) via the subresultant PRS: d monic, s*f + t*g == d.

    Raises BudgetExceeded if the budget is exhausted (the computation
    stays unfinished and could be resumed via PRSComputation directly).
    """
    comp = PRSComputation(f, g, budget=budget)
    comp.run()
    if comp.status != PRSComputation.DONE:
        if comp._budget_error is not None:
            raise comp._budget_error
        raise RuntimeError(f"computation not finished: {comp.status}")
    return comp.result
