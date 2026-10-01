"""Empirical estimation with a caller-injected random source.

The caller owns the RNG (and therefore reproducibility); this module
only records the seed and the number of samples it was told to draw.
Estimates use uniform-proposal importance weighting, which handles
evidence and zero-probability configurations without any graph
structure assumptions.
"""

from __future__ import annotations

from dataclasses import dataclass
from itertools import product

from .errors import ContradictoryEvidenceError


@dataclass
class EmpiricalResult:
    """Empirical answer; always labelled as such via `method`."""

    variables: tuple
    domains: dict
    probabilities: dict
    normalizer: float
    seed: object
    n_samples: int
    n_nonzero: int
    method: str = "empirical"

    def __str__(self):
        lines = [
            f"[EMPIRICAL] P({', '.join(self.variables)})  "
            f"Z~{self.normalizer:.6g}  "
            f"(seed={self.seed}, samples={self.n_samples}, "
            f"nonzero={self.n_nonzero})"
        ]
        for key, p in self.probabilities.items():
            lines.append(f"  {key}: {p:.6f}")
        return "\n".join(lines)


def estimate_query(graph, query_vars, evidence=None, n_samples=1000,
                   rng=None, seed=None):
    """Estimate P(query_vars | evidence) and Z by weighted sampling.

    Parameters
    ----------
    rng : random.Random
        Random source injected by the caller (required).
    seed : object
        Seed the caller used to build `rng`; recorded for the audit
        trail.  This function never reseeds.
    """
    if rng is None:
        raise ValueError("caller must inject a random.Random instance")
    evidence = dict(evidence or {})
    query_vars = tuple(query_vars)
    free_vars = [v for v in graph.domains if v not in evidence]
    free_domains = [graph.domains[v] for v in free_vars]
    n_assignments = 1
    for d in free_domains:
        n_assignments *= len(d)

    hist = {key: 0.0 for key in product(
        *(graph.domains[v] for v in query_vars)
    )}
    weight_sum = 0.0
    n_nonzero = 0
    for _ in range(n_samples):
        assignment = {
            v: rng.choice(d) for v, d in zip(free_vars, free_domains)
        }
        assignment.update(evidence)
        w = graph.joint_weight(assignment)
        if w == 0.0:
            continue
        n_nonzero += 1
        weight_sum += w
        hist[tuple(assignment[v] for v in query_vars)] += w
    if weight_sum == 0.0:
        raise ContradictoryEvidenceError(
            f"no sample consistent with evidence {evidence} in "
            f"{n_samples} draws (estimated Z = 0)"
        )
    z_hat = weight_sum / n_samples * n_assignments
    probs = {k: v / weight_sum for k, v in hist.items()}
    return EmpiricalResult(
        variables=query_vars,
        domains={v: graph.domains[v] for v in query_vars},
        probabilities=probs,
        normalizer=z_hat,
        seed=seed,
        n_samples=n_samples,
        n_nonzero=n_nonzero,
    )
