"""Binary and k-ary randomized response channels (synthetic data only).

Definitions
-----------
A *channel* is a k x k row-stochastic matrix ``P`` with
``P[i][j] = Pr[output = j | input = i]``. The population distribution is a
probability vector ``pi`` of length ``k``; the induced output distribution is
``q = P^T pi``.

The k-ary epsilon-channel (generalized randomized response) is
    p_diag = e^eps / (e^eps + k - 1)   (report true value)
    p_off  = 1 / (e^eps + k - 1)       (report any other value)

Special cases
-------------
- ``k == 1``: the channel is ``[[1.0]]``; the output is a constant and the
  only possible population distribution is ``[1.0]``. Estimation is trivial.
- ``epsilon == 0``: every row equals ``(1/k, ..., 1/k)``; the output is
  independent of the input and the inverse problem is unidentifiable.

Nothing in this repository constitutes a formal privacy proof; the
statistical experiments here are illustrative and use synthetic data only.
"""

import math


def validate_k(k):
    if isinstance(k, bool) or not isinstance(k, int) or k < 1:
        raise ValueError("k must be an integer >= 1")


def validate_epsilon(epsilon):
    if isinstance(epsilon, bool) or not isinstance(epsilon, (int, float)):
        raise ValueError("epsilon must be a real number")
    if not math.isfinite(epsilon) or epsilon < 0:
        raise ValueError("epsilon must be finite and >= 0")


def kary_channel(k, epsilon):
    """k-ary epsilon randomized response channel matrix.

    Raises ValueError on invalid parameters.
    """
    validate_k(k)
    validate_epsilon(epsilon)
    if k == 1:
        return [[1.0]]
    e = math.exp(epsilon)
    p_diag = e / (e + k - 1.0)
    p_off = 1.0 / (e + k - 1.0)
    return [[p_diag if i == j else p_off for j in range(k)] for i in range(k)]


def binary_channel(p=None, q=None, epsilon=None):
    """Binary (Warner-style) channel.

    Either pass ``epsilon`` (symmetric channel, equivalent to
    ``kary_channel(2, epsilon)``) or explicit stay-probabilities ``p`` for
    input 0 and ``q`` for input 1:
        P[0] = [p, 1 - p],  P[1] = [1 - q, q].
    """
    if epsilon is not None:
        if p is not None or q is not None:
            raise ValueError("pass either epsilon or (p, q), not both")
        return kary_channel(2, epsilon)
    if p is None or q is None:
        raise ValueError("must pass epsilon or both p and q")
    for name, v in (("p", p), ("q", q)):
        if isinstance(v, bool) or not isinstance(v, (int, float)):
            raise ValueError(f"{name} must be a real number in [0, 1]")
        if not 0.0 <= v <= 1.0:
            raise ValueError(f"{name} must be in [0, 1]")
    return [[float(p), 1.0 - float(p)], [1.0 - float(q), float(q)]]


def validate_channel(P):
    """Validate a square row-stochastic channel matrix; returns k."""
    if not P or any(len(row) != len(P) for row in P):
        raise ValueError("channel matrix must be non-empty and square")
    for row in P:
        for v in row:
            if isinstance(v, bool) or not isinstance(v, (int, float)):
                raise ValueError("channel entries must be real numbers")
            if not 0.0 <= v <= 1.0:
                raise ValueError("channel entries must be in [0, 1]")
        if abs(sum(row) - 1.0) > 1e-9:
            raise ValueError("each channel row must sum to 1")
    return len(P)


def validate_distribution(pi, k):
    """Validate a probability vector of length k."""
    if len(pi) != k:
        raise ValueError(f"distribution must have length {k}")
    for v in pi:
        if isinstance(v, bool) or not isinstance(v, (int, float)):
            raise ValueError("distribution entries must be real numbers")
        if v < 0.0:
            raise ValueError("distribution entries must be non-negative")
    if abs(sum(pi) - 1.0) > 1e-9:
        raise ValueError("distribution must sum to 1")


def output_probabilities(P, pi):
    """Theoretical output distribution q = P^T pi."""
    k = validate_channel(P)
    validate_distribution(pi, k)
    return [sum(P[i][j] * pi[i] for i in range(k)) for j in range(k)]


def _sample_symbol(rng, weights):
    r = rng.random()
    acc = 0.0
    for j, w in enumerate(weights):
        acc += w
        if r <= acc:
            return j
    return len(weights) - 1


def simulate(rng, pi, P, n):
    """Draw ``n`` i.i.d. synthetic samples: input ~ pi, output ~ P[input].

    Returns the list of output counts of length k.
    """
    k = validate_channel(P)
    validate_distribution(pi, k)
    if isinstance(n, bool) or not isinstance(n, int) or n < 0:
        raise ValueError("n must be a non-negative integer")
    counts = [0] * k
    for _ in range(n):
        i = _sample_symbol(rng, pi)
        j = _sample_symbol(rng, P[i])
        counts[j] += 1
    return counts
