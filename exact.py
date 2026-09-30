"""Exact small-domain reference distribution of the inverse estimator.

For a single group with channel ``P``, true distribution ``pi`` and small
sample size ``n``, the output counts follow exactly

    counts ~ Multinomial(n, q),   q = P^T pi.

Enumerating all compositions of ``n`` into ``k`` parts gives the exact
distribution of the unconstrained inverse estimator
``pi_hat = (P^T)^{-1} (counts / n)`` — no Monte Carlo and no normal
approximation. Used as a reference to check estimator bias and to validate
the asymptotic covariance on small domains. Only feasible for small n and k
(the number of compositions is C(n + k - 1, k - 1)).
"""

import math

import linalg
from randomized_response import output_probabilities, validate_channel


def compositions(n, k):
    """All k-tuples of non-negative integers summing to n."""
    if k == 1:
        yield (n,)
        return
    for i in range(n + 1):
        for rest in compositions(n - i, k - 1):
            yield (i,) + rest


def multinomial_pmf(counts, q):
    """Pr[counts] under Multinomial(sum(counts), q)."""
    logp = math.lgamma(sum(counts) + 1)
    for c, qi in zip(counts, q):
        logp -= math.lgamma(c + 1)
        if c > 0:
            if qi <= 0.0:
                return 0.0
            logp += c * math.log(qi)
    return math.exp(logp)


def exact_estimator_moments(P, pi, n):
    """Exact mean / variance / bias of the unconstrained inverse estimator.

    Requires P to be invertible (full-rank channel). Returns a dict with
    keys ``total_mass`` (sanity check, should be 1), ``mean``, ``variance``
    and ``bias`` (mean minus pi; zero up to floating point error, since the
    estimator is linear in the counts).
    """
    k = validate_channel(P)
    pt_inv = linalg.inverse(linalg.transpose(P))
    q = output_probabilities(P, pi)
    mean = [0.0] * k
    second = [0.0] * k
    total = 0.0
    for counts in compositions(n, k):
        w = multinomial_pmf(counts, q)
        if w == 0.0:
            continue
        total += w
        est = linalg.matvec(pt_inv, [c / n for c in counts])
        for i in range(k):
            mean[i] += w * est[i]
            second[i] += w * est[i] * est[i]
    variance = [second[i] - mean[i] ** 2 for i in range(k)]
    bias = [mean[i] - pi[i] for i in range(k)]
    return {"total_mass": total, "mean": mean, "variance": variance, "bias": bias}
