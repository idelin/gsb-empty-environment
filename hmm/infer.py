"""Exact inference for finite HMMs.

The core recursions run in log space, so arbitrarily long observation
sequences cannot underflow.  A Rabiner-style scaled forward-backward
(:func:`forward_backward_scaled`) is provided as an independent
implementation and is used by the test-suite to cross-validate the
log-space results.

Conventions
-----------
- Missing observations (``hmm.MISSING``) carry no information: their
  emission probability is 1 in every state.
- ``constraints`` is an optional per-time list of allowed-state sets
  (or ``None`` for "no constraint").  An empty set is a usage error.
- ``transitions`` is an optional list of ``T - 1`` row-stochastic matrices
  (piecewise/segmented dynamics); the model's own matrix is used by default.
- A zero-probability observation sequence is *infeasible*: the log-evidence
  is ``-inf`` and no posterior is produced.  We never renormalize an
  all-zero message vector into a fake posterior.
- Empty sequence convention: the evidence is 1 (log-evidence 0), the
  posterior list is empty and the Viterbi path is the empty tuple with
  log-probability 0.
"""
from __future__ import annotations

import math
from dataclasses import dataclass
from typing import Optional, Sequence

from .model import HMM, validate_transition_matrix

NEG_INF = float("-inf")


def logsumexp(values) -> float:
    vals = list(values)
    best = max(vals, default=NEG_INF)
    if best == NEG_INF:
        return NEG_INF
    return best + math.log(sum(math.exp(v - best) for v in vals))


def _log_matrix(matrix):
    return tuple(
        tuple(NEG_INF if a <= 0.0 else math.log(a) for a in row) for row in matrix
    )


def _resolve_raw_transitions(model: HMM, T: int, transitions):
    steps = max(T - 1, 0)
    if transitions is None:
        return [model.transition] * steps
    if len(transitions) != steps:
        raise ValueError(
            f"expected {steps} transition matrices for T={T}, got {len(transitions)}"
        )
    return [
        validate_transition_matrix(m, model.n_states, f"transitions[{k}]")
        for k, m in enumerate(transitions)
    ]


def _resolve_transitions(model: HMM, T: int, transitions):
    return [_log_matrix(m) for m in _resolve_raw_transitions(model, T, transitions)]


def _resolve_constraints(model: HMM, T: int, constraints):
    if constraints is None:
        return [None] * T
    if len(constraints) != T:
        raise ValueError(f"expected {T} constraint entries, got {len(constraints)}")
    resolved = []
    for t, allowed in enumerate(constraints):
        if allowed is None:
            resolved.append(None)
            continue
        s = frozenset(allowed)
        if not s:
            raise ValueError(
                f"constraints[{t}] is an empty allowed-state set; "
                "infeasibility must come from observations, not constraints"
            )
        bad = s - set(range(model.n_states))
        if bad:
            raise ValueError(f"constraints[{t}] mentions unknown states: {sorted(bad)}")
        resolved.append(s)
    return resolved


def _log_emissions(model: HMM, obs, cons):
    table = []
    for t, o in enumerate(obs):
        allowed = cons[t]
        row = []
        for j in range(model.n_states):
            if allowed is not None and j not in allowed:
                row.append(NEG_INF)
                continue
            e = model.emission_prob(j, o)
            row.append(NEG_INF if e <= 0.0 else math.log(e))
        table.append(tuple(row))
    return table


def _log_initial(model: HMM):
    return [NEG_INF if p <= 0.0 else math.log(p) for p in model.initial]


def forward(model: HMM, observations, transitions=None, constraints=None):
    """Log-space forward pass.  Returns ``(log_alpha, log_evidence)``."""
    obs = list(observations)
    T = len(obs)
    n = model.n_states
    log_trans = _resolve_transitions(model, T, transitions)
    cons = _resolve_constraints(model, T, constraints)
    log_emit = _log_emissions(model, obs, cons)
    log_init = _log_initial(model)
    log_alpha = []
    for t in range(T):
        row = []
        for j in range(n):
            le = log_emit[t][j]
            if le == NEG_INF:
                row.append(NEG_INF)
            elif t == 0:
                row.append(log_init[j] + le if log_init[j] != NEG_INF else NEG_INF)
            else:
                prev = log_alpha[t - 1]
                A = log_trans[t - 1]
                row.append(logsumexp(prev[i] + A[i][j] for i in range(n)) + le)
        log_alpha.append(tuple(row))
    evidence = logsumexp(log_alpha[-1]) if T else 0.0
    return log_alpha, evidence


def backward(model: HMM, observations, transitions=None, constraints=None):
    """Log-space backward pass.  Returns ``log_beta``."""
    obs = list(observations)
    T = len(obs)
    n = model.n_states
    log_trans = _resolve_transitions(model, T, transitions)
    cons = _resolve_constraints(model, T, constraints)
    log_emit = _log_emissions(model, obs, cons)
    if T == 0:
        return []
    log_beta = [None] * T
    log_beta[T - 1] = tuple(0.0 for _ in range(n))
    for t in range(T - 2, -1, -1):
        A = log_trans[t]
        nxt = log_beta[t + 1]
        emit_next = log_emit[t + 1]
        log_beta[t] = tuple(
            logsumexp(A[i][j] + emit_next[j] + nxt[j] for j in range(n))
            for i in range(n)
        )
    return log_beta


def posteriors_from_messages(log_alpha, log_beta, log_evidence):
    """Normalize messages into smoothed posteriors.

    Returns ``None`` when the sequence is infeasible (log-evidence ``-inf``):
    an all-zero message vector is never renormalized into a fake posterior.
    """
    if log_evidence == NEG_INF:
        return None
    out = []
    for a_row, b_row in zip(log_alpha, log_beta):
        out.append(
            tuple(
                math.exp(a + b - log_evidence) if a + b != NEG_INF else 0.0
                for a, b in zip(a_row, b_row)
            )
        )
    return tuple(out)


def viterbi(model: HMM, observations, transitions=None, constraints=None):
    """Log-space Viterbi.  Returns ``(path, log_joint)``.

    ``path`` is ``None`` (with log-probability ``-inf``) when no state path
    can produce the observations.
    """
    obs = list(observations)
    T = len(obs)
    n = model.n_states
    if T == 0:
        return (), 0.0
    log_trans = _resolve_transitions(model, T, transitions)
    cons = _resolve_constraints(model, T, constraints)
    log_emit = _log_emissions(model, obs, cons)
    log_init = _log_initial(model)
    delta = [
        log_init[j] + log_emit[0][j]
        if log_init[j] != NEG_INF and log_emit[0][j] != NEG_INF
        else NEG_INF
        for j in range(n)
    ]
    psi = [[0] * n for _ in range(T)]
    for t in range(1, T):
        A = log_trans[t - 1]
        new = []
        for j in range(n):
            le = log_emit[t][j]
            if le == NEG_INF:
                new.append(NEG_INF)
                continue
            best_val, best_i = NEG_INF, 0
            for i in range(n):
                v = delta[i] + A[i][j]
                if v > best_val:
                    best_val, best_i = v, i
            psi[t][j] = best_i
            new.append(best_val + le)
        delta = new
    best = max(delta)
    if best == NEG_INF:
        return None, NEG_INF
    last = delta.index(best)
    path = [last]
    for t in range(T - 1, 0, -1):
        last = psi[t][last]
        path.append(last)
    path.reverse()
    return tuple(path), best


@dataclass(frozen=True)
class InferenceResult:
    """Exact inference outcome.  ``kind`` is always ``"exact"``."""

    kind: str
    feasible: bool
    log_evidence: float
    posteriors: Optional[tuple]  # None when infeasible
    viterbi_path: Optional[tuple]  # None when infeasible
    viterbi_logprob: float


def forward_backward(model: HMM, observations, transitions=None, constraints=None) -> InferenceResult:
    """Full exact inference: evidence, smoothed posteriors and Viterbi path."""
    log_alpha, log_evidence = forward(model, observations, transitions, constraints)
    log_beta = backward(model, observations, transitions, constraints)
    feasible = log_evidence != NEG_INF
    post = posteriors_from_messages(log_alpha, log_beta, log_evidence)
    path, lp = viterbi(model, observations, transitions, constraints)
    return InferenceResult(
        kind="exact",
        feasible=feasible,
        log_evidence=log_evidence,
        posteriors=post,
        viterbi_path=path,
        viterbi_logprob=lp,
    )


def forward_backward_scaled(model: HMM, observations):
    """Rabiner-scaled forward-backward (independent cross-check implementation).

    Returns ``(log_evidence, gamma)`` where ``gamma`` rows are smoothed
    posteriors, or ``(NEG_INF, None)`` when the sequence is infeasible.
    Only the model's own transition matrix is used (no piecewise dynamics).
    """
    obs = list(observations)
    T = len(obs)
    n = model.n_states
    if T == 0:
        return 0.0, ()
    alpha = []
    scales = []
    row = [model.initial[j] * model.emission_prob(j, obs[0]) for j in range(n)]
    c = sum(row)
    if c == 0.0:
        return NEG_INF, None
    alpha.append([v / c for v in row])
    scales.append(c)
    for t in range(1, T):
        row = []
        for j in range(n):
            e = model.emission_prob(j, obs[t])
            row.append(
                e * sum(alpha[t - 1][i] * model.transition[i][j] for i in range(n))
            )
        c = sum(row)
        if c == 0.0:
            return NEG_INF, None
        alpha.append([v / c for v in row])
        scales.append(c)
    log_evidence = sum(math.log(c) for c in scales)
    beta = [None] * T
    beta[T - 1] = [1.0] * n
    for t in range(T - 2, -1, -1):
        beta[t] = [
            sum(
                model.transition[i][j] * model.emission_prob(j, obs[t + 1]) * beta[t + 1][j]
                for j in range(n)
            )
            / scales[t + 1]
            for i in range(n)
        ]
    gamma = tuple(
        tuple(a * b for a, b in zip(a_row, b_row)) for a_row, b_row in zip(alpha, beta)
    )
    return log_evidence, gamma
