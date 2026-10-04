"""Top-K distinct state paths.

The two stages are deliberately separated:

1. :func:`rank_topk_paths` ranks paths by their joint score with a K-best
   dynamic program over the trellis (exact, because the trellis is a DAG).
2. :func:`accumulate_path_probabilities` independently recomputes each
   ranked path's probability from scratch and accumulates the probability
   mass — it does not trust the DP scores.
"""
from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Optional

from .infer import (
    NEG_INF,
    _log_emissions,
    _log_initial,
    _resolve_constraints,
    _resolve_raw_transitions,
    _resolve_transitions,
    forward,
)
from .model import HMM


@dataclass(frozen=True)
class RankedPath:
    """A path produced by the ranking stage (score = log joint)."""

    rank: int
    path: tuple
    log_joint: float


def rank_topk_paths(model: HMM, observations, k: int, transitions=None, constraints=None):
    """Return the ``k`` highest-scoring distinct state paths (exact K-best DP)."""
    if k < 1:
        raise ValueError(f"k must be >= 1, got {k}")
    obs = list(observations)
    T = len(obs)
    n = model.n_states
    if T == 0:
        return [RankedPath(rank=0, path=(), log_joint=0.0)]
    log_trans = _resolve_transitions(model, T, transitions)
    cons = _resolve_constraints(model, T, constraints)
    log_emit = _log_emissions(model, obs, cons)
    log_init = _log_initial(model)
    cand = []
    for j in range(n):
        s = log_init[j] + log_emit[0][j]
        cand.append([] if s == NEG_INF else [(s, (j,))])
    for t in range(1, T):
        A = log_trans[t - 1]
        new = []
        for j in range(n):
            le = log_emit[t][j]
            if le == NEG_INF:
                new.append([])
                continue
            merged = []
            for i in range(n):
                a = A[i][j]
                if a == NEG_INF:
                    continue
                for score, pth in cand[i]:
                    merged.append((score + a + le, pth + (j,)))
            merged.sort(key=lambda sp: sp[0], reverse=True)
            new.append(merged[:k])
        cand = new
    allpaths = [sp for c in cand for sp in c]
    allpaths.sort(key=lambda sp: sp[0], reverse=True)
    return [
        RankedPath(rank=r, path=p, log_joint=s)
        for r, (s, p) in enumerate(allpaths[:k])
    ]


def path_log_joint(model: HMM, observations, path, transitions=None) -> float:
    """Log joint ``log P(states=path, observations)`` computed from scratch."""
    obs = list(observations)
    T = len(obs)
    if len(path) != T:
        raise ValueError(f"path length {len(path)} != observation length {T}")
    if T == 0:
        return 0.0
    trans = _resolve_raw_transitions(model, T, transitions)
    total = 0.0
    for t in range(T):
        s = path[t]
        if not 0 <= s < model.n_states:
            raise ValueError(f"path mentions unknown state {s}")
        if t == 0:
            p = model.initial[s]
        else:
            p = trans[t - 1][path[t - 1]][s]
        e = model.emission_prob(s, obs[t])
        if p <= 0.0 or e <= 0.0:
            return NEG_INF
        total += math.log(p) + math.log(e)
    return total


@dataclass(frozen=True)
class PathProbability:
    rank: int
    path: tuple
    log_joint: float
    joint_prob: float
    conditional_prob: Optional[float]  # P(path | obs); None when infeasible
    cumulative_conditional: Optional[float]


@dataclass(frozen=True)
class PathAccumulation:
    """Probability accumulation over ranked paths.  ``kind`` is ``"exact"``."""

    kind: str
    feasible: bool
    log_evidence: float
    entries: tuple
    covered_conditional_mass: Optional[float]
    residual_conditional_mass: Optional[float]


def accumulate_path_probabilities(model: HMM, observations, ranked, transitions=None) -> PathAccumulation:
    """Attach probabilities to ranked paths and accumulate conditional mass.

    Because ``ranked`` holds the *top* paths, every unranked path has
    conditional probability at most that of the last ranked entry, and the
    residual mass is exactly ``1 - covered``.
    """
    _, log_evidence = forward(model, observations, transitions)
    feasible = log_evidence != NEG_INF
    entries = []
    cumulative = 0.0
    for rp in ranked:
        lj = path_log_joint(model, observations, rp.path, transitions)
        joint = math.exp(lj) if lj != NEG_INF else 0.0
        if feasible:
            cond = math.exp(lj - log_evidence) if lj != NEG_INF else 0.0
            cumulative += cond
        else:
            cond = None
        entries.append(
            PathProbability(
                rank=rp.rank,
                path=rp.path,
                log_joint=lj,
                joint_prob=joint,
                conditional_prob=cond,
                cumulative_conditional=cumulative if feasible else None,
            )
        )
    return PathAccumulation(
        kind="exact",
        feasible=feasible,
        log_evidence=log_evidence,
        entries=tuple(entries),
        covered_conditional_mass=cumulative if feasible else None,
        residual_conditional_mass=(1.0 - cumulative) if feasible else None,
    )
