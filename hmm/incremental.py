"""Incremental, versioned inference under local observation revisions.

``VersionedInference`` caches forward/backward messages per version.  When a
single observation is revised, only the affected message rows are
recomputed: forward rows ``t .. T-1`` from the stored ``alpha[t-1]`` and
backward rows ``0 .. t`` from the stored ``beta[t+1]``.  Every version is
immutable, so queries against older versions keep returning the original
answers.  (Memory grows with the number of retained versions.)
"""
from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Optional

from .infer import NEG_INF, backward, forward, logsumexp, posteriors_from_messages
from .model import HMM


@dataclass(frozen=True)
class _Version:
    version: int
    observations: tuple
    log_alpha: tuple
    log_beta: tuple
    log_evidence: float


class VersionedInference:
    """Versioned forward/backward messages over a fixed observation grid."""

    def __init__(self, model: HMM, observations):
        self._model = model
        obs = tuple(observations)
        log_alpha, log_evidence = forward(model, obs)
        log_beta = backward(model, obs)
        self._versions = [
            _Version(0, obs, tuple(log_alpha), tuple(log_beta), log_evidence)
        ]

    @property
    def version(self) -> int:
        return self._versions[-1].version

    def versions(self):
        return [v.version for v in self._versions]

    def _log_emit(self, obs, j: int) -> float:
        e = self._model.emission_prob(j, obs)
        return NEG_INF if e <= 0.0 else math.log(e)

    def revise(self, t: int, new_observation) -> int:
        """Revise the observation at time ``t``; returns the new version id."""
        model = self._model
        prev = self._versions[-1]
        T = len(prev.observations)
        if not 0 <= t < T:
            raise IndexError(f"no observation at time {t} (T={T})")
        obs = list(prev.observations)
        obs[t] = new_observation
        obs = tuple(obs)
        n = model.n_states
        logA = [
            [NEG_INF if a <= 0.0 else math.log(a) for a in row]
            for row in model.transition
        ]
        log_init = [
            NEG_INF if p <= 0.0 else math.log(p) for p in model.initial
        ]
        # Forward: rows before t are reused untouched; recompute t .. T-1.
        alpha = list(prev.log_alpha[:t])
        for s in range(t, T):
            row = []
            for j in range(n):
                le = self._log_emit(obs[s], j)
                if le == NEG_INF:
                    row.append(NEG_INF)
                elif s == 0:
                    row.append(
                        log_init[j] + le if log_init[j] != NEG_INF else NEG_INF
                    )
                else:
                    prev_row = alpha[s - 1]
                    row.append(
                        logsumexp(prev_row[i] + logA[i][j] for i in range(n)) + le
                    )
            alpha.append(tuple(row))
        # Backward: rows after t are reused untouched; recompute t .. 0.
        beta = [None] * T
        for s in range(t + 1, T):
            beta[s] = prev.log_beta[s]
        for s in range(min(t, T - 1), -1, -1):
            if s == T - 1:
                beta[s] = tuple(0.0 for _ in range(n))
            else:
                emit_next = [self._log_emit(obs[s + 1], j) for j in range(n)]
                beta[s] = tuple(
                    logsumexp(
                        logA[i][j] + emit_next[j] + beta[s + 1][j] for j in range(n)
                    )
                    for i in range(n)
                )
        evidence = logsumexp(alpha[-1]) if T else 0.0
        vid = prev.version + 1
        self._versions.append(
            _Version(vid, obs, tuple(alpha), tuple(beta), evidence)
        )
        return vid

    def _get(self, version: Optional[int]) -> _Version:
        if version is None:
            return self._versions[-1]
        for v in self._versions:
            if v.version == version:
                return v
        raise KeyError(f"unknown version {version}")

    def observations(self, version: Optional[int] = None) -> tuple:
        return self._get(version).observations

    def log_evidence(self, version: Optional[int] = None) -> float:
        return self._get(version).log_evidence

    def posteriors(self, version: Optional[int] = None):
        """Smoothed posteriors for a version; ``None`` when infeasible."""
        v = self._get(version)
        return posteriors_from_messages(v.log_alpha, v.log_beta, v.log_evidence)

    def posterior(self, t: int, version: Optional[int] = None):
        post = self.posteriors(version)
        return None if post is None else post[t]
