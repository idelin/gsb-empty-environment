"""Rejection sampling with certified envelopes, budgets and cancellation.

Design rules enforced by this module:

* The envelope constant is explicit and checked per attempt: the
  target/proposal ratio must never exceed the envelope bound.  A violation
  raises :class:`BoundViolationError` immediately; the ratio is never
  clamped to 1 and no sample produced after a violation is claimed unbiased.
* The envelope may be set per partition and refined adaptively, but every
  partition bound must be backed by a caller-supplied certificate or by
  exact finite-domain verification before it is used.  Per-partition bounds
  are used for the violation check; the acceptance probability uses the
  global envelope constant (the maximum partition bound), so the output
  distribution is exactly proportional to the target.
* The random source is injected by the caller; the seed and the number of
  confirmed samples are recorded so runs and confirmed sample positions
  can be recovered by replay.
* Budgets and cancellation return only the samples completed so far.
* Acceptance-rate statistics use the number of attempts as denominator.
* Exact (analytic, finite-domain) conclusions and empirical (simulation)
  estimates are always labelled separately.
"""

from __future__ import annotations

import math
import random
from collections import Counter
from dataclasses import dataclass, replace
from enum import Enum
from typing import Any, Callable, Dict, Iterable, List, Optional, Sequence, Tuple

__all__ = [
    "BoundViolationError",
    "UncertifiedBoundError",
    "DomainNotCoveredError",
    "RecoveryMismatchError",
    "Certificate",
    "Partition",
    "Envelope",
    "CountingRandom",
    "UniformFiniteProposal",
    "CancellationToken",
    "Status",
    "AcceptedRecord",
    "SamplingResult",
    "RejectionSampler",
    "verify_partition_exactly",
    "verify_envelope_exactly",
    "ExactVerificationReport",
    "ExactAnalysis",
    "analyze_finite",
    "EmpiricalReport",
    "empirical_report",
    "total_variation",
    "recover_confirmed_samples",
]

REL_TOL = 1e-9


class BoundViolationError(RuntimeError):
    """The target/proposal ratio exceeded the envelope bound."""


class UncertifiedBoundError(RuntimeError):
    """A partition bound has no caller certificate and no exact verification."""


class DomainNotCoveredError(RuntimeError):
    """No partition of the envelope covers a required point."""


class RecoveryMismatchError(RuntimeError):
    """Replay did not reproduce a previously confirmed sample."""


@dataclass(frozen=True)
class Certificate:
    """Caller-supplied attestation that a partition bound is valid."""

    attestation: str
    issuer: str = "caller"


@dataclass
class Partition:
    """A region of the domain with an explicit envelope bound."""

    name: str
    contains: Callable[[Any], bool]
    bound: float
    certificate: Optional[Certificate] = None
    verified_exactly: bool = False

    def is_trusted(self) -> bool:
        return self.certificate is not None or self.verified_exactly


def _acceptance_ratio(target: Callable[[Any], float], proposal: Any, x: Any) -> float:
    w = float(target(x))
    q = float(proposal.pmf(x))
    if w < 0.0:
        raise ValueError(f"target weight must be non-negative, got {w} at {x!r}")
    if q < 0.0:
        raise ValueError(f"proposal pmf must be non-negative, got {q} at {x!r}")
    if q == 0.0:
        return math.inf if w > 0.0 else 0.0
    return w / q


def _ratio_exceeds(ratio: float, bound: float, rel_tol: float = REL_TOL) -> bool:
    return ratio > bound and not math.isclose(ratio, bound, rel_tol=rel_tol, abs_tol=0.0)


class Envelope:
    """Partitioned envelope with explicit per-partition bounds."""

    def __init__(self, partitions: Iterable[Partition] = ()):
        self._partitions: List[Partition] = list(partitions)

    @property
    def partitions(self) -> Tuple[Partition, ...]:
        return tuple(self._partitions)

    def add_partition(self, partition: Partition) -> None:
        self._partitions.append(partition)

    def partition_for(self, x: Any) -> Partition:
        for p in self._partitions:
            if p.contains(x):
                return p
        raise DomainNotCoveredError(f"no envelope partition covers {x!r}")

    def bound_for(self, x: Any) -> float:
        p = self.partition_for(x)
        if not p.is_trusted():
            raise UncertifiedBoundError(
                f"partition {p.name!r} has neither a caller certificate nor "
                "exact finite-domain verification"
            )
        return p.bound

    def acceptance_bound(self) -> float:
        """Global envelope constant: the maximum trusted partition bound.

        Used as the acceptance denominator so that accepted samples are
        exactly proportional to the target even with per-partition bounds.
        """
        if not self._partitions:
            raise DomainNotCoveredError("envelope has no partitions")
        for p in self._partitions:
            if not p.is_trusted():
                raise UncertifiedBoundError(
                    f"partition {p.name!r} has neither a caller certificate nor "
                    "exact finite-domain verification"
                )
        return max(p.bound for p in self._partitions)

    def refine(
        self,
        name: str,
        subpartitions: Iterable[Partition],
        *,
        target: Optional[Callable[[Any], float]] = None,
        proposal: Any = None,
        domain: Optional[Iterable[Any]] = None,
    ) -> None:
        """Replace partition ``name`` by finer subpartitions.

        Each subpartition must carry a caller certificate, be already
        verified, or be verifiable exactly against the finite ``domain``
        (requires ``target``, ``proposal`` and ``domain``).  Anything else
        raises :class:`UncertifiedBoundError`; a bound that fails exact
        verification raises :class:`BoundViolationError`.
        """
        idx = next(
            (i for i, p in enumerate(self._partitions) if p.name == name), None
        )
        if idx is None:
            raise KeyError(f"no partition named {name!r}")
        parent = self._partitions[idx]
        subs = list(subpartitions)
        if not subs:
            raise ValueError("refinement needs at least one subpartition")
        if domain is not None:
            for x in domain:
                if parent.contains(x) and not any(sp.contains(x) for sp in subs):
                    raise DomainNotCoveredError(
                        f"refinement of {name!r} leaves {x!r} uncovered"
                    )
        trusted_subs: List[Partition] = []
        for sp in subs:
            if sp.is_trusted():
                trusted_subs.append(sp)
                continue
            if target is not None and proposal is not None and domain is not None:
                if not verify_partition_exactly(sp, target, proposal, domain):
                    raise BoundViolationError(
                        f"exact verification failed for refined partition {sp.name!r}"
                    )
                trusted_subs.append(replace(sp, verified_exactly=True))
            else:
                raise UncertifiedBoundError(
                    f"refined partition {sp.name!r} has no certificate and no "
                    "exact finite-domain verification data was supplied"
                )
        self._partitions[idx : idx + 1] = trusted_subs


class CountingRandom:
    """Wraps a ``random.Random``-like object and counts draws consumed."""

    def __init__(self, rng: random.Random):
        self._rng = rng
        self.draws = 0

    def __getattr__(self, name: str):
        attr = getattr(self._rng, name)
        if not callable(attr):
            return attr

        def counted(*args, **kwargs):
            self.draws += 1
            return attr(*args, **kwargs)

        return counted


class UniformFiniteProposal:
    """Uniform proposal over a finite domain (uses one ``rng.random()`` draw)."""

    def __init__(self, domain: Sequence[Any]):
        if not domain:
            raise ValueError("domain must be non-empty")
        self.domain = list(domain)

    def sample(self, rng: random.Random) -> Any:
        return self.domain[int(rng.random() * len(self.domain))]

    def pmf(self, x: Any) -> float:
        return 1.0 / len(self.domain) if x in self.domain else 0.0


@dataclass
class CancellationToken:
    _cancelled: bool = False

    def cancel(self) -> None:
        self._cancelled = True

    @property
    def cancelled(self) -> bool:
        return self._cancelled


class Status(str, Enum):
    COMPLETED = "completed"
    BUDGET_EXHAUSTED = "budget_exhausted"
    CANCELLED = "cancelled"


@dataclass(frozen=True)
class AcceptedRecord:
    """A confirmed sample and where it happened, sufficient for replay."""

    value: Any
    attempt_index: int
    draws_used: int


@dataclass(frozen=True)
class SamplingResult:
    status: Status
    requested: int
    samples: Tuple[Any, ...]
    records: Tuple[AcceptedRecord, ...]
    attempts: int
    seed: Any

    @property
    def accepted(self) -> int:
        return len(self.samples)

    @property
    def remaining(self) -> int:
        return self.requested - self.accepted

    @property
    def acceptance_rate(self) -> float:
        """Accepted divided by attempts (the denominator is attempts)."""
        return self.accepted / self.attempts if self.attempts else 0.0


def _run_attempt(target, proposal, envelope, rng, rel_tol: float = REL_TOL):
    x = proposal.sample(rng)
    ratio = _acceptance_ratio(target, proposal, x)
    partition_bound = envelope.bound_for(x)
    if _ratio_exceeds(ratio, partition_bound, rel_tol):
        raise BoundViolationError(
            f"target/proposal ratio {ratio!r} exceeds envelope bound "
            f"{partition_bound!r} at {x!r}; aborting instead of clamping"
        )
    global_bound = envelope.acceptance_bound()
    u = rng.random()
    return x, (u * global_bound) < ratio


class RejectionSampler:
    """Rejection sampler with certified envelope, budget and cancellation.

    The random source is injected by the caller (``rng``), or built from a
    caller-supplied ``seed``.  The seed and the number of confirmed samples
    are recorded on every result so runs can be recovered by replay.
    """

    def __init__(
        self,
        target: Callable[[Any], float],
        proposal: Any,
        envelope: Envelope,
        *,
        rng: Optional[random.Random] = None,
        seed: Any = None,
    ):
        if rng is None:
            if seed is None:
                raise ValueError("caller must inject rng or provide a seed")
            rng = random.Random(seed)
        self.target = target
        self.proposal = proposal
        self.envelope = envelope
        self.seed = seed
        self.rng = CountingRandom(rng)

    def sample(
        self,
        n: int,
        *,
        max_attempts: Optional[int] = None,
        token: Optional[CancellationToken] = None,
    ) -> SamplingResult:
        if n < 0:
            raise ValueError("n must be non-negative")
        if max_attempts is not None and max_attempts < 0:
            raise ValueError("max_attempts must be non-negative")
        samples: List[Any] = []
        records: List[AcceptedRecord] = []
        attempts = 0
        status = Status.COMPLETED
        while len(samples) < n:
            if token is not None and token.cancelled:
                status = Status.CANCELLED
                break
            if max_attempts is not None and attempts >= max_attempts:
                status = Status.BUDGET_EXHAUSTED
                break
            attempts += 1
            x, accepted = _run_attempt(
                self.target, self.proposal, self.envelope, self.rng
            )
            if accepted:
                samples.append(x)
                records.append(
                    AcceptedRecord(
                        value=x, attempt_index=attempts, draws_used=self.rng.draws
                    )
                )
        return SamplingResult(
            status=status,
            requested=n,
            samples=tuple(samples),
            records=tuple(records),
            attempts=attempts,
            seed=self.seed,
        )


def verify_partition_exactly(
    partition: Partition,
    target: Callable[[Any], float],
    proposal: Any,
    domain: Iterable[Any],
    *,
    rel_tol: float = REL_TOL,
) -> bool:
    """Exact finite-domain check that ``partition.bound`` covers the ratio."""
    for x in domain:
        if not partition.contains(x):
            continue
        ratio = _acceptance_ratio(target, proposal, x)
        if _ratio_exceeds(ratio, partition.bound, rel_tol):
            return False
    return True


@dataclass(frozen=True)
class ExactVerificationReport:
    kind: str
    all_ok: bool
    per_partition: Dict[str, bool]


def verify_envelope_exactly(
    envelope: Envelope,
    target: Callable[[Any], float],
    proposal: Any,
    domain: Iterable[Any],
    *,
    rel_tol: float = REL_TOL,
) -> ExactVerificationReport:
    """Verify every partition bound exactly over the finite domain.

    Partitions that pass are marked ``verified_exactly`` so they can be
    used without a caller certificate.
    """
    domain = list(domain)
    per_partition: Dict[str, bool] = {}
    for x in domain:
        envelope.partition_for(x)  # raises DomainNotCoveredError if uncovered
    for p in envelope.partitions:
        ok = verify_partition_exactly(p, target, proposal, domain, rel_tol=rel_tol)
        per_partition[p.name] = ok
        if ok:
            p.verified_exactly = True
    return ExactVerificationReport(
        kind="exact",
        all_ok=all(per_partition.values()),
        per_partition=per_partition,
    )


@dataclass(frozen=True)
class ExactAnalysis:
    """Analytic results obtained by enumerating the finite domain."""

    kind: str
    acceptance_probability: float
    expected_attempts_per_sample: float
    expected_rejections_per_sample: float
    output_distribution: Dict[Any, float]


def analyze_finite(
    domain: Iterable[Any],
    target: Callable[[Any], float],
    proposal: Any,
    envelope: Envelope,
    *,
    rel_tol: float = REL_TOL,
) -> ExactAnalysis:
    """Exact acceptance probability, rejections and output distribution."""
    domain = list(domain)
    global_bound = envelope.acceptance_bound()
    p_acc = 0.0
    weighted: Dict[Any, float] = {}
    for x in domain:
        q = float(proposal.pmf(x))
        ratio = _acceptance_ratio(target, proposal, x)
        partition_bound = envelope.bound_for(x)
        if _ratio_exceeds(ratio, partition_bound, rel_tol):
            raise BoundViolationError(
                f"target/proposal ratio {ratio!r} exceeds envelope bound "
                f"{partition_bound!r} at {x!r}"
            )
        accept_prob = 0.0 if global_bound == 0.0 else min(1.0, ratio / global_bound)
        p_acc += q * accept_prob
        weighted[x] = q * accept_prob
    if p_acc <= 0.0:
        raise ValueError("zero acceptance probability; sampler cannot terminate")
    return ExactAnalysis(
        kind="exact",
        acceptance_probability=p_acc,
        expected_attempts_per_sample=1.0 / p_acc,
        expected_rejections_per_sample=(1.0 - p_acc) / p_acc,
        output_distribution={x: v / p_acc for x, v in weighted.items()},
    )


@dataclass(frozen=True)
class EmpiricalReport:
    """Simulation estimates; never to be confused with exact conclusions."""

    kind: str
    samples: int
    attempts: int
    rejections: int
    acceptance_rate: float
    distribution: Dict[Any, float]


def empirical_report(result: SamplingResult) -> EmpiricalReport:
    counts = Counter(result.samples)
    total = len(result.samples)
    dist = {x: c / total for x, c in counts.items()} if total else {}
    return EmpiricalReport(
        kind="empirical",
        samples=total,
        attempts=result.attempts,
        rejections=result.attempts - total,
        acceptance_rate=result.acceptance_rate,
        distribution=dist,
    )


def total_variation(exact: Dict[Any, float], empirical: Dict[Any, float]) -> float:
    keys = set(exact) | set(empirical)
    return 0.5 * sum(abs(exact.get(k, 0.0) - empirical.get(k, 0.0)) for k in keys)


def recover_confirmed_samples(
    target: Callable[[Any], float],
    proposal: Any,
    envelope: Envelope,
    seed: Any,
    records: Sequence[AcceptedRecord],
) -> List[Any]:
    """Replay from ``seed`` and recover the confirmed sample positions.

    Raises :class:`RecoveryMismatchError` if the replayed value at a
    recorded attempt index differs from the recorded value.
    """
    ordered = sorted(records, key=lambda r: r.attempt_index)
    if not ordered:
        return []
    rng = CountingRandom(random.Random(seed))
    recovered: List[Any] = []
    next_idx = 0
    last_attempt = ordered[-1].attempt_index
    for attempt in range(1, last_attempt + 1):
        x, accepted = _run_attempt(target, proposal, envelope, rng)
        if (
            accepted
            and next_idx < len(ordered)
            and attempt == ordered[next_idx].attempt_index
        ):
            if ordered[next_idx].value != x:
                raise RecoveryMismatchError(
                    f"attempt {attempt}: recorded {ordered[next_idx].value!r} "
                    f"but replay produced {x!r}"
                )
            recovered.append(x)
            next_idx += 1
    return recovered
