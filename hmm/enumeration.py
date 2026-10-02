"""Brute-force enumeration over all state paths.

Exponential in the sequence length; intended as ground truth to cross-check
the dynamic-programming implementations on small models.
"""
from __future__ import annotations

import math
from itertools import product

NEG_INF = float("-inf")


def path_probability(model, path, observations) -> float:
    """Unnormalised joint probability P(path, observations)."""
    if len(path) != len(observations):
        raise ValueError("path and observations must have equal length")
    if not path:
        return 1.0
    prob = model.initial[path[0]] * model.emission_prob(path[0], observations[0])
    for t in range(1, len(path)):
        if prob == 0.0:
            return 0.0
        A = model.transition_matrix(t - 1)
        prob *= A[path[t - 1]][path[t]] * model.emission_prob(path[t], observations[t])
    return prob


def enumerate_paths(model, observations, allowed_states=None):
    """Yield (path_tuple, joint_probability) for every state path."""
    obs = list(observations)
    T = len(obs)
    n = model.n_states
    if T == 0:
        yield (), 1.0
        return
    domains = []
    for t in range(T):
        if allowed_states is not None and allowed_states[t] is not None:
            domains.append(sorted(allowed_states[t]))
        else:
            domains.append(range(n))
    for path in product(*domains):
        yield path, path_probability(model, list(path), obs)


def exact_evidence(model, observations, allowed_states=None) -> float:
    return sum(p for _, p in enumerate_paths(model, observations, allowed_states))


def exact_posteriors(model, observations, allowed_states=None):
    """Posterior marginals from full enumeration; None if evidence is zero."""
    obs = list(observations)
    T = len(obs)
    n = model.n_states
    if T == 0:
        return []
    totals = [[0.0] * n for _ in range(T)]
    evidence = 0.0
    for path, p in enumerate_paths(model, obs, allowed_states):
        evidence += p
        for t, s in enumerate(path):
            totals[t][s] += p
    if evidence == 0.0:
        return None
    return [[x / evidence for x in row] for row in totals]


def exact_topk(model, observations, k: int, allowed_states=None):
    """k best distinct paths as (path_tuple, probability), sorted best-first.

    Ties are broken by path tuple for determinism.
    """
    scored = [
        (path, p)
        for path, p in enumerate_paths(model, observations, allowed_states)
        if p > 0.0
    ]
    scored.sort(key=lambda item: (-item[1], item[0]))
    return scored[:k]


def exact_viterbi(model, observations, allowed_states=None):
    """Best path as (path_tuple, probability); (None, 0.0) if none exists."""
    best = exact_topk(model, observations, 1, allowed_states)
    if not best:
        return None, 0.0
    return best[0]


def log_of(prob: float) -> float:
    return NEG_INF if prob <= 0.0 else math.log(prob)
