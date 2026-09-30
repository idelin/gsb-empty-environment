"""End-to-end synthetic release pipeline.

Flow:
  1. synthetic_counts() builds a synthetic dataset with a hard contribution
     bound: each synthetic user adds +1 to at most `max_contribution`
     distinct leaf buckets, so every published count query has L1
     sensitivity `max_contribution`.
  2. publish() draws noise ONCE per measurement via a NoiseLedger and
     returns a Release holding the raw noisy measurements. The total
     target is either explicitly public (exact count) or a separately
     noised measurement with its own budget share -- never silently
     derived from the other noisy values.
  3. reconcile() post-processes the raw noisy measurements into a
     non-negative vector whose sum equals the total target, minimizing a
     rationally weighted quadratic under the linear consistency
     constraints, and attaches an independently checkable KKT
     certificate. Raw noisy values and post-processed values are kept in
     separate fields. An infeasible (negative) total is explicitly
     rejected; an exhausted iteration budget is reported as
     "not_converged" with no post-processed output.

Scope: synthetic data only. The epsilon split below is basic sequential
composition for bookkeeping; nothing here is a formal privacy proof.
"""
from __future__ import annotations

import random
from dataclasses import dataclass, field
from fractions import Fraction

from noise import NoiseLedger
from qp import (InfeasibleError, Publication, check_kkt, objective,
                project_simplex, solve_iterative)


@dataclass(frozen=True)
class BucketSchema:
    """Public fixed buckets: leaf buckets plus optional overlapping groups
    (each group is a named subset of leaf indices whose sum is published)."""
    leaf_names: tuple
    groups: tuple = ()  # ((group_name, (leaf_idx, ...)), ...)

    @property
    def n(self):
        return len(self.leaf_names)


def synthetic_counts(num_users, n_buckets, max_contribution, seed):
    """Synthetic counts with per-user contribution bounded by
    `max_contribution` distinct buckets, +1 each."""
    rng = random.Random(seed)
    counts = [0] * n_buckets
    for _ in range(num_users):
        k = rng.randint(1, max_contribution)
        for b in rng.sample(range(n_buckets), min(k, n_buckets)):
            counts[b] += 1
    return counts


@dataclass
class Release:
    """Raw noisy measurements plus everything needed to reproduce them."""
    schema: BucketSchema
    true_counts: list
    measurements: dict      # key -> {"noisy", "noise", "scale", "reused"}
    total_source: str       # "public" | "noised"
    total_value: int        # the total target handed to the projection
    scale: Fraction
    epsilon_each: Fraction


def publish(schema, counts, epsilon, ledger: NoiseLedger,
            max_contribution=1, total_source="public"):
    """Noisy release over the public schema. Noise is drawn once per key
    by the ledger; calling publish again with the same ledger reuses the
    stored draws. epsilon is split evenly over all measurements (basic
    composition)."""
    if total_source not in ("public", "noised"):
        raise ValueError("total_source must be 'public' or 'noised'")
    n_measurements = (schema.n + len(schema.groups)
                      + (1 if total_source == "noised" else 0))
    eps_each = Fraction(epsilon) / n_measurements
    scale = Fraction(max_contribution) / eps_each

    measurements = {}

    def record(key, true_value):
        noisy, noise, reused = ledger.noisy_value(key, true_value, scale)
        measurements[key] = {"noisy": noisy, "noise": noise,
                             "scale": str(scale), "reused": reused}

    for i, name in enumerate(schema.leaf_names):
        record(f"leaf:{name}", counts[i])
    for gname, members in schema.groups:
        record(f"group:{gname}", sum(counts[i] for i in members))
    if total_source == "noised":
        record("total", sum(counts))
        total_value = measurements["total"]["noisy"]
    else:
        total_value = sum(counts)

    return Release(schema, list(counts), measurements, total_source,
                   total_value, scale, eps_each)


def _publications(release: Release):
    weight = 1 / (release.scale * release.scale)
    pubs = []
    for i, name in enumerate(release.schema.leaf_names):
        m = release.measurements[f"leaf:{name}"]
        pubs.append(Publication(f"leaf:{name}", {i: Fraction(1)},
                                Fraction(m["noisy"]), weight))
    for gname, members in release.schema.groups:
        m = release.measurements[f"group:{gname}"]
        coeffs = {i: Fraction(1) for i in members}
        pubs.append(Publication(f"group:{gname}", coeffs,
                                Fraction(m["noisy"]), weight))
    return pubs


def reconcile(release: Release, max_iterations=1000):
    """Project raw noisy measurements onto {x >= 0, sum(x) = total}.

    Deterministic: no randomness is consumed here, so the projection can
    never resample until a pleasing result appears."""
    pubs = _publications(release)
    raw = {k: m["noisy"] for k, m in release.measurements.items()}
    result = {
        "raw_noisy": raw,
        "total_source": release.total_source,
        "total_target": release.total_value,
        "postprocessed": None,
    }
    try:
        sol = solve_iterative(pubs, release.schema.n, release.total_value,
                              max_iterations=max_iterations)
    except InfeasibleError as exc:
        result["status"] = "rejected_infeasible"
        result["reason"] = str(exc)
        return result
    if sol.status != "optimal":
        result["status"] = "not_converged"
        result["iterations"] = sol.iterations
        return result
    violations = check_kkt(pubs, release.schema.n, release.total_value,
                           sol.x, sol.lam, sol.mu)
    result["status"] = "optimal" if not violations else "certificate_invalid"
    result["iterations"] = sol.iterations
    result["objective"] = str(sol.objective_value)
    result["kkt_violations"] = violations
    result["certificate"] = {
        "x": [str(v) for v in sol.x],
        "lambda": str(sol.lam),
        "mu": [str(v) for v in sol.mu],
    }
    result["postprocessed"] = list(sol.x)
    return result


def simple_release(counts, epsilon, ledger, max_contribution=1,
                   total_source="public"):
    """Single-level convenience path: noisy counts projected onto the
    non-negative simplex with the given total (no overlapping groups)."""
    schema = BucketSchema(tuple(f"b{i}" for i in range(len(counts))))
    release = publish(schema, counts, epsilon, ledger,
                      max_contribution=max_contribution,
                      total_source=total_source)
    raw = {k: m["noisy"] for k, m in release.measurements.items()}
    out = {"raw_noisy": raw, "total_source": total_source,
           "total_target": release.total_value, "postprocessed": None}
    try:
        leaf_noisy = [release.measurements[f"leaf:b{i}"]["noisy"]
                      for i in range(len(counts))]
        out["postprocessed"] = project_simplex(leaf_noisy,
                                               release.total_value)
        out["status"] = "optimal"
    except InfeasibleError as exc:
        out["status"] = "rejected_infeasible"
        out["reason"] = str(exc)
    return out
