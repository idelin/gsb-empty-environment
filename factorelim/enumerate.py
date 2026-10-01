"""Brute-force enumeration: ground truth for small graphs."""

from __future__ import annotations

from itertools import product

from .eliminate import QueryResult, EliminationStats
from .errors import ContradictoryEvidenceError


def brute_force_query(graph, query_vars, evidence=None):
    """Exact posterior by enumerating every joint assignment.

    Only feasible for small graphs; used as the reference implementation
    that variable elimination is checked against.
    """
    evidence = dict(evidence or {})
    query_vars = tuple(query_vars)
    free_vars = [v for v in graph.domains if v not in evidence]
    free_domains = [graph.domains[v] for v in free_vars]

    z = 0.0
    hist = {key: 0.0 for key in product(
        *(graph.domains[v] for v in query_vars)
    )}
    for values in product(*free_domains):
        assignment = dict(zip(free_vars, values))
        assignment.update(evidence)
        w = graph.joint_weight(assignment)
        if w == 0.0:
            continue
        z += w
        hist[tuple(assignment[v] for v in query_vars)] += w
    if z == 0.0:
        raise ContradictoryEvidenceError(
            f"evidence {evidence} has probability 0 (normalizer Z = 0)"
        )
    probs = {k: v / z for k, v in hist.items()}
    return QueryResult(
        variables=query_vars,
        domains={v: graph.domains[v] for v in query_vars},
        probabilities=probs,
        normalizer=z,
        stats=EliminationStats(order=()),
    )
