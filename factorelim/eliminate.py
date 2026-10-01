"""Variable elimination with caller-controlled elimination order."""

from __future__ import annotations

from dataclasses import dataclass, field

from .errors import ContradictoryEvidenceError
from .factors import Factor


@dataclass
class EliminationStats:
    """Bookkeeping that shows how the order shapes intermediate tables."""

    order: tuple = ()
    intermediate_sizes: list = field(default_factory=list)

    @property
    def max_intermediate_size(self):
        return max(self.intermediate_sizes, default=1)


def _unit_factor():
    return Factor((), {}, [1.0])


def variable_elimination(factors, order, stats=None):
    """Eliminate `order` from `factors`; return the remaining factor.

    `stats`, if given, records the table size of every intermediate
    product formed just before a variable is summed out -- this is the
    quantity a good elimination order keeps small.
    """
    work = [f for f in factors if f.variables]
    constants = [f for f in factors if not f.variables]
    for var in order:
        involved = [f for f in work if var in f.variables]
        if not involved:
            continue
        work = [f for f in work if var not in f.variables]
        product = involved[0]
        for f in involved[1:]:
            product = product.multiply(f)
        if stats is not None:
            stats.intermediate_sizes.append(product.size)
        reduced = product.sum_out(var)
        if reduced.variables:
            work.append(reduced)
        else:
            constants.append(reduced)
    result = _unit_factor()
    for f in constants + work:
        result = result.multiply(f)
    return result


def greedy_order(factors, keep):
    """Min-size heuristic: always eliminate the variable whose removal
    produces the smallest intermediate factor (ties: fewest new fill
    edges, then name)."""
    keep = set(keep)
    scopes = [set(f.variables) for f in factors]
    sizes = {v: len(f.domains[v]) for f in factors for v in f.variables}
    order = []
    remaining = set(sizes) - keep
    while remaining:
        best = None
        for var in sorted(remaining):
            touched = [s for s in scopes if var in s]
            cluster = set().union(*touched) - {var}
            cost = 1
            for v in cluster | {var}:
                cost *= sizes[v]
            fill = sum(
                1
                for a in cluster
                for b in cluster
                if a < b and not any(a in s and b in s for s in scopes)
            )
            key = (cost, fill, var)
            if best is None or key < best[0]:
                best = (key, var, cluster)
        _, var, cluster = best
        order.append(var)
        scopes = [s for s in scopes if var not in s]
        if cluster:
            scopes.append(set(cluster))
        remaining.discard(var)
    return tuple(order)


@dataclass
class QueryResult:
    """Exact answer to a marginal query."""

    variables: tuple
    domains: dict
    probabilities: dict
    normalizer: float
    stats: EliminationStats
    method: str = "exact"

    def __str__(self):
        lines = [f"[EXACT] P({', '.join(self.variables)})  Z={self.normalizer:.6g}"]
        for key, p in self.probabilities.items():
            lines.append(f"  {key}: {p:.6f}")
        lines.append(
            f"  elimination order={self.stats.order} "
            f"max_intermediate={self.stats.max_intermediate_size}"
        )
        return "\n".join(lines)


def query(graph, query_vars, evidence=None, order=None):
    """Exact posterior P(query_vars | evidence) plus the normalizer Z.

    Z equals P(evidence) (or the model's partition function when no
    evidence is given).  Z == 0 means the evidence is contradictory and
    raises ContradictoryEvidenceError.
    """
    evidence = dict(evidence or {})
    query_vars = tuple(query_vars)
    restricted = [f.restrict(evidence) for f in graph.factors]
    if order is None:
        order = greedy_order(restricted, query_vars)
    else:
        order = tuple(order)
        leftover = [
            v
            for v in graph.domains
            if v not in order and v not in query_vars and v not in evidence
        ]
        order = order + tuple(leftover)
    stats = EliminationStats(order=order)
    joint = variable_elimination(restricted, order, stats)
    normalized, z = joint.normalize()
    if z == 0.0:
        raise ContradictoryEvidenceError(
            f"evidence {evidence} has probability 0 (normalizer Z = 0)"
        )
    return QueryResult(
        variables=normalized.variables,
        domains=normalized.domains,
        probabilities=normalized.probabilities(),
        normalizer=z,
        stats=stats,
    )
