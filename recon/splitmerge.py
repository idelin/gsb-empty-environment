"""Split/merge (one-to-many and many-to-one) reconciliation matching.

This is NOT a bipartite matching problem: feasible units are *groups* of up to
3 rows (at least one row from each table) and rows may appear in at most one
group, so the problem is an exact set-packing problem solved by exact
branch-and-bound search.

Group feasibility:
  * 2..3 rows, at least one from each table;
  * all rows in the group share at least one common candidate business key
    (non-empty intersection of key sets);
  * the sum of left amounts strictly equals the sum of right amounts;
  * the within-group time span (max time - min time) does not exceed the
    window.

Objectives (lexicographic):
  1. maximize the sum of |amount| over matched rows;
  2. maximize the number of matched rows;
  3. minimize the sum of within-group time spans;
  4. minimize the sorted list of canonical group IDs lexicographically.

For tie-break 4 each feasible group gets a rank by its canonical ID
``"<left ids joined by '+'>=<right ids joined by '+'>"`` and weight
``2^(G-1-rank)``.  Within any tie class of objectives 1-2 no solution's group
set can be a strict subset of another's (a strict subset covers strictly
fewer rows), so comparing these binary weights is exactly equivalent to
comparing the sorted canonical-ID lists lexicographically.
"""
from __future__ import annotations

from dataclasses import dataclass
from decimal import Decimal
from itertools import combinations


@dataclass(frozen=True)
class Group:
    gid: str
    rows: tuple          # sorted combined row indices (left rows first block)
    amount: Decimal      # sum of |amount| over member rows
    span: Decimal        # max time - min time within the group
    lexw: int            # 2^(G-1-rank) tie-break weight
    shared_keys: tuple


class BudgetExceeded(Exception):
    """Raised internally when the search node budget is exhausted."""


def _canonical_gid(left_ids, right_ids):
    return f"{'+'.join(sorted(left_ids))}={'+'.join(sorted(right_ids))}"


def enumerate_groups(left, right, window):
    """Enumerate all feasible groups, sorted by canonical ID with weights."""
    window = Decimal(window)
    nL = len(left)
    rows = list(left) + list(right)
    combos = []
    for li in range(nL):
        for ri in range(nL, len(rows)):
            combos.append((li, ri))
    for li in range(nL):
        for r1, r2 in combinations(range(nL, len(rows)), 2):
            combos.append((li, r1, r2))
    for l1, l2 in combinations(range(nL), 2):
        for ri in range(nL, len(rows)):
            combos.append((l1, l2, ri))

    raw = []
    for combo in combos:
        common = None
        for idx in combo:
            common = rows[idx].keys if common is None else common & rows[idx].keys
        if not common:
            continue
        lsum = sum((rows[i].amount for i in combo if i < nL), Decimal(0))
        rsum = sum((rows[i].amount for i in combo if i >= nL), Decimal(0))
        if lsum != rsum:
            continue
        times = [rows[i].time for i in combo]
        span = max(times) - min(times)
        if span > window:
            continue
        gid = _canonical_gid(
            [rows[i].id for i in combo if i < nL],
            [rows[i].id for i in combo if i >= nL],
        )
        raw.append((gid, tuple(sorted(combo)),
                    sum((abs(rows[i].amount) for i in combo), Decimal(0)),
                    span, tuple(sorted(common))))
    raw.sort(key=lambda g: g[0])
    total = len(raw)
    return [
        Group(gid=g[0], rows=g[1], amount=g[2], span=g[3],
              lexw=1 << (total - 1 - rank), shared_keys=g[4])
        for rank, g in enumerate(raw)
    ]


def exact_search(rows, groups, budget=None, required=frozenset(), prune=True):
    """Exact set-packing branch-and-bound over feasible groups.

    Returns a dict with the best objective tuple
    ``(abs_amount, n_rows, neg_span, lexw)`` (maximized lexicographically),
    the chosen groups, the node count, and an ``interrupted`` flag.
    ``required`` rows may not be left unmatched (used for forced-inclusion
    analysis).  With ``prune=False`` this degenerates to a plain exhaustive
    enumeration used as an independent cross-check.
    """
    n = len(rows)
    by_row = [[] for _ in range(n)]
    for g in groups:
        for r in g.rows:
            by_row[r].append(g)
    suffix_abs = [Decimal(0)] * (n + 1)
    for i in range(n - 1, -1, -1):
        suffix_abs[i] = suffix_abs[i + 1] + abs(rows[i].amount)
    total_lexw = sum(g.lexw for g in groups)

    best_obj = None
    best_sol = None
    if not required:
        best_obj = (Decimal(0), 0, Decimal(0), 0)  # empty packing is feasible
        best_sol = []
    nodes = 0

    def rec(i, used, acc, chosen):
        nonlocal best_obj, best_sol, nodes
        while i < n and used[i]:
            i += 1
        if i == n:
            if best_obj is None or acc > best_obj:
                best_obj = acc
                best_sol = list(chosen)
            return
        nodes += 1
        if budget is not None and nodes > budget:
            raise BudgetExceeded
        if prune and best_obj is not None:
            ub = (acc[0] + suffix_abs[i], acc[1] + (n - i), acc[2],
                  acc[3] + total_lexw)
            if ub < best_obj:
                return
        for g in by_row[i]:
            if any(used[j] for j in g.rows):
                continue
            # Only branch when i is the smallest-indexed unused member, so
            # each packing is generated exactly once.
            if min(j for j in g.rows if not used[j]) != i:
                continue
            for j in g.rows:
                used[j] = True
            chosen.append(g)
            rec(i + 1, used,
                (acc[0] + g.amount, acc[1] + len(g.rows),
                 acc[2] - g.span, acc[3] + g.lexw), chosen)
            chosen.pop()
            for j in g.rows:
                used[j] = False
        if i not in required:
            rec(i + 1, used, acc, chosen)

    interrupted = False
    try:
        rec(0, [False] * n, (Decimal(0), 0, Decimal(0), 0), [])
    except BudgetExceeded:
        interrupted = True
    return {"objective": best_obj, "groups": best_sol or [],
            "nodes": nodes, "interrupted": interrupted}


def _obj_to_public(obj):
    return {
        "matched_abs_amount": str(obj[0]),
        "matched_rows": obj[1],
        "total_span": str(-obj[2]),
    }


def forced_inclusion(rows, groups, row_index, optimal_obj):
    """Optimal objective when the given row is forced to be matched."""
    containing = [g for g in groups if row_index in g.rows]
    if not containing:
        return {"status": "infeasible",
                "detail": "row belongs to no feasible group at all; it can "
                          "never be matched under the constraints"}
    res = exact_search(rows, groups, required={row_index})
    obj = res["objective"]
    if obj[:3] == optimal_obj[:3]:
        # "Equal quality" is judged on the three substantive objectives;
        # the fourth (canonical-ID order) is only a deterministic
        # tie-breaker between solutions of identical quality.
        return {"status": "equal_alternative",
                "detail": "an optimal solution matching this row exists; "
                          "leaving it unmatched costs nothing",
                "objective_if_forced": _obj_to_public(obj)}
    loss = {
        "abs_amount": str(optimal_obj[0] - obj[0]),
        "rows": optimal_obj[1] - obj[1],
        "extra_span": str(obj[2] - optimal_obj[2]),
        "tiebreak_weight": optimal_obj[3] - obj[3],
    }
    return {"status": "loss",
            "detail": "forcing this row strictly degrades the objective",
            "objective_if_forced": _obj_to_public(obj),
            "loss": loss}


def solve_splitmerge(left, right, window, budget=None, verify_exhaustive=False,
                     explain=True):
    """Solve the split/merge problem and return a JSON-serializable report."""
    window = Decimal(window)
    nL = len(left)
    rows = list(left) + list(right)
    groups = enumerate_groups(left, right, window)

    result = exact_search(rows, groups, budget=budget)
    obj = result["objective"]
    chosen = result["groups"]
    matched = set()
    for g in chosen:
        matched.update(g.rows)

    report = {
        "mode": "splitmerge",
        "window": str(window),
        "feasible_groups_enumerated": len(groups),
        "objective": _obj_to_public(obj),
        "groups": [
            {
                "gid": g.gid,
                "left": [rows[i].id for i in g.rows if i < nL],
                "right": [rows[i].id for i in g.rows if i >= nL],
                "span": str(g.span),
                "matched_abs_amount": str(g.amount),
                "shared_keys": list(g.shared_keys),
            }
            for g in sorted(chosen, key=lambda g: g.gid)
        ],
        "search": {
            "nodes": result["nodes"],
            "interrupted": result["interrupted"],
            "lower_bound": _obj_to_public(obj),
        },
    }

    if result["interrupted"]:
        # Loose but valid component-wise upper bounds: match everything that
        # remains with zero additional span.
        report["search"]["upper_bound"] = {
            "matched_abs_amount": str(sum((abs(r.amount) for r in rows),
                                          Decimal(0))),
            "matched_rows": len(rows),
            "total_span": "0",
        }
        report["search"]["note"] = (
            "budget exhausted: result is the best solution found so far "
            "(lower bound); optimality is NOT proven")

    if verify_exhaustive and not result["interrupted"]:
        check = exact_search(rows, groups, prune=False)
        report["search"]["exhaustive_check"] = {
            "performed": True,
            "exhaustive_nodes": check["nodes"],
            "objectives_match": check["objective"] == obj,
        }
    else:
        report["search"]["exhaustive_check"] = {"performed": False}

    report["optimal"] = (
        not result["interrupted"]
        and report["search"]["exhaustive_check"].get("objectives_match", True)
    )

    unmatched = []
    for i, row in enumerate(rows):
        if i in matched:
            continue
        entry = {"side": "left" if i < nL else "right", "id": row.id}
        if explain:
            entry["forced_inclusion"] = forced_inclusion(rows, groups, i, obj)
        unmatched.append(entry)
    report["unmatched"] = unmatched
    return report
