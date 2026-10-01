"""Auditing utilities: decomposition residuals and empirical covariances.

Two clearly separated kinds of statements:

* exact/deterministic: the decomposition residual ``||Sigma - A A^T||`` is a
  deterministic fact about the factorization (no randomness involved);
* empirical: sample means/covariances are statistical estimates whose
  accuracy depends on the (recorded) seed and sample size.
"""
from __future__ import annotations

from dataclasses import dataclass, field

from . import linalg
from .sampling import SamplingRecord


def sample_moments(samples: list[list[float]]):
    """Empirical mean and unbiased (n-1) covariance of the samples."""
    n = len(samples)
    if n < 2:
        raise ValueError("need at least 2 samples for a covariance estimate")
    dim = len(samples[0])
    mean = [sum(s[i] for s in samples) / n for i in range(dim)]
    cov = [
        [
            sum((s[i] - mean[i]) * (s[j] - mean[j]) for s in samples) / (n - 1)
            for j in range(dim)
        ]
        for i in range(dim)
    ]
    return mean, cov


@dataclass
class CovarianceAudit:
    """Empirical comparison of sample moments against the target covariance."""

    n_samples: int
    seed: int | None
    dim: int
    sample_mean: list[float]
    sample_covariance: linalg.Matrix
    mean_error_max: float
    cov_error_fro: float
    cov_error_relative: float
    kind: str = field(default="empirical")


def audit_samples(
    samples: list[list[float]],
    mean,
    cov,
    record: SamplingRecord | None = None,
) -> CovarianceAudit:
    """Compare empirical moments of ``samples`` against ``(mean, cov)``.

    The result is an *empirical estimate*; the seed/sample size are taken
    from ``record`` when provided so the audit trail is complete.
    """
    target_mean = [float(x) for x in mean]
    target_cov = linalg.as_matrix(cov, name="covariance")
    sample_mean, sample_cov = sample_moments(samples)
    diff = linalg.sub(sample_cov, target_cov)
    base = max(linalg.frobenius(target_cov), 1e-300)
    return CovarianceAudit(
        n_samples=len(samples),
        seed=record.seed if record else None,
        dim=len(target_mean),
        sample_mean=sample_mean,
        sample_covariance=sample_cov,
        mean_error_max=max(
            abs(a - b) for a, b in zip(sample_mean, target_mean)
        ),
        cov_error_fro=linalg.frobenius(diff),
        cov_error_relative=linalg.frobenius(diff) / base,
    )
