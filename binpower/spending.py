"""Group-sequential designs with pre-declared error spending.

The alpha-spending function is declared BEFORE any data is seen; boundaries
are then fixed by exact recursion over the continuation distribution under
H0.  Simulation never adapts the rule -- it only records which pre-declared
stopping condition fired.  Exact size/power of the whole design are computed
by the same recursion, labeled ``kind="exact"``.
"""

from __future__ import annotations

from bisect import bisect_left
from dataclasses import dataclass
from math import sqrt
from statistics import NormalDist
from typing import Callable

from .exact import binomial_cdf_table, binomial_pmf_table
from .interval import clopper_pearson
from .simulate import StreamHandle

__all__ = [
    "obrien_fleming_spending",
    "power_spending",
    "LookBoundary",
    "GroupSequentialDesign",
    "group_sequential_design",
    "group_sequential_power",
    "GroupSequentialEstimate",
    "simulate_group_sequential",
]


def obrien_fleming_spending(alpha: float) -> Callable[[float], float]:
    """Pre-declared O'Brien-Fleming alpha-spending function alpha*(t)."""
    if not 0.0 < alpha < 1.0:
        raise ValueError("alpha must be in (0, 1)")
    nd = NormalDist()
    z = nd.inv_cdf(1.0 - alpha / 2.0)

    def spend(t: float) -> float:
        if t <= 0.0:
            return 0.0
        if t >= 1.0:
            return alpha
        return 2.0 * (1.0 - nd.cdf(z / sqrt(t)))

    return spend


def power_spending(alpha: float, rho: float = 2.0) -> Callable[[float], float]:
    """Pre-declared power-family spending alpha*(t) = alpha * t**rho."""
    if not 0.0 < alpha < 1.0:
        raise ValueError("alpha must be in (0, 1)")
    if rho <= 0.0:
        raise ValueError("rho must be positive")

    def spend(t: float) -> float:
        if t <= 0.0:
            return 0.0
        if t >= 1.0:
            return alpha
        return alpha * t**rho

    return spend


@dataclass(frozen=True)
class LookBoundary:
    """Fixed rejection boundary at one pre-declared look."""

    look: int
    n: int                    # cumulative sample size at this look
    critical: int             # reject & stop iff cumulative count >= critical
    alpha_budget_cum: float   # pre-declared cumulative budget spending(t)
    alpha_spent_cum: float    # exact cumulative crossing probability under H0


@dataclass(frozen=True)
class GroupSequentialDesign:
    """A fixed sequence of look boundaries; ``size`` is exact under H0."""

    p0: float
    alpha: float
    looks: tuple[LookBoundary, ...]
    size: float
    kind: str = "exact"


def _continuation_crossing(
    cont: dict[int, float], n_prev: int, n_cur: int, p: float
) -> list[float]:
    """Convolve the continuation distribution with the next-stage increment."""
    increment = binomial_pmf_table(n_cur - n_prev, p)
    new = [0.0] * (n_cur + 1)
    for s, prob in cont.items():
        if prob == 0.0:
            continue
        for j, pj in enumerate(increment):
            if pj:
                new[s + j] += prob * pj
    return new


def group_sequential_design(
    n_looks: list[int],
    p0: float,
    alpha: float,
    spending: Callable[[float], float],
) -> GroupSequentialDesign:
    """Fixed boundaries from a pre-declared error-spending function.

    At look ``k`` the boundary ``u_k`` is the smallest count whose exact
    cumulative crossing probability under H0 stays within the pre-declared
    budget ``spending(n_k / n_K)``.  ``u_k = n_k + 1`` means rejection is
    impossible at that look (budget too small to spend).
    """
    if not n_looks or any(n < 1 for n in n_looks):
        raise ValueError("n_looks must be non-empty with positive sizes")
    if sorted(n_looks) != list(n_looks) or len(set(n_looks)) != len(n_looks):
        raise ValueError("look sample sizes must be strictly increasing")
    if not 0.0 < alpha < 1.0:
        raise ValueError("alpha must be in (0, 1)")
    n_max = n_looks[-1]
    cont = {0: 1.0}
    spent = 0.0
    boundaries: list[LookBoundary] = []
    n_prev = 0
    for k, n_k in enumerate(n_looks, start=1):
        new = _continuation_crossing(cont, n_prev, n_k, p0)
        # cros[u] = P(S_k >= u and not stopped before look k) under H0.
        cros = [0.0] * (n_k + 2)
        running = 0.0
        for s in range(n_k, -1, -1):
            running += new[s]
            cros[s] = running
        budget = spending(n_k / n_max)
        u = n_k + 1
        while u - 1 >= 1 and spent + cros[u - 1] <= budget + 1e-15:
            u -= 1
        spent += cros[u]
        boundaries.append(
            LookBoundary(
                look=k,
                n=n_k,
                critical=u,
                alpha_budget_cum=budget,
                alpha_spent_cum=spent,
            )
        )
        cont = {s: new[s] for s in range(min(u, n_k + 1)) if new[s] > 0.0}
        n_prev = n_k
    return GroupSequentialDesign(p0=p0, alpha=alpha, looks=tuple(boundaries), size=spent)


def group_sequential_power(design: GroupSequentialDesign, p: float) -> float:
    """Exact P_p(reject) of the fixed boundary sequence (kind="exact")."""
    cont = {0: 1.0}
    n_prev = 0
    total = 0.0
    for b in design.looks:
        new = _continuation_crossing(cont, n_prev, b.n, p)
        if b.critical <= b.n:
            total += sum(new[b.critical:])
        cont = {s: new[s] for s in range(min(b.critical, b.n + 1)) if new[s] > 0.0}
        n_prev = b.n
    return min(total, 1.0)


@dataclass(frozen=True)
class GroupSequentialEstimate:
    """Empirical rejection rate of a group-sequential design (NOT exact).

    ``stopping_conditions`` counts how often each pre-declared stopping
    condition fired.  ``status == "interrupted"`` means the trial budget ran
    out; the interval is then the pending (undecided) range.
    """

    p: float
    trials: int
    rejections: int
    rate: float
    ci_low: float
    ci_high: float
    ci_alpha: float
    seed: int
    stopping_conditions: tuple[tuple[str, int], ...]
    exact_power: float
    exact_size: float
    status: str = "completed"  # "completed" | "interrupted"
    kind: str = "empirical"


def simulate_group_sequential(
    design: GroupSequentialDesign,
    *,
    p: float,
    trials: int,
    stream: StreamHandle,
    ci_alpha: float = 0.05,
    trial_budget: int | None = None,
) -> GroupSequentialEstimate:
    """Simulate the FIXED boundary sequence; only stopping times are random."""
    if trials < 1:
        raise ValueError("trials must be >= 1: zero-trial estimates are refused")
    budget = trials if trial_budget is None else max(0, min(trials, trial_budget))
    if budget < 1:
        raise ValueError("trial_budget leaves zero trials: estimates are refused")
    cdfs: dict[int, list[float]] = {}
    n_prev = 0
    for b in design.looks:
        step = b.n - n_prev
        cdfs[b.look] = binomial_cdf_table(step, p)
        n_prev = b.n
    counts: dict[str, int] = {}
    rejections = 0
    for _ in range(budget):
        total = 0
        condition = ""
        for b in design.looks:
            total += bisect_left(cdfs[b.look], stream.rng.random())
            if total >= b.critical:
                condition = f"reject at look {b.look} (S >= u={b.critical})"
                break
        else:
            last = design.looks[-1]
            condition = (
                f"accept at final look {last.look} (S < u={last.critical})"
            )
        counts[condition] = counts.get(condition, 0) + 1
        if condition.startswith("reject"):
            rejections += 1
    lo, hi = clopper_pearson(rejections, budget, ci_alpha)
    return GroupSequentialEstimate(
        p=p,
        trials=budget,
        rejections=rejections,
        rate=rejections / budget,
        ci_low=lo,
        ci_high=hi,
        ci_alpha=ci_alpha,
        seed=stream.seed,
        stopping_conditions=tuple(sorted(counts.items())),
        exact_power=group_sequential_power(design, p),
        exact_size=design.size,
        status="completed" if budget == trials else "interrupted",
    )
