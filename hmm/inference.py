"""Exact inference for finite HMMs.

All forward/backward recursions use per-step scaling so that sequences of
arbitrary length do not underflow.  A sequence whose total probability is
zero is reported as *infeasible* (``feasible=False``); posteriors are then
``None`` instead of a renormalised pseudo-posterior.

Conventions for the empty sequence: the marginal evidence is 1.0
(log-evidence 0.0), posteriors are the empty list, and the optimal path is
the empty path with log-probability 0.0.
"""
from __future__ import annotations

import math
from dataclasses import dataclass

NEG_INF = float("-inf")


@dataclass
class ForwardBackwardResult:
    feasible: bool
    log_evidence: float
    posteriors: list[list[float]] | None  # gamma[t][state]; None if infeasible
    alpha: list[list[float]] | None = None  # scaled forward messages
    beta: list[list[float]] | None = None   # scaled backward messages
    scale: list[float] | None = None        # per-step scaling coefficients c_t


@dataclass
class ViterbiResult:
    feasible: bool
    path: list[int] | None  # None if no path has positive probability
    log_prob: float         # NEG_INF if infeasible


@dataclass
class ScoredPath:
    path: list[int]
    log_prob: float

    @property
    def prob(self) -> float:
        return math.exp(self.log_prob)


def _log(x: float) -> float:
    return NEG_INF if x <= 0.0 else math.log(x)


def _emission_row(model, obs, n: int) -> list[float]:
    if obs is None:
        return [1.0] * n
    return [model.emission_prob(s, obs) for s in range(n)]


def _allowed(allowed_states, t: int):
    if allowed_states is None:
        return None
    return allowed_states[t]


def forward_messages(model, observations, allowed_states=None):
    """Scaled forward messages.  Returns (alpha, c); c[t] == 0.0 marks an
    infeasible prefix (total probability zero up to and including t)."""
    obs = list(observations)
    n = model.n_states
    T = len(obs)
    if T == 0:
        return [], []
    alpha = [[0.0] * n for _ in range(T)]
    c = [0.0] * T
    e = _emission_row(model, obs[0], n)
    allow = _allowed(allowed_states, 0)
    for i in range(n):
        if allow is not None and i not in allow:
            continue
        alpha[0][i] = model.initial[i] * e[i]
    c[0] = sum(alpha[0])
    if c[0] == 0.0:
        return alpha, c
    alpha[0] = [a / c[0] for a in alpha[0]]
    for t in range(1, T):
        A = model.transition_matrix(t - 1)
        e = _emission_row(model, obs[t], n)
        allow = _allowed(allowed_states, t)
        for j in range(n):
            if allow is not None and j not in allow:
                continue
            if e[j] == 0.0:
                continue
            acc = 0.0
            for i in range(n):
                if alpha[t - 1][i] != 0.0 and A[i][j] != 0.0:
                    acc += alpha[t - 1][i] * A[i][j]
            alpha[t][j] = acc * e[j]
        c[t] = sum(alpha[t])
        if c[t] == 0.0:
            return alpha, c
        alpha[t] = [a / c[t] for a in alpha[t]]
    return alpha, c


def backward_messages(model, observations, c, allowed_states=None):
    """Scaled backward messages, given the forward scaling coefficients."""
    obs = list(observations)
    n = model.n_states
    T = len(obs)
    if T == 0:
        return []
    beta = [[0.0] * n for _ in range(T)]
    beta[T - 1] = [1.0] * n
    for t in range(T - 2, -1, -1):
        A = model.transition_matrix(t)
        e = _emission_row(model, obs[t + 1], n)
        allow_next = _allowed(allowed_states, t + 1)
        scale = c[t + 1]
        for i in range(n):
            acc = 0.0
            for j in range(n):
                if allow_next is not None and j not in allow_next:
                    continue
                if A[i][j] != 0.0 and e[j] != 0.0 and beta[t + 1][j] != 0.0:
                    acc += A[i][j] * e[j] * beta[t + 1][j]
            beta[t][i] = acc / scale
    return beta


def forward_backward(model, observations, allowed_states=None) -> ForwardBackwardResult:
    """Scaled forward-backward.  ``allowed_states[t]`` optionally restricts the
    state at time t to a set of indices (hard path constraints)."""
    obs = list(observations)
    T = len(obs)
    if T == 0:
        return ForwardBackwardResult(True, 0.0, [], [], [], [])
    alpha, c = forward_messages(model, obs, allowed_states)
    if any(ct == 0.0 for ct in c):
        # Zero-probability sequence: explicitly infeasible, no pseudo-posterior.
        return ForwardBackwardResult(False, NEG_INF, None, alpha, None, c)
    beta = backward_messages(model, obs, c, allowed_states)
    posteriors = []
    for t in range(T):
        row = [alpha[t][i] * beta[t][i] for i in range(model.n_states)]
        total = sum(row)
        if total <= 0.0:
            return ForwardBackwardResult(False, NEG_INF, None, alpha, beta, c)
        posteriors.append([x / total for x in row])
    log_evidence = sum(math.log(ct) for ct in c)  # evidence = prod_t c_t
    return ForwardBackwardResult(True, log_evidence, posteriors, alpha, beta, c)


def viterbi(model, observations, allowed_states=None) -> ViterbiResult:
    """Most likely single state path (log-space, no underflow)."""
    obs = list(observations)
    n = model.n_states
    T = len(obs)
    if T == 0:
        return ViterbiResult(True, [], 0.0)
    delta = [[NEG_INF] * n for _ in range(T)]
    psi = [[-1] * n for _ in range(T)]
    e = _emission_row(model, obs[0], n)
    allow = _allowed(allowed_states, 0)
    for i in range(n):
        if allow is not None and i not in allow:
            continue
        if model.initial[i] > 0.0 and e[i] > 0.0:
            delta[0][i] = math.log(model.initial[i]) + math.log(e[i])
    for t in range(1, T):
        A = model.transition_matrix(t - 1)
        e = _emission_row(model, obs[t], n)
        allow = _allowed(allowed_states, t)
        for j in range(n):
            if allow is not None and j not in allow:
                continue
            if e[j] == 0.0:
                continue
            best, arg = NEG_INF, -1
            for i in range(n):
                if delta[t - 1][i] == NEG_INF or A[i][j] == 0.0:
                    continue
                cand = delta[t - 1][i] + math.log(A[i][j])
                if cand > best:
                    best, arg = cand, i
            if arg >= 0:
                delta[t][j] = best + math.log(e[j])
                psi[t][j] = arg
    best, last = NEG_INF, -1
    for i in range(n):
        if delta[T - 1][i] > best:
            best, last = delta[T - 1][i], i
    if last < 0:
        # No path with positive probability (e.g. impossible observation).
        return ViterbiResult(False, None, NEG_INF)
    path = [last]
    for t in range(T - 1, 0, -1):
        path.append(psi[t][path[-1]])
    path.reverse()
    return ViterbiResult(True, path, best)


def pointwise_map(posteriors: list[list[float]]) -> list[int]:
    """State-wise argmax of the posterior marginals.

    Note: concatenating per-time MAP states is in general *not* the Viterbi
    path; it may even have probability zero under the transition model.
    """
    return [max(range(len(row)), key=lambda i: row[i]) for row in posteriors]


def topk_paths(model, observations, k: int, allowed_states=None) -> list[ScoredPath]:
    """The k highest-probability *distinct* state paths, sorted best-first.

    Ranking (sorting) happens here; accumulating the probability mass of a
    collection of paths is a separate concern, see
    :func:`accumulate_log_probability`.
    """
    if k < 1:
        raise ValueError("k must be >= 1")
    obs = list(observations)
    n = model.n_states
    T = len(obs)
    if T == 0:
        return [ScoredPath([], 0.0)]
    prev: list[list[tuple[float, tuple]]] = [[] for _ in range(n)]
    e = _emission_row(model, obs[0], n)
    allow = _allowed(allowed_states, 0)
    for i in range(n):
        if allow is not None and i not in allow:
            continue
        lp = _log(model.initial[i]) + _log(e[i])
        if lp > NEG_INF:
            prev[i] = [(lp, (i,))]
    for t in range(1, T):
        A = model.transition_matrix(t - 1)
        e = _emission_row(model, obs[t], n)
        allow = _allowed(allowed_states, t)
        cur: list[list[tuple[float, tuple]]] = [[] for _ in range(n)]
        for j in range(n):
            if allow is not None and j not in allow:
                continue
            le = _log(e[j])
            if le == NEG_INF:
                continue
            pool = []
            for i in range(n):
                if not prev[i] or A[i][j] == 0.0:
                    continue
                la = math.log(A[i][j])
                for lp, path in prev[i]:
                    pool.append((lp + la + le, path + (j,)))
            pool.sort(key=lambda item: item[0], reverse=True)
            cur[j] = pool[:k]
        prev = cur
    final = [cand for lst in prev for cand in lst]
    final.sort(key=lambda item: item[0], reverse=True)
    return [ScoredPath(list(path), lp) for lp, path in final[:k]]


def accumulate_log_probability(log_probs) -> float:
    """Total probability mass (log-sum-exp) of a collection of paths.

    Deliberately independent of any ranking: pass any iterable of path
    log-probabilities, sorted or not.
    """
    vals = list(log_probs)
    if not vals:
        return NEG_INF
    m = max(vals)
    if m == NEG_INF:
        return NEG_INF
    return m + math.log(sum(math.exp(v - m) for v in vals))
