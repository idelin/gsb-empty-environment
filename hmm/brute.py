"""Brute-force enumeration over all state paths (small models only).

Used by the test-suite as ground truth for the DP-based inference code.
"""
from __future__ import annotations

import itertools
import math
from dataclasses import dataclass
from typing import Optional

from .infer import NEG_INF, _resolve_raw_transitions, logsumexp
from .model import HMM


def enumerate_paths(model: HMM, observations, transitions=None, constraints=None):
    """Return ``[(path, log_joint), ...]`` over every allowed state path."""
    obs = list(observations)
    T = len(obs)
    n = model.n_states
    trans = _resolve_raw_transitions(model, T, transitions)
    if constraints is None:
        cons = [None] * T
    else:
        if len(constraints) != T:
            raise ValueError(f"expected {T} constraint entries")
        cons = list(constraints)
    allowed = [
        sorted(c) if c is not None else list(range(n)) for c in cons
    ]
    if T == 0:
        return [((), 0.0)]
    out = []
    for path in itertools.product(*allowed):
        p = model.initial[path[0]] * model.emission_prob(path[0], obs[0])
        for t in range(1, T):
            if p == 0.0:
                break
            p *= trans[t - 1][path[t - 1]][path[t]] * model.emission_prob(
                path[t], obs[t]
            )
        out.append((path, NEG_INF if p == 0.0 else math.log(p)))
    return out


@dataclass(frozen=True)
class BruteForceResult:
    """Exact results obtained by explicit enumeration."""

    kind: str  # always "exact"
    feasible: bool
    log_evidence: float
    posteriors: Optional[tuple]
    best_path: Optional[tuple]
    best_logprob: float
    path_log_joints: tuple  # (path, log_joint) sorted by score, descending


def brute_force(model: HMM, observations, transitions=None, constraints=None) -> BruteForceResult:
    obs = list(observations)
    T = len(obs)
    entries = enumerate_paths(model, obs, transitions, constraints)
    if T == 0:
        return BruteForceResult(
            kind="exact",
            feasible=True,
            log_evidence=0.0,
            posteriors=(),
            best_path=(),
            best_logprob=0.0,
            path_log_joints=(((), 0.0),),
        )
    evidence = logsumexp(lj for _, lj in entries)
    feasible = evidence != NEG_INF
    posteriors = None
    if feasible:
        rows = []
        for t in range(T):
            row = []
            for j in range(model.n_states):
                lj = logsumexp(
                    lj for path, lj in entries if path[t] == j
                )
                row.append(math.exp(lj - evidence))
            rows.append(tuple(row))
        posteriors = tuple(rows)
    ranked = sorted(entries, key=lambda kv: kv[1], reverse=True)
    best_path, best_lp = ranked[0]
    if best_lp == NEG_INF:
        best_path, best_lp = None, NEG_INF
    return BruteForceResult(
        kind="exact",
        feasible=feasible,
        log_evidence=evidence,
        posteriors=posteriors,
        best_path=best_path,
        best_logprob=best_lp,
        path_log_joints=tuple(ranked),
    )
