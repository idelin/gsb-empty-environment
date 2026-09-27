"""Rejection sampling with explicit certified envelopes, budgets, and cancellation.

Design guarantees
-----------------
- The envelope constant is explicit. It may be set per partition and refined
  adaptively, but every partition bound must be backed by either a caller
  certificate or an exact finite-domain verification.
- If target/proposal ever exceeds the certified envelope, sampling aborts
  immediately with ``EnvelopeViolationError``. The ratio is never clamped to 1
  and no samples produced after a violation are claimed to be unbiased.
- The random source is injected by the caller; the seed (supplied as audit
  metadata) and the number of draws are recorded.
- Acceptance-rate statistics always use the number of attempts as denominator.
- Budgets bound the number of proposal attempts; on exhaustion or cancellation
  the partial result (completed samples + remaining count) is returned.
- Sampler state (RNG state + confirmed acceptance positions) can be
  checkpointed and restored.
- Exact conclusions (finite enumeration) and empirical estimates (sampled)
  are always labelled separately.
"""

from __future__ import annotations

import math
from collections import Counter
from dataclasses import dataclass, field
from fractions import Fraction
from typing import Any, Callable, Hashable, Iterable, Sequence


class EnvelopeViolationError(RuntimeError):
    """Raised immediately when target/proposal exceeds the certified envelope."""


class CertificateRequiredError(ValueError):
    """Raised when a partition bound lacks a certificate or exact verification."""


# ---------------------------------------------------------------------------
# Certificates and partitioned envelopes
# ---------------------------------------------------------------------------

CALLER_CERTIFIED = "caller_certified"
EXACT_FINITE_VERIFICATION = "exact_finite_verification"


@dataclass(frozen=True)
class Certificate:
    """Evidence that one partition's bound is valid."""

    partition: tuple[float, float]
    bound: float
    method: str  # CALLER_CERTIFIED or EXACT_FINITE_VERIFICATION
    evidence: Any = None


class PartitionedEnvelope:
    """Piecewise-constant envelope over a 1D domain with adaptive refinement.

    Each partition's bound must be justified by a caller certificate or by
    exact verification over a finite set of domain points.
    """

    def __init__(
        self,
        target: Callable[[Any], float],
        proposal_pdf: Callable[[Any], float],
    ) -> None:
        self._target = target
        self._proposal_pdf = proposal_pdf
        self._parts: list[tuple[float, float]] = []  # sorted, non-overlapping
        self._certs: dict[tuple[float, float], Certificate] = {}

    @property
    def certificates(self) -> dict[tuple[float, float], Certificate]:
        return dict(self._certs)

    @property
    def partitions(self) -> list[tuple[float, float]]:
        return list(self._parts)

    def _build_cert(
        self,
        lo: float,
        hi: float,
        bound: float,
        *,
        certificate: Any,
        exact_domain: Iterable[Any] | None,
    ) -> Certificate:
        if not lo < hi:
            raise ValueError(f"empty partition ({lo}, {hi})")
        if not (math.isfinite(bound) and bound > 0):
            raise ValueError(f"bound must be positive and finite, got {bound!r}")
        if exact_domain is not None:
            points = [x for x in exact_domain if lo <= x < hi]
            if not points:
                raise ValueError(
                    f"exact_domain has no points inside partition ({lo}, {hi})"
                )
            max_ratio = max(self._target(x) / self._proposal_pdf(x) for x in points)
            if max_ratio > bound:
                raise EnvelopeViolationError(
                    f"exact verification failed on ({lo}, {hi}): "
                    f"max target/proposal = {max_ratio} > bound {bound}"
                )
            return Certificate(
                (lo, hi),
                bound,
                EXACT_FINITE_VERIFICATION,
                {"max_ratio": max_ratio, "n_points": len(points)},
            )
        if certificate is None:
            raise CertificateRequiredError(
                f"partition ({lo}, {hi}) needs a caller certificate or "
                f"exact finite-domain verification"
            )
        return Certificate((lo, hi), bound, CALLER_CERTIFIED, certificate)

    def _check_overlap(self, lo: float, hi: float) -> None:
        for p_lo, p_hi in self._parts:
            if lo < p_hi and p_lo < hi:
                raise ValueError(f"({lo}, {hi}) overlaps existing ({p_lo}, {p_hi})")

    def add_partition(
        self,
        lo: float,
        hi: float,
        bound: float,
        *,
        certificate: Any = None,
        exact_domain: Iterable[Any] | None = None,
    ) -> Certificate:
        """Register a certified bound for [lo, hi)."""
        lo, hi = float(lo), float(hi)
        self._check_overlap(lo, hi)
        cert = self._build_cert(
            lo, hi, bound, certificate=certificate, exact_domain=exact_domain
        )
        self._parts.append((lo, hi))
        self._parts.sort()
        self._certs[(lo, hi)] = cert
        return cert

    def refine(self, lo: float, hi: float, subparts: Sequence[dict]) -> list[Certificate]:
        """Adaptively split partition [lo, hi) into finer certified partitions.

        ``subparts`` is a sequence of dicts with keys ``lo``, ``hi``, ``bound``
        and optionally ``certificate`` / ``exact_domain`` (same semantics as
        :meth:`add_partition`). The sub-partitions must tile [lo, hi) exactly.
        The refinement is atomic: if any sub-partition fails certification the
        original partition is left untouched.
        """
        lo, hi = float(lo), float(hi)
        if (lo, hi) not in self._certs:
            raise KeyError(f"no existing partition ({lo}, {hi}) to refine")
        ordered = sorted(subparts, key=lambda s: s["lo"])
        cursor = lo
        for sub in ordered:
            if abs(float(sub["lo"]) - cursor) > 0.0 and float(sub["lo"]) != cursor:
                raise ValueError("sub-partitions must tile the parent exactly")
            cursor = float(sub["hi"])
        if cursor != hi:
            raise ValueError("sub-partitions must tile the parent exactly")
        certs = [
            self._build_cert(
                float(s["lo"]),
                float(s["hi"]),
                s["bound"],
                certificate=s.get("certificate"),
                exact_domain=s.get("exact_domain"),
            )
            for s in ordered
        ]
        for s in ordered:
            self._check_overlap_excluding(float(s["lo"]), float(s["hi"]), (lo, hi))
        self._parts.remove((lo, hi))
        del self._certs[(lo, hi)]
        for s, cert in zip(ordered, certs):
            key = (float(s["lo"]), float(s["hi"]))
            self._parts.append(key)
            self._certs[key] = cert
        self._parts.sort()
        return certs

    def _check_overlap_excluding(
        self, lo: float, hi: float, exclude: tuple[float, float]
    ) -> None:
        for p_lo, p_hi in self._parts:
            if (p_lo, p_hi) == exclude:
                continue
            if lo < p_hi and p_lo < hi:
                raise ValueError(f"({lo}, {hi}) overlaps existing ({p_lo}, {p_hi})")

    def bound_at(self, x: Any) -> float:
        for p_lo, p_hi in self._parts:
            if p_lo <= x < p_hi:
                return self._certs[(p_lo, p_hi)].bound
        raise KeyError(f"no certified partition covers x={x!r}")

    def certificate_at(self, x: Any) -> Certificate:
        for p_lo, p_hi in self._parts:
            if p_lo <= x < p_hi:
                return self._certs[(p_lo, p_hi)]
        raise KeyError(f"no certified partition covers x={x!r}")


def constant_envelope(
    target: Callable[[Any], float],
    proposal_pdf: Callable[[Any], float],
    bound: float,
    *,
    certificate: Any = None,
    exact_domain: Iterable[Any] | None = None,
) -> PartitionedEnvelope:
    """Convenience: a single certified partition covering the whole real line."""
    env = PartitionedEnvelope(target, proposal_pdf)
    env.add_partition(
        float("-inf"),
        float("inf"),
        bound,
        certificate=certificate,
        exact_domain=exact_domain,
    )
    return env


# ---------------------------------------------------------------------------
# Sampler state and results
# ---------------------------------------------------------------------------


@dataclass
class SamplerState:
    """Recoverable checkpoint: RNG state plus confirmed acceptance positions."""

    rng_state: object
    attempts: int
    accepted_positions: tuple[int, ...]  # 1-based attempt indices of accepts


@dataclass
class SamplingResult:
    """Outcome of one ``sample`` call.

    ``reason`` is one of "completed", "budget_exhausted", "cancelled".
    ``acceptance_rate`` is an EMPIRICAL estimate with denominator = attempts.
    """

    samples: list[Any]
    attempts: int
    requested: int
    reason: str
    seed: Any = None
    state: SamplerState | None = None

    @property
    def accepted(self) -> int:
        return len(self.samples)

    @property
    def remaining(self) -> int:
        return self.requested - len(self.samples)

    @property
    def completed(self) -> bool:
        return self.reason == "completed"

    @property
    def acceptance_rate(self) -> float:
        """Empirical acceptance rate: accepted / attempts."""
        return len(self.samples) / self.attempts if self.attempts else 0.0


def _is_cancelled(cancel: Any) -> bool:
    if hasattr(cancel, "is_set"):
        return bool(cancel.is_set())
    return bool(cancel())


class RejectionSampler:
    """Rejection sampler with a certified envelope, budget, and cancellation.

    The caller injects the random source (``rng``). ``seed`` is audit metadata
    recorded on every result; ``random.Random`` does not expose its seed, so
    the caller declares it.
    """

    def __init__(
        self,
        *,
        target: Callable[[Any], float],
        proposal_pdf: Callable[[Any], float],
        proposal_sample: Callable[[Any], Any],
        envelope: PartitionedEnvelope,
        rng: Any,
        seed: Any = None,
    ) -> None:
        if rng is None:
            raise ValueError("caller must inject a random source via rng=")
        self._target = target
        self._proposal_pdf = proposal_pdf
        self._proposal_sample = proposal_sample
        self._envelope = envelope
        self._rng = rng
        self._seed = seed
        self._attempts = 0
        self._accepted_positions: list[int] = []

    @property
    def state(self) -> SamplerState:
        return SamplerState(
            self._rng.getstate(), self._attempts, tuple(self._accepted_positions)
        )

    def restore(self, state: SamplerState) -> None:
        self._rng.setstate(state.rng_state)
        self._attempts = state.attempts
        self._accepted_positions = list(state.accepted_positions)

    @property
    def total_attempts(self) -> int:
        return self._attempts

    @property
    def accepted_positions(self) -> tuple[int, ...]:
        return tuple(self._accepted_positions)

    def sample(
        self,
        n: int,
        *,
        budget: int | None = None,
        cancel: Any = None,
    ) -> SamplingResult:
        """Draw up to ``n`` samples.

        ``budget`` caps the number of proposal attempts for this call.
        ``cancel`` may be a ``threading.Event`` or a zero-arg callable.
        On budget exhaustion or cancellation the partial result is returned
        with the completed samples and the remaining count.
        """
        if n < 0:
            raise ValueError("n must be >= 0")
        if budget is not None and budget < 0:
            raise ValueError("budget must be >= 0")
        samples: list[Any] = []
        attempts_used = 0
        reason = "completed"
        while len(samples) < n:
            if cancel is not None and _is_cancelled(cancel):
                reason = "cancelled"
                break
            if budget is not None and attempts_used >= budget:
                reason = "budget_exhausted"
                break
            x = self._proposal_sample(self._rng)
            self._attempts += 1
            attempts_used += 1
            t = self._target(x)
            q = self._proposal_pdf(x)
            bound = self._envelope.bound_at(x)
            if q <= 0:
                if t > 0:
                    raise EnvelopeViolationError(
                        f"proposal has zero density at x={x!r} where target is "
                        f"positive; envelope cannot hold"
                    )
                continue  # impossible point for both; does not count as accept
            ratio = t / (bound * q)
            if ratio > 1.0:
                # Never clamp to 1: abort instead of emitting biased samples.
                raise EnvelopeViolationError(
                    f"target/proposal ratio {ratio!r} exceeds certified envelope "
                    f"bound {bound!r} at x={x!r}; aborting, no unbiased-sample "
                    f"claim is made"
                )
            if self._rng.random() < ratio:
                samples.append(x)
                self._accepted_positions.append(self._attempts)
        return SamplingResult(
            samples=samples,
            attempts=attempts_used,
            requested=n,
            reason=reason,
            seed=self._seed,
            state=self.state,
        )


# ---------------------------------------------------------------------------
# Finite discrete targets: exact enumeration vs empirical estimates
# ---------------------------------------------------------------------------

LABEL_EXACT = "exact"
LABEL_EMPIRICAL = "empirical"


def exact_finite_distribution(
    target: Callable[[Any], float], domain: Sequence[Hashable]
) -> dict:
    """Exact normalized distribution over a finite domain (labelled 'exact')."""
    weights = [(x, Fraction(target(x)).limit_denominator(10**12)) for x in domain]
    total = sum(w for _, w in weights)
    if total <= 0:
        raise ValueError("target has zero total mass over the domain")
    return {
        "label": LABEL_EXACT,
        "probabilities": {x: w / total for x, w in weights},
        "total_mass": total,
    }


def expected_acceptance_probability(
    target: Callable[[Any], float],
    proposal_pdf: Callable[[Any], float],
    domain: Sequence[Hashable],
    bound: float,
) -> Fraction:
    """Exact acceptance probability Z / bound for a constant envelope.

    Requires the proposal to normalize to exactly 1 over ``domain``.
    """
    total = sum(Fraction(target(x)).limit_denominator(10**12) for x in domain)
    q_total = sum(Fraction(proposal_pdf(x)).limit_denominator(10**12) for x in domain)
    if q_total != 1:
        raise ValueError(f"proposal must normalize to 1 over domain, got {q_total}")
    return total / Fraction(bound).limit_denominator(10**12)


def empirical_distribution(samples: Sequence[Hashable]) -> dict:
    """Empirical distribution of collected samples (labelled 'empirical')."""
    counts = Counter(samples)
    n = len(samples)
    return {
        "label": LABEL_EMPIRICAL,
        "n": n,
        "probabilities": {x: c / n for x, c in counts.items()} if n else {},
    }


def verify_sample_distribution(
    samples: Sequence[Hashable],
    target: Callable[[Any], float],
    domain: Sequence[Hashable],
    tolerance: float,
) -> dict:
    """Compare the empirical sample distribution against the exact one."""
    exact = exact_finite_distribution(target, domain)
    emp = empirical_distribution(samples)
    max_dev = max(
        abs(emp["probabilities"].get(x, 0.0) - float(p))
        for x, p in exact["probabilities"].items()
    )
    return {
        "label": "empirical-vs-exact",
        "within_tolerance": max_dev <= tolerance,
        "max_deviation": max_dev,
        "tolerance": tolerance,
        "exact": exact,
        "empirical": emp,
    }


@dataclass
class RejectionCountReport:
    """Observed (empirical) rejection counts vs exact expectation."""

    attempts: int
    accepted: int
    rejected: int
    expected_acceptance: Fraction  # exact
    observed_acceptance: float  # empirical, denominator = attempts
    tolerance: float
    within_tolerance: bool
    label: str = "empirical-vs-exact"


def check_rejection_counts(
    result: SamplingResult,
    expected_acceptance: Fraction,
    tolerance: float,
) -> RejectionCountReport:
    """Verify observed acceptance/rejection counts against the exact rate."""
    observed = result.acceptance_rate
    expected = float(expected_acceptance)
    return RejectionCountReport(
        attempts=result.attempts,
        accepted=result.accepted,
        rejected=result.attempts - result.accepted,
        expected_acceptance=expected_acceptance,
        observed_acceptance=observed,
        tolerance=tolerance,
        within_tolerance=abs(observed - expected) <= tolerance,
    )
