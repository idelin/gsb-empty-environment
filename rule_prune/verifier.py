"""Independent verifier for rule-deletion solutions.

The verifier deliberately shares no search logic with the solver: it only
uses the generic graph helpers (cycle detection) and re-checks every claim
of a solution document from scratch:

* deleted ids exist and are unique;
* deleted_cost equals the sum of the deleted rule costs;
* the remaining graph is acyclic;
* the reported topological order contains every node exactly once and
  respects every remaining edge;
* the lower-bound certificate consists of genuine directed cycles of the
  original graph that are pairwise edge-disjoint, and the certified lower
  bound equals the sum of the per-cycle minimum edge costs;
* the certified lower bound does not exceed the deleted cost;
* the reported global lower bound is consistent
  (certified_lower_bound <= lower_bound <= deleted_cost).

When certified_lower_bound == deleted_cost, optimality of the solution is
independently proven (no trust in the solver required).
"""
from __future__ import annotations

from .solver import build_adjacency, find_cycle


def _check(checks, name, ok, detail=""):
    checks.append({"name": name, "ok": bool(ok), "detail": detail})
    return bool(ok)


def verify_solution(rules, solution):
    """Verify a solution document against the parsed rule list.

    Returns a verdict dict: {"ok": bool, "optimality_proven": bool,
    "checks": [{"name", "ok", "detail"}, ...]}.
    """
    checks = []
    ok_all = True

    if not isinstance(solution, dict):
        return {
            "ok": False,
            "optimality_proven": False,
            "checks": [{"name": "solution_is_object", "ok": False,
                        "detail": "solution must be a JSON object"}],
        }

    by_id = {r.id: r for r in rules}
    cost_of = {r.id: r.cost for r in rules}

    deleted = solution.get("deleted", [])
    ok_all &= _check(
        checks, "deleted_is_list_of_strings",
        isinstance(deleted, list)
        and all(isinstance(d, str) for d in deleted),
    )
    deleted = deleted if isinstance(deleted, list) else []
    ok_all &= _check(
        checks, "deleted_ids_known",
        all(d in by_id for d in deleted),
        f"unknown ids: {[d for d in deleted if d not in by_id]}",
    )
    ok_all &= _check(
        checks, "deleted_ids_unique", len(set(deleted)) == len(deleted),
    )
    deleted_set = set(deleted)

    known_deleted = [d for d in deleted_set if d in by_id]
    actual_cost = sum(cost_of[d] for d in known_deleted)
    claimed_cost = solution.get("deleted_cost")
    ok_all &= _check(
        checks, "deleted_cost_matches",
        claimed_cost == actual_cost,
        f"claimed {claimed_cost!r}, actual {actual_cost}",
    )

    remaining_cycle = find_cycle(build_adjacency(rules, deleted_set))
    ok_all &= _check(
        checks, "remaining_acyclic", remaining_cycle is None,
        f"remaining cycle: {remaining_cycle}" if remaining_cycle else "",
    )

    nodes = set()
    for r in rules:
        nodes.add(r.src)
        nodes.add(r.dst)
    order = solution.get("topological_order", [])
    order_ok = (
        isinstance(order, list)
        and len(order) == len(nodes)
        and set(order) == nodes
    )
    if order_ok:
        pos = {n: i for i, n in enumerate(order)}
        order_ok = all(
            pos[r.src] < pos[r.dst]
            for r in rules
            if r.id not in deleted_set
        )
    ok_all &= _check(checks, "topological_order_valid", order_ok)

    certificate = solution.get("lower_bound_certificate", [])
    certified_lb = solution.get("certified_lower_bound")
    cert_ok = isinstance(certificate, list)
    used_edges = set()
    cert_total = 0
    if cert_ok:
        for cycle in certificate:
            if (
                not isinstance(cycle, list)
                or not cycle
                or any(e not in by_id for e in cycle)
            ):
                cert_ok = False
                break
            if any(e in used_edges for e in cycle):
                cert_ok = False  # cycles must be edge-disjoint
                break
            for i, eid in enumerate(cycle):
                nxt = cycle[(i + 1) % len(cycle)]
                if by_id[eid].dst != by_id[nxt].src:
                    cert_ok = False  # not a genuine directed cycle
                    break
            if not cert_ok:
                break
            used_edges.update(cycle)
            cert_total += min(cost_of[e] for e in cycle)
    ok_all &= _check(checks, "certificate_cycles_valid", cert_ok)
    ok_all &= _check(
        checks, "certified_lower_bound_matches",
        isinstance(certified_lb, int) and certified_lb == cert_total,
        f"claimed {certified_lb!r}, recomputed {cert_total}",
    )
    ok_all &= _check(
        checks, "certified_lower_bound_respected",
        isinstance(certified_lb, int) and certified_lb <= actual_cost,
    )

    lower_bound = solution.get("lower_bound")
    ok_all &= _check(
        checks, "lower_bound_consistent",
        isinstance(lower_bound, int)
        and isinstance(certified_lb, int)
        and certified_lb <= lower_bound <= actual_cost,
        f"certified={certified_lb!r} lower_bound={lower_bound!r} "
        f"cost={actual_cost}",
    )

    optimality_proven = ok_all and certified_lb == actual_cost
    _check(
        checks, "optimality_independently_proven", optimality_proven,
        "certified lower bound equals deleted cost"
        if optimality_proven
        else "optimality not independently proven by the certificate",
    )

    return {
        "ok": ok_all,
        "optimality_proven": optimality_proven,
        "checks": checks,
    }
