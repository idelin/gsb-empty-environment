"""Importance-sampling estimators with log-stabilized weights.

Estimators
----------
ordinary (un-normalized) IS
    mu_hat = (1/n) * sum_i w_i f(x_i),  w_i = p(x_i)/q(x_i).
    EXACT property: unbiased whenever the proposal covers the target
    (q(x) > 0 wherever p(x) > 0) and w*f has finite expectation.
    Requires a normalized target density.

self-normalized IS (SNIS)
    mu_tilde = sum_i w_i f(x_i) / sum_i w_i.
    EXACT property: biased for every finite n in general (bias is O(1/n)),
    but consistent as n -> infinity; often has lower mean-squared error
    than the ordinary estimator.  Works with an unnormalized target.

All weight arithmetic is done in the log domain with a max-shift before
exponentiation, so weights whose logs are hundreds of nats apart do not
overflow or underflow.
"""

from __future__ import annotations

import math
from dataclasses import dataclass, field
from typing import Any, Callable, Hashable, List, Optional, Sequence


@dataclass
class Diagnostic:
    """A structured finding about an importance-sampling run."""

    code: str
    severity: str  # "error" -> estimates unavailable; "warning" -> estimates usable but suspect
    message: str


@dataclass
class ISResult:
    """Outcome of an importance-sampling estimation.

    ``ordinary`` and ``self_normalized`` are ``None`` when ``available`` is
    False (e.g. zero coverage).  ``seed`` and ``n`` record the
    caller-supplied seed label and the number of samples used.
    """

    ordinary: Optional[float]
    self_normalized: Optional[float]
    ess: float
    n: int
    seed: Optional[int]
    diagnostics: List[Diagnostic] = field(default_factory=list)
    available: bool = True

    @property
    def errors(self) -> List[Diagnostic]:
        return [d for d in self.diagnostics if d.severity == "error"]

    @property
    def warnings(self) -> List[Diagnostic]:
        return [d for d in self.diagnostics if d.severity == "warning"]


def _pdf_of(dist: Any, x: Hashable) -> float:
    if hasattr(dist, "pdf"):
        return float(dist.pdf(x))
    return float(dist(x))


def _log_pdf_of(dist: Any, x: Hashable) -> float:
    if hasattr(dist, "log_pdf"):
        return float(dist.log_pdf(x))
    p = _pdf_of(dist, x)
    return math.log(p) if p > 0.0 else -math.inf


def compute_log_weights(target: Any, proposal: Any, xs: Sequence[Hashable]) -> List[float]:
    """log w_i = log p(x_i) - log q(x_i) for each sample."""
    return [_log_pdf_of(target, x) - _log_pdf_of(proposal, x) for x in xs]


def ordinary_is(log_weights: Sequence[float], f_values: Sequence[float]) -> float:
    """Ordinary IS estimate (1/n) * sum_i w_i f_i, computed stably."""
    n = len(log_weights)
    m = max(log_weights)
    shifted = sum(math.exp(lw - m) * fv for lw, fv in zip(log_weights, f_values))
    if shifted == 0.0:
        return 0.0
    # sign * exp(m + log|shifted|) avoids overflow when m is large.
    return math.copysign(math.exp(m + math.log(abs(shifted))), shifted) / n


def self_normalized_is(log_weights: Sequence[float], f_values: Sequence[float]) -> float:
    """Self-normalized IS estimate sum_i w_i f_i / sum_i w_i."""
    m = max(log_weights)
    numerator = 0.0
    denominator = 0.0
    for lw, fv in zip(log_weights, f_values):
        w = math.exp(lw - m)
        numerator += w * fv
        denominator += w
    return numerator / denominator


def effective_sample_size(log_weights: Sequence[float]) -> float:
    """ESS = (sum w)^2 / sum w^2.

    Equal weights give ESS == n exactly; one dominant weight gives ESS ~ 1.
    """
    m = max(log_weights)
    weights = [math.exp(lw - m) for lw in log_weights]
    total = sum(weights)
    total_sq = sum(w * w for w in weights)
    if total_sq == 0.0:
        return 0.0
    return total * total / total_sq


def diagnose_log_weights(log_weights: Sequence[float]) -> List[Diagnostic]:
    """Inspect computed log-weights for fatal numeric conditions."""
    diagnostics: List[Diagnostic] = []
    if any(math.isnan(lw) for lw in log_weights):
        diagnostics.append(
            Diagnostic("NAN_WEIGHT", "error", "NaN importance weight encountered (0/0 density ratio?)")
        )
    if any(lw == math.inf for lw in log_weights):
        diagnostics.append(
            Diagnostic(
                "INFINITE_WEIGHT",
                "error",
                "infinite importance weight: proposal density is zero where the "
                "target is positive; estimates are unavailable",
            )
        )
    if log_weights and max(log_weights) == -math.inf:
        diagnostics.append(
            Diagnostic(
                "ALL_WEIGHTS_ZERO",
                "error",
                "every sample has zero target density; all importance weights are zero",
            )
        )
    return diagnostics


def check_coverage(target: Any, proposal: Any) -> List[Diagnostic]:
    """Zero-coverage check for distributions that expose ``support()``.

    If q(x) == 0 while p(x) > 0 for some x, no importance-sampling
    estimator can recover the target expectation: report unavailable.
    """
    if not (hasattr(target, "support") and hasattr(proposal, "support")):
        return []
    uncovered = [x for x in target.support() if target.pdf(x) > 0.0 and _pdf_of(proposal, x) == 0.0]
    if uncovered:
        return [
            Diagnostic(
                "ZERO_COVERAGE",
                "error",
                f"proposal assigns zero probability where the target is positive "
                f"at {uncovered!r}; importance-sampling estimates are unavailable",
            )
        ]
    return []


def estimate(
    target: Any,
    proposal: Any,
    f: Callable[[Hashable], float],
    n: int,
    rng,
    *,
    seed: Optional[int] = None,
    ess_warn_fraction: float = 0.1,
) -> ISResult:
    """Draw ``n`` samples from ``proposal`` and estimate E_target[f].

    ``rng`` is a caller-injected ``random.Random`` (or compatible) source;
    ``seed`` is recorded in the result for traceability.
    """
    if n < 1:
        raise ValueError("n must be >= 1")
    diagnostics: List[Diagnostic] = []
    diagnostics.extend(check_coverage(target, proposal))

    xs = [proposal.sample(rng) for _ in range(n)]
    log_weights = compute_log_weights(target, proposal, xs)
    diagnostics.extend(diagnose_log_weights(log_weights))

    if any(d.severity == "error" for d in diagnostics):
        return ISResult(None, None, float("nan"), n, seed, diagnostics, available=False)

    f_values = [float(f(x)) for x in xs]
    ess = effective_sample_size(log_weights)
    if ess < ess_warn_fraction * n:
        diagnostics.append(
            Diagnostic(
                "LOW_ESS",
                "warning",
                f"effective sample size {ess:.1f} is below {ess_warn_fraction:.0%} "
                f"of n={n}; estimates may be unreliable",
            )
        )
    return ISResult(
        ordinary=ordinary_is(log_weights, f_values),
        self_normalized=self_normalized_is(log_weights, f_values),
        ess=ess,
        n=n,
        seed=seed,
        diagnostics=diagnostics,
        available=True,
    )
