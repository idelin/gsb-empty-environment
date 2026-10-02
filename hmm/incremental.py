"""Versioned observations with incremental forward/backward message updates.

A local observation revision at position p only invalidates the forward
messages from p onward and the backward messages from p backward; the rest
are reused from the parent version.  Every version remains queryable, so
results computed before a revision are still available afterwards.
"""
from __future__ import annotations

import math
from dataclasses import dataclass

from .inference import (
    NEG_INF,
    _emission_row,
    backward_messages,
    forward_messages,
)


@dataclass
class Messages:
    alpha: list[list[float]] | None
    beta: list[list[float]] | None
    scale: list[float]
    feasible: bool


class VersionedObservations:
    """An observation sequence under revision control, bound to one HMM."""

    def __init__(self, model, observations):
        self.model = model
        self._obs: dict[int, tuple] = {0: tuple(observations)}
        self._edits: dict[int, tuple[int, int]] = {}  # v -> (parent, position)
        self._cache: dict[int, Messages] = {}
        self._latest = 0

    @property
    def latest_version(self) -> int:
        return self._latest

    def observations(self, version: int | None = None) -> tuple:
        v = self._latest if version is None else version
        return self._obs[v]

    def revise(self, position: int, value) -> int:
        """Create a new version with observations[position] replaced."""
        parent = self._latest
        obs = list(self._obs[parent])
        if not 0 <= position < len(obs):
            raise IndexError(f"position {position} out of range for length {len(obs)}")
        obs[position] = value
        v = max(self._obs) + 1
        self._obs[v] = tuple(obs)
        self._edits[v] = (parent, position)
        self._latest = v
        return v

    def messages(self, version: int | None = None) -> Messages:
        v = self._latest if version is None else version
        if v not in self._cache:
            self._cache[v] = self._compute(v)
        return self._cache[v]

    def _compute(self, v: int) -> Messages:
        obs = list(self._obs[v])
        T = len(obs)
        if v == 0 or T == 0:
            alpha, c = forward_messages(self.model, obs)
            if any(ct == 0.0 for ct in c):
                return Messages(alpha, None, c, False)
            beta = backward_messages(self.model, obs, c)
            return Messages(alpha, beta, c, True)
        parent, pos = self._edits[v]
        base = self.messages(parent)
        n = self.model.n_states
        # Forward: prefix before pos is unchanged; recompute pos..T-1.
        alpha = [row[:] for row in base.alpha[:pos]] if base.alpha else []
        c = list(base.scale[:pos])
        for t in range(pos, T):
            row = [0.0] * n
            e = _emission_row(self.model, obs[t], n)
            if t == 0:
                for i in range(n):
                    row[i] = self.model.initial[i] * e[i]
            else:
                A = self.model.transition_matrix(t - 1)
                prev = alpha[t - 1]
                for j in range(n):
                    if e[j] == 0.0:
                        continue
                    acc = 0.0
                    for i in range(n):
                        if prev[i] != 0.0 and A[i][j] != 0.0:
                            acc += prev[i] * A[i][j]
                    row[j] = acc * e[j]
            ct = sum(row)
            alpha.append(row)
            c.append(ct)
            if ct == 0.0:
                # Infeasible from here on; remaining messages stay zero.
                for _ in range(t + 1, T):
                    alpha.append([0.0] * n)
                    c.append(0.0)
                return Messages(alpha, None, c, False)
            alpha[t] = [a / ct for a in row]
        # Backward: suffix after pos is inherited from the parent version and
        # only pos..0 is recomputed.  Inherited rows follow the parent's
        # scaling convention, so a stored beta row can differ from a fresh
        # scaled beta by a per-t constant; posteriors are exact because they
        # are renormalised per time step.
        if base.beta is None:
            beta = backward_messages(self.model, obs, c)
        else:
            beta = [row[:] for row in base.beta]
            for t in range(min(pos, T - 2), -1, -1):
                A = self.model.transition_matrix(t)
                e = _emission_row(self.model, obs[t + 1], n)
                scale = c[t + 1]
                for i in range(n):
                    acc = 0.0
                    for j in range(n):
                        if A[i][j] != 0.0 and e[j] != 0.0 and beta[t + 1][j] != 0.0:
                            acc += A[i][j] * e[j] * beta[t + 1][j]
                    beta[t][i] = acc / scale
        return Messages(alpha, beta, c, True)

    def log_evidence(self, version: int | None = None) -> float:
        m = self.messages(version)
        if not m.feasible:
            return NEG_INF
        return sum(math.log(ct) for ct in m.scale)  # evidence = prod_t c_t

    def posteriors(self, version: int | None = None):
        """Posterior marginals for a version; None if that version is
        infeasible (zero-probability sequence, never renormalised)."""
        m = self.messages(version)
        if not m.feasible:
            return None
        out = []
        for a_row, b_row in zip(m.alpha, m.beta):
            row = [a * b for a, b in zip(a_row, b_row)]
            total = sum(row)
            if total <= 0.0:
                return None
            out.append([x / total for x in row])
        return out
