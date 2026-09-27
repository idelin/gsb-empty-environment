"""Importance sampling: ordinary and self-normalized estimators with diagnostics.

Conventions
-----------
- [EXACT] marks conclusions derived analytically (no Monte Carlo error).
- [EMPIRICAL] marks conclusions from finite random samples; they carry Monte
  Carlo error and are only validated by simulation.

Estimator properties
--------------------
- Ordinary IS estimator  T_ord = (1/n) sum_i w_i f(x_i),  w_i = p(x_i)/q(x_i):
  [EXACT] unbiased for E_p[f] provided p is a *normalized* density and q
  covers p (q(x) > 0 wherever p(x) > 0).
- Self-normalized estimator  T_sn = sum_i w_i f(x_i) / sum_i w_i:
  works with an *unnormalized* target; [EXACT] biased for every finite n
  (it is a ratio of two random variables, and E[A/B] != E[A]/E[B] in
  general), but [EXACT] consistent: bias and variance vanish as n -> inf.
  This bias is stated honestly and demonstrated empirically in the tests.

Weight computation
------------------
All weights are computed in log space (log w = log p - log q) and stabilized
by subtracting the maximum log weight before exponentiation. ESS and the
self-normalized estimator are invariant to this shift; the ordinary estimator
is rescaled afterwards.

Randomness
----------
The random source is injected by the caller as a ``random.Random`` instance;
the seed and the sample size are recorded on every result for reproducibility.
"""

from __future__ import annotations

import json
import math
import random
from dataclasses import dataclass, field
from pathlib import Path
from typing import Callable, Iterable, Sequence

NEG_INF = float("-inf")
POS_INF = float("inf")

# Diagnostic codes
ZERO_COVERAGE = "ZERO_COVERAGE"
INFINITE_WEIGHT = "INFINITE_WEIGHT"
LOW_ESS = "LOW_ESS"
EMPTY_SAMPLE = "EMPTY_SAMPLE"
ALL_WEIGHTS_ZERO = "ALL_WEIGHTS_ZERO"


# ---------------------------------------------------------------------------
# Diagnostics and results
# ---------------------------------------------------------------------------

@dataclass(frozen=True)
class Diagnostic:
    code: str
    severity: str  # "error" | "warning"
    message: str


@dataclass
class ISResult:
    """Outcome of an importance-sampling estimation.

    ``ordinary`` / ``self_normalized`` / ``ess`` are ``None`` when an error
    diagnostic makes the estimates unavailable.
    """

    ordinary: float | None
    self_normalized: float | None
    ess: float | None
    n: int
    seed: int | None
    diagnostics: list[Diagnostic] = field(default_factory=list)

    @property
    def available(self) -> bool:
        return self.ordinary is not None and self.self_normalized is not None

    def errors(self) -> list[Diagnostic]:
        return [d for d in self.diagnostics if d.severity == "error"]

    def warnings(self) -> list[Diagnostic]:
        return [d for d in self.diagnostics if d.severity == "warning"]


# ---------------------------------------------------------------------------
# Log-space helpers
# ---------------------------------------------------------------------------

def logsumexp(log_values: Iterable[float]) -> float:
    vals = list(log_values)
    if not vals:
        return NEG_INF
    m = max(vals)
    if m == NEG_INF:
        return NEG_INF
    if m == POS_INF:
        return POS_INF
    return m + math.log(sum(math.exp(v - m) for v in vals))


def stabilize_log_weights(log_weights: Sequence[float]) -> tuple[list[float], float]:
    """Subtract the max log weight so ``exp`` is safe.

    Returns (shifted_log_weights, max_log_weight). ESS and the
    self-normalized estimator are invariant to the shift; the ordinary
    estimator must be rescaled by ``exp(max_log_weight)``.
    """
    m = max(log_weights)
    return [lw - m for lw in log_weights], m


# ---------------------------------------------------------------------------
# Core estimator
# ---------------------------------------------------------------------------

def estimate_from_log_weights(
    values: Sequence[float],
    log_weights: Sequence[float],
    *,
    seed: int | None = None,
    ess_fraction: float = 0.1,
) -> ISResult:
    """Compute ordinary / self-normalized estimates and ESS from log weights.

    Diagnostics:
    - EMPTY_SAMPLE: no draws at all (error).
    - INFINITE_WEIGHT: a weight is +inf/NaN, i.e. a sample with positive
      target density was generated under zero proposal density (error,
      estimates unavailable).
    - ALL_WEIGHTS_ZERO: every observed weight is zero; the draws carry no
      information about the target (error, estimates unavailable).
    - LOW_ESS: ESS below ``ess_fraction`` of n (warning).
    """
    n = len(values)
    if n == 0:
        return ISResult(
            None, None, None, 0, seed,
            [Diagnostic(EMPTY_SAMPLE, "error",
                        "No samples were drawn; estimates are unavailable.")],
        )

    if any(lw == POS_INF or math.isnan(lw) for lw in log_weights):
        return ISResult(
            None, None, None, n, seed,
            [Diagnostic(
                INFINITE_WEIGHT, "error",
                "At least one importance weight is infinite or NaN: a sample "
                "with positive target density has zero proposal density "
                "(proposal probability 0 while target is positive). "
                "Estimates are unavailable.")],
        )

    shifted, m = stabilize_log_weights(log_weights)
    if m == NEG_INF:
        return ISResult(
            None, None, None, n, seed,
            [Diagnostic(
                ALL_WEIGHTS_ZERO, "error",
                "All importance weights are zero: the observed samples carry "
                "no information about the target (empirical zero coverage). "
                "Estimates are unavailable.")],
        )

    w = [math.exp(lw) for lw in shifted]
    sw = sum(w)
    self_normalized = sum(wi * vi for wi, vi in zip(w, values)) / sw

    # Ordinary estimate: rescale by exp(m). Overflow means the true estimate
    # is genuinely astronomical; report inf with a warning instead of raising.
    diagnostics: list[Diagnostic] = []
    try:
        scale = math.exp(m)
        ordinary = scale * sum(wi * vi for wi, vi in zip(w, values)) / n
    except OverflowError:
        ordinary = POS_INF
        diagnostics.append(Diagnostic(
            INFINITE_WEIGHT, "warning",
            "Ordinary estimate overflowed after rescaling; reported as inf."))

    # ESS = (sum w)^2 / sum w^2; scale-invariant, so stabilized weights are fine.
    ess = sw * sw / sum(wi * wi for wi in w)
    if ess < ess_fraction * n:
        diagnostics.append(Diagnostic(
            LOW_ESS, "warning",
            f"Effective sample size {ess:.1f} is below {ess_fraction:.0%} of "
            f"n={n}: a few weights dominate and the estimates are unreliable."))

    return ISResult(ordinary, self_normalized, ess, n, seed, diagnostics)


def run_importance_sampling(
    n: int,
    target_logpdf: Callable[[float], float],
    proposal_logpdf: Callable[[float], float],
    proposal_sampler: Callable[[random.Random], float],
    f: Callable[[float], float],
    *,
    rng: random.Random,
    seed: int | None = None,
    ess_fraction: float = 0.1,
) -> ISResult:
    """Draw ``n`` samples from the proposal and estimate E_target[f].

    The random source ``rng`` is injected by the caller; ``seed`` is recorded
    on the result for bookkeeping. Weights use the proposal density supplied
    here, which must be the density the samples are actually drawn from.
    """
    samples = [proposal_sampler(rng) for _ in range(n)]
    log_weights = [_log_weight(target_logpdf, proposal_logpdf, x) for x in samples]
    values = [f(x) for x in samples]
    return estimate_from_log_weights(
        values, log_weights, seed=seed, ess_fraction=ess_fraction)


def _log_weight(target_logpdf, proposal_logpdf, x) -> float:
    lt = target_logpdf(x)
    lq = proposal_logpdf(x)
    if lq == NEG_INF:
        # Proposal zero. If the target is positive here the weight is
        # infinite -> surfaced as an INFINITE_WEIGHT error downstream.
        return POS_INF if lt > NEG_INF else NEG_INF
    return lt - lq


# ---------------------------------------------------------------------------
# Coverage diagnostics (finite discrete domains)
# ---------------------------------------------------------------------------

def check_discrete_coverage(
    domain: Iterable,
    target_pdf: Callable[[object], float],
    proposal_pdf: Callable[[object], float],
) -> Diagnostic | None:
    """[EXACT] Verify the proposal covers the target on a finite domain.

    Returns an error Diagnostic if any point has target probability > 0 while
    the proposal probability is 0; in that case importance sampling is
    unavailable (weights would be infinite and the estimators undefined).
    """
    missing = [x for x in domain
               if target_pdf(x) > 0.0 and proposal_pdf(x) <= 0.0]
    if missing:
        preview = list(missing[:5])
        return Diagnostic(
            ZERO_COVERAGE, "error",
            f"Proposal assigns zero probability to {len(missing)} point(s) "
            f"with positive target probability (e.g. {preview!r}). Importance "
            "sampling is unavailable: weights would be infinite and the "
            "estimators are not defined.")
    return None


# ---------------------------------------------------------------------------
# Finite discrete distributions and exact references
# ---------------------------------------------------------------------------

class DiscreteDistribution:
    """Finite discrete distribution; the pmf is normalized on construction.

    Points with zero probability are kept out of the support, so
    ``pdf(x) == 0.0`` exactly for uncovered points (used by coverage checks).
    """

    def __init__(self, pmf: dict):
        total = sum(pmf.values())
        if total <= 0:
            raise ValueError("pmf must have positive total mass")
        if any(p < 0 for p in pmf.values()):
            raise ValueError("pmf must be non-negative")
        self.support = [x for x, p in pmf.items() if p > 0]
        self._pmf = {x: pmf[x] / total for x in self.support}

    def pdf(self, x) -> float:
        return self._pmf.get(x, 0.0)

    def logpdf(self, x) -> float:
        p = self.pdf(x)
        return math.log(p) if p > 0.0 else NEG_INF

    def sample(self, rng: random.Random):
        u = rng.random()
        acc = 0.0
        for x in self.support:
            acc += self._pmf[x]
            if u <= acc:
                return x
        return self.support[-1]


def exact_discrete_expectation(dist: DiscreteDistribution, f) -> float:
    """[EXACT] E_p[f] over the finite support of a normalized distribution."""
    return sum(dist.pdf(x) * f(x) for x in dist.support)


# ---------------------------------------------------------------------------
# Mixture proposals
# ---------------------------------------------------------------------------

class MixtureProposal:
    """Mixture of proposal components:  q(x) = sum_k pi_k q_k(x).

    Components must expose ``logpdf(x)`` and ``sample(rng)`` (e.g.
    ``DiscreteDistribution``). Weights drawn from the mixture are evaluated
    against the mixture density itself, which is the true generating density.
    """

    def __init__(self, components: Sequence, weights: Sequence[float]):
        if len(components) != len(weights):
            raise ValueError("components and weights must have equal length")
        if not components:
            raise ValueError("at least one component is required")
        if any(w <= 0 for w in weights):
            raise ValueError("mixture weights must be positive")
        total = sum(weights)
        self.components = list(components)
        self._log_weights = [math.log(w / total) for w in weights]

    def logpdf(self, x) -> float:
        return logsumexp(lw + comp.logpdf(x)
                         for lw, comp in zip(self._log_weights, self.components))

    def pdf(self, x) -> float:
        lp = self.logpdf(x)
        return math.exp(lp) if lp > NEG_INF else 0.0

    def sample(self, rng: random.Random):
        u = rng.random()
        acc = 0.0
        for lw, comp in zip(self._log_weights, self.components):
            acc += math.exp(lw)
            if u <= acc:
                return comp.sample(rng)
        return self.components[-1].sample(rng)


# ---------------------------------------------------------------------------
# Adaptive / multi-round importance sampling
# ---------------------------------------------------------------------------

@dataclass
class RoundRecord:
    """One adaptive round.

    ``log_weights`` are computed at generation time with the proposal density
    actually used for that round, then frozen. Checkpoint restore reuses them
    verbatim and never recomputes them with a newer proposal density.
    """

    index: int
    n: int
    proposal_description: str
    samples: list
    values: list
    log_weights: list


def _encode_logw(lw: float):
    if math.isnan(lw):
        return "nan"
    if lw == POS_INF:
        return "inf"
    if lw == NEG_INF:
        return "-inf"
    return lw


def _decode_logw(raw) -> float:
    if raw == "nan":
        return float("nan")
    if raw == "inf":
        return POS_INF
    if raw == "-inf":
        return NEG_INF
    return float(raw)


class AdaptiveIS:
    """Multi-round adaptive importance sampling.

    Each round may use a different (e.g. adapted) proposal. The weight of
    every sample uses the proposal density in force when that sample was
    generated; stored weights survive checkpoint/restore unchanged.
    """

    CHECKPOINT_FORMAT = "adaptive-is-checkpoint-v1"

    def __init__(self, target_logpdf, f, *, rng: random.Random, seed: int | None = None):
        self.target_logpdf = target_logpdf
        self.f = f
        self.rng = rng
        self.seed = seed
        self.rounds: list[RoundRecord] = []

    def run_round(self, n: int, proposal_logpdf, proposal_sampler,
                  description: str = "") -> RoundRecord:
        """Draw ``n`` samples from the current proposal and freeze their weights."""
        samples = [proposal_sampler(self.rng) for _ in range(n)]
        log_weights = [_log_weight(self.target_logpdf, proposal_logpdf, x)
                       for x in samples]
        record = RoundRecord(
            index=len(self.rounds),
            n=n,
            proposal_description=description,
            samples=samples,
            values=[self.f(x) for x in samples],
            log_weights=log_weights,
        )
        self.rounds.append(record)
        return record

    def estimates(self, *, ess_fraction: float = 0.1) -> ISResult:
        """Pool all rounds.

        Pooled ordinary estimate (1/N) sum w_i f(x_i) remains [EXACT]
        unbiased across rounds because each weight uses its own round's true
        generating density; the pooled self-normalized estimate is consistent.
        """
        values = [v for r in self.rounds for v in r.values]
        log_weights = [lw for r in self.rounds for lw in r.log_weights]
        return estimate_from_log_weights(
            values, log_weights, seed=self.seed, ess_fraction=ess_fraction)

    def save_checkpoint(self, path) -> None:
        payload = {
            "format": self.CHECKPOINT_FORMAT,
            "seed": self.seed,
            "rounds": [
                {
                    "index": r.index,
                    "n": r.n,
                    "proposal_description": r.proposal_description,
                    "samples": r.samples,
                    "values": r.values,
                    "log_weights": [_encode_logw(lw) for lw in r.log_weights],
                }
                for r in self.rounds
            ],
        }
        Path(path).write_text(json.dumps(payload, indent=2), encoding="utf-8")

    @classmethod
    def load_checkpoint(cls, path, target_logpdf, f, *,
                        rng: random.Random | None = None,
                        seed: int | None = None) -> "AdaptiveIS":
        """Restore rounds from a checkpoint.

        Stored log weights are reused verbatim; they are NOT recomputed with
        any newer proposal density. A fresh ``rng`` may be injected for
        subsequent rounds; the recorded seed is restored unless overridden.
        """
        payload = json.loads(Path(path).read_text(encoding="utf-8"))
        if payload.get("format") != cls.CHECKPOINT_FORMAT:
            raise ValueError(f"unrecognized checkpoint format: {payload.get('format')!r}")
        restored_seed = seed if seed is not None else payload.get("seed")
        obj = cls(target_logpdf, f, rng=rng if rng is not None else random.Random(),
                  seed=restored_seed)
        for r in payload["rounds"]:
            obj.rounds.append(RoundRecord(
                index=r["index"],
                n=r["n"],
                proposal_description=r["proposal_description"],
                samples=list(r["samples"]),
                values=list(r["values"]),
                log_weights=[_decode_logw(lw) for lw in r["log_weights"]],
            ))
        return obj
