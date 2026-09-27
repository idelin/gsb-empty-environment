"""Binomial sequential probability ratio test (SPRT) with exact and
simulation-based operating characteristics.

Conventions
-----------
H0: p = p0,  H1: p = p1,  p0 != p1.
The log-likelihood ratio accumulates log(P(x | H1) / P(x | H0)):

    accept H1  when  log_lr >= log((1 - beta) / alpha)
    accept H0  when  log_lr <= log(beta / (1 - alpha))
    undecided  when  n_max is reached without crossing a boundary

Boundaries are compared exactly (>= / <= on the accumulated float).
No epsilon shifts are applied anywhere in this module.

Zero-likelihood outcomes (p0 or p1 equal to 0 or 1) are handled
exactly: an outcome with zero probability under Hj and positive
probability under the other hypothesis decides immediately in favour
of the other hypothesis.  No epsilon smoothing is used.

Scope of guarantees
-------------------
The (alpha, beta) error guarantees and the e-value coverage of the
evidence process hold ONLY under the declared stopping rule:
stop at a boundary crossing, at n_max (undecided), or at shared
budget exhaustion.  Selective stopping outside the declared rule
(for example peeking and stopping ad hoc) invalidates the
guarantees.  Decisions come only from the boundary rule; a
fixed-sample p-value is never a substitute for the rule.
"""

from __future__ import annotations

import math
from dataclasses import dataclass
from enum import Enum


class Decision(Enum):
    CONTINUE = "continue"
    ACCEPT_H0 = "accept_h0"
    ACCEPT_H1 = "accept_h1"
    UNDECIDED = "undecided"                # n_max reached, no crossing
    BUDGET_EXHAUSTED = "budget_exhausted"  # shared budget ran out (resumable)


TERMINAL = (Decision.ACCEPT_H0, Decision.ACCEPT_H1, Decision.UNDECIDED)


class HypothesisError(ValueError):
    """Invalid hypothesis or error-spending configuration."""


def _log_ratio(num: float, den: float) -> float:
    """log(num / den) with exact 0/inf handling (no epsilon)."""
    if num == den:
        return 0.0
    if den == 0.0:
        return math.inf
    if num == 0.0:
        return -math.inf
    return math.log(num / den)


def decide_from_log_lr(log_lr: float, log_a: float, log_b: float) -> Decision:
    """Boundary rule, exact comparison, no epsilon."""
    if log_lr >= log_a:
        return Decision.ACCEPT_H1
    if log_lr <= log_b:
        return Decision.ACCEPT_H0
    return Decision.CONTINUE


class SharedBudget:
    """Stopping budget shared by multiple streams.

    A unit of budget pays for exactly one observation across all
    attached streams.  A failed ``try_consume`` leaves the stream
    state untouched, so an interrupted stream resumes without
    double-counting any sample.
    """

    def __init__(self, total: int):
        if total < 0:
            raise ValueError("budget must be non-negative")
        self.remaining = total
        self.consumed = 0

    def try_consume(self, k: int = 1) -> bool:
        if k > self.remaining:
            return False
        self.remaining -= k
        self.consumed += k
        return True

    def refill(self, k: int) -> None:
        if k < 0:
            raise ValueError("refill must be non-negative")
        self.remaining += k


class BinomialSPRT:
    """One sequential stream testing H0: p = p0 against H1: p = p1."""

    def __init__(self, p0, p1, alpha, beta, n_max, budget=None, label=None):
        if not (0.0 <= p0 <= 1.0 and 0.0 <= p1 <= 1.0):
            raise HypothesisError("p0 and p1 must lie in [0, 1]")
        if p0 == p1:
            raise HypothesisError("p0 and p1 must differ")
        if not (0.0 < alpha < 1.0 and 0.0 < beta < 1.0):
            raise HypothesisError("alpha and beta must lie in (0, 1)")
        if alpha + beta >= 1.0:
            raise HypothesisError("require alpha + beta < 1")
        if n_max < 1:
            raise HypothesisError("n_max must be >= 1")
        self.p0, self.p1 = p0, p1
        self.alpha, self.beta = alpha, beta
        self.n_max = n_max
        self.budget = budget
        self.label = label or "stream"
        # Wald bounds, on the log scale.
        self.log_a = math.log((1.0 - beta) / alpha)
        self.log_b = math.log(beta / (1.0 - alpha))
        # Per-observation log-LR increments; +-inf encodes zero likelihood.
        self._inc1 = _log_ratio(p1, p0)           # success
        self._inc0 = _log_ratio(1.0 - p1, 1.0 - p0)  # failure
        # Mutable state.
        self.n = 0
        self.k = 0              # successes
        self.log_lr = 0.0
        self.status = Decision.CONTINUE

    def step(self, x: int) -> Decision:
        """Feed one observation (1 = success, 0 = failure)."""
        if x not in (0, 1):
            raise ValueError("observation must be 0 or 1")
        if self.status in TERMINAL:
            return self.status
        if self.budget is not None and not self.budget.try_consume(1):
            # Budget interruption: state untouched, nothing counted.
            return Decision.BUDGET_EXHAUSTED
        self.n += 1
        if x == 1:
            self.k += 1
            self.log_lr += self._inc1
        else:
            self.log_lr += self._inc0
        self.status = decide_from_log_lr(self.log_lr, self.log_a, self.log_b)
        if self.status is Decision.CONTINUE and self.n >= self.n_max:
            self.status = Decision.UNDECIDED
        return self.status

    def snapshot(self) -> dict:
        """Checkpoint the full state (log_lr stored, not recomputed)."""
        return {
            "p0": self.p0, "p1": self.p1,
            "alpha": self.alpha, "beta": self.beta,
            "n_max": self.n_max, "label": self.label,
            "n": self.n, "k": self.k,
            "log_lr": self.log_lr, "status": self.status.name,
        }

    @classmethod
    def from_snapshot(cls, snap: dict, budget=None) -> "BinomialSPRT":
        obj = cls(snap["p0"], snap["p1"], snap["alpha"], snap["beta"],
                  snap["n_max"], budget=budget, label=snap["label"])
        obj.n = snap["n"]
        obj.k = snap["k"]
        obj.log_lr = snap["log_lr"]  # restored verbatim: resume is exact
        obj.status = Decision[snap["status"]]
        return obj


class EvidenceProcess:
    """Re-queryable evidence view over a stream.

    The likelihood ratio exp(log_lr) is an e-value under H0 (Ville's
    inequality): P_H0(sup_t LR_t >= 1/alpha) <= alpha.  This coverage
    holds only while stopping follows the declared rule of the
    underlying stream; ad-hoc selective stopping voids it.
    """

    GUARANTEE = ("coverage valid only under the declared stopping rule "
                 "(boundary crossing, n_max, or shared budget exhaustion)")

    def __init__(self, stream: BinomialSPRT):
        self.stream = stream

    def query(self) -> dict:
        s = self.stream
        e_value = math.exp(s.log_lr) if math.isfinite(s.log_lr) else (
            math.inf if s.log_lr > 0 else 0.0)
        return {
            "label": s.label,
            "n": s.n,
            "successes": s.k,
            "log_lr": s.log_lr,
            "e_value": e_value,
            "decision": s.status.name,
            "guarantee": self.GUARANTEE,
        }


# ---------------------------------------------------------------------------
# Exact small-sample recursion (discrete lattice over (n, k)).
# ---------------------------------------------------------------------------

@dataclass
class ExactResult:
    p: float
    p_accept_h0: float
    p_accept_h1: float
    p_undecided: float
    expected_n: float
    label: str = "exact (discrete recursion)"

    @property
    def type1_error(self):  # P(accept H1 | p = p0) when evaluated at p0
        return self.p_accept_h1

    @property
    def type2_error(self):  # P(accept H0 | p = p1) when evaluated at p1
        return self.p_accept_h0


def exact_operating_characteristics(p0, p1, alpha, beta, n_max, p) -> ExactResult:
    """Exact error probabilities and E[stopping time] under Bernoulli(p).

    Dynamic programming over the (n, k) lattice; boundary states are
    absorbing.  Works also for p in {0, 1} (degenerate binomial).
    """
    probe = BinomialSPRT(p0, p1, alpha, beta, n_max)
    log_a, log_b = probe.log_a, probe.log_b
    inc1, inc0 = probe._inc1, probe._inc0

    # frontier: k -> [probability, log_lr] for states not yet stopped.
    frontier = {0: [1.0, 0.0]}
    acc = {Decision.ACCEPT_H0: 0.0, Decision.ACCEPT_H1: 0.0,
           Decision.UNDECIDED: 0.0}
    expected_n = 0.0

    for n in range(1, n_max + 1):
        nxt = {}
        for k, (prob, llr) in frontier.items():
            for dk, trans_p, inc in ((1, p, inc1), (0, 1.0 - p, inc0)):
                if trans_p == 0.0:
                    continue
                nk, np_, nllr = k + dk, prob * trans_p, llr + inc
                d = decide_from_log_lr(nllr, log_a, log_b)
                if d is Decision.CONTINUE and n >= n_max:
                    d = Decision.UNDECIDED
                if d is Decision.CONTINUE:
                    slot = nxt.setdefault(nk, [0.0, nllr])
                    slot[0] += np_
                else:
                    acc[d] += np_
                    expected_n += n * np_
        frontier = nxt
        if not frontier:
            break
    # Any leftover frontier mass is undecided at n_max.
    for prob, _ in frontier.values():
        acc[Decision.UNDECIDED] += prob
        expected_n += n_max * prob

    return ExactResult(p=p,
                       p_accept_h0=acc[Decision.ACCEPT_H0],
                       p_accept_h1=acc[Decision.ACCEPT_H1],
                       p_undecided=acc[Decision.UNDECIDED],
                       expected_n=expected_n)


# ---------------------------------------------------------------------------
# Monte Carlo (random source injected by the caller).
# ---------------------------------------------------------------------------

@dataclass
class SimulationResult:
    p: float
    n_rep: int
    seed: object
    total_samples: int
    p_accept_h0: float
    p_accept_h1: float
    p_undecided: float
    expected_n: float
    label: str = "empirical (Monte Carlo)"


def simulate(p0, p1, alpha, beta, n_max, p, n_rep, rng, seed=None,
             budget=None) -> SimulationResult:
    """Estimate operating characteristics with a caller-injected RNG.

    ``rng`` must provide ``random()`` (e.g. random.Random(seed)).
    The seed and the total number of samples drawn are recorded.
    """
    c_h0 = c_h1 = c_und = 0
    total_n = 0
    for _ in range(n_rep):
        s = BinomialSPRT(p0, p1, alpha, beta, n_max, budget=budget)
        while True:
            d = s.step(1 if rng.random() < p else 0)
            if d is Decision.BUDGET_EXHAUSTED:
                break
            if d in TERMINAL:
                break
        total_n += s.n
        if s.status is Decision.ACCEPT_H0:
            c_h0 += 1
        elif s.status is Decision.ACCEPT_H1:
            c_h1 += 1
        else:
            c_und += 1
    return SimulationResult(
        p=p, n_rep=n_rep, seed=seed, total_samples=total_n,
        p_accept_h0=c_h0 / n_rep, p_accept_h1=c_h1 / n_rep,
        p_undecided=c_und / n_rep, expected_n=total_n / n_rep)
