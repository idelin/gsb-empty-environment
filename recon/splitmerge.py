"""Split/merge reconciliation: one-to-many and many-to-one group matching.

This is NOT a bipartite matching problem: a group is 1..2 left rows plus
1..2 right rows (at most 3 rows total) whose amount sums are strictly equal
and whose rows share at least one common business key. Each row appears in
at most one group -> an exact set-packing problem, solved here by exact
branch-and-bound with an optional node budget (reporting lower/upper
bounds on interruption) plus an independent exhaustive cross-check.

Objectives (lexicographic):
  1. maximize total absolute matched amount (summed over matched rows)
  2. maximize number of matched rows
  3. minimize sum of per-group time spreads (max pairwise |dt| in a group)
  4. lexicographically smallest sorted list of canonical group ids
"""

from __future__ import annotations

from dataclasses import dataclass
from decimal import Decimal
from itertools import combinations

ZERO = Decimal(0)


@dataclass(frozen=True)
class Group:
    left_idx: tuple
    right_idx: tuple
    mask: int            # bitmask over the combined row list (left ++ right)
    amount_abs: Decimal  # sum of |amount| over member rows
    row_count: int
    spread: int          # max pairwise absolute time difference
    canonical_id: str
    shared_keys: tuple
    left_sum: Decimal
    right_sum: Decimal


def enumerate_groups(instance):
    """All feasible groups of shape 1v1, 1v2, 2v1 (at most 3 rows)."""
    left, right = instance.left, instance.right
    nl = len(left)
    groups = []
    for k_l, k_r in ((1, 1), (1, 2), (2, 1)):
        for ls in combinations(range(nl), k_l):
            for rs in combinations(range(len(right)), k_r):
                members = [left[i] for i in ls] + [right[i] for i in rs]
                common = set.intersection(*(set(m.keys) for m in members))
                if not common:
                    continue
                l_sum = sum((left[i].amount for i in ls), ZERO)
                r_sum = sum((right[i].amount for i in rs), ZERO)
                if l_sum != r_sum:  # strict equality required
                    continue
                times = [m.time for m in members]
                spread = max(times) - min(times)
                mask = 0
                for i in ls:
                    mask |= 1 << i
                for i in rs:
                    mask |= 1 << (nl + i)
                cid = ("L:" + ",".join(sorted(left[i].id for i in ls))
                       + "|R:" + ",".join(sorted(right[i].id for i in rs)))
                groups.append(Group(
                    left_idx=ls,
                    right_idx=rs,
                    mask=mask,
                    amount_abs=sum((m.abs_amount for m in members), ZERO),
                    row_count=len(members),
                    spread=spread,
                    canonical_id=cid,
                    shared_keys=tuple(sorted(common)),
                    left_sum=l_sum,
                    right_sum=r_sum,
                ))
    groups.sort(key=lambda g: g.canonical_id)
    return groups


def _canon(groups, chosen):
    return tuple(sorted(groups[i].canonical_id for i in chosen))


class BudgetExceeded(Exception):
    pass


def exact_search(groups, node_budget=None, row_amounts=None):
    """Exact branch-and-bound set packing over groups.

    Returns dict with status ("proven_optimal" | "budget_exceeded"),
    best key/solution (lower bound) and an optimistic upper-bound key.
    Key ordering: maximize (amount_abs, row_count, -spread).

    row_amounts: optional list of per-row absolute amounts indexed by bit
    position in the group masks; enables a tighter bound ("remaining rows
    can contribute at most their own absolute amounts").
    """
    n = len(groups)
    suffix_amount = [ZERO] * (n + 1)
    suffix_rows = [0] * (n + 1)
    for i in range(n - 1, -1, -1):
        suffix_amount[i] = suffix_amount[i + 1] + groups[i].amount_abs
        suffix_rows[i] = suffix_rows[i + 1] + groups[i].row_count

    if row_amounts is not None:
        n_rows = len(row_amounts)

        def bound(amt, rows, spr, pos, used):
            free_amount = ZERO
            free_rows = 0
            for b in range(n_rows):
                if not (used >> b) & 1:
                    free_amount += row_amounts[b]
                    free_rows += 1
            return (amt + min(suffix_amount[pos], free_amount),
                    rows + min(suffix_rows[pos], free_rows), -spr)
    else:
        def bound(amt, rows, spr, pos, used):
            return (amt + suffix_amount[pos], rows + suffix_rows[pos], -spr)

    best_key = None
    best_chosen = ()
    nodes = 0
    interrupted = False
    # frame: [pos, used_mask, amount, rows, spread, chosen, bound, stage]
    stack = [[0, 0, ZERO, 0, 0, (), bound(ZERO, 0, 0, 0, 0), 0]]
    while stack:
        if node_budget is not None and nodes >= node_budget:
            interrupted = True
            break
        frame = stack[-1]
        pos, used, amt, rows, spr, chosen, bnd, stage = frame
        if stage == 0:
            nodes += 1
            frame[7] = 1
            if best_key is not None and bnd < best_key:
                stack.pop()
                continue
            if pos == n:
                key = (amt, rows, -spr)
                if (best_key is None or key > best_key
                        or (key == best_key
                            and _canon(groups, chosen) < _canon(groups, best_chosen))):
                    best_key, best_chosen = key, chosen
                stack.pop()
                continue
            g = groups[pos]
            if not (g.mask & used):
                nused = used | g.mask
                stack.append([pos + 1, used | g.mask, amt + g.amount_abs,
                              rows + g.row_count, spr + g.spread,
                              chosen + (pos,),
                              bound(amt + g.amount_abs, rows + g.row_count,
                                    spr + g.spread, pos + 1, nused), 0])
        else:
            stack.pop()
            stack.append([pos + 1, used, amt, rows, spr, chosen,
                          bound(amt, rows, spr, pos + 1, used), 0])

    if interrupted:
        upper = best_key
        for frame in stack:
            if upper is None or frame[6] > upper:
                upper = frame[6]
        status = "budget_exceeded"
    else:
        upper = best_key
        status = "proven_optimal"
    return {
        "status": status,
        "key": best_key,
        "chosen": best_chosen,
        "upper_bound": upper,
        "nodes": nodes,
    }


def exhaustive_cross_check(groups):
    """Independent brute force: enumerate every disjoint group collection."""
    best = [None, ()]

    def rec(i, used, amt, rows, spr, chosen):
        if i == len(groups):
            key = (amt, rows, -spr)
            if (best[0] is None or key > best[0]
                    or (key == best[0]
                        and _canon(groups, chosen) < _canon(groups, best[1]))):
                best[0] = key
                best[1] = chosen
            return
        rec(i + 1, used, amt, rows, spr, chosen)
        g = groups[i]
        if not (g.mask & used):
            rec(i + 1, used | g.mask, amt + g.amount_abs,
                rows + g.row_count, spr + g.spread, chosen + (i,))

    rec(0, 0, ZERO, 0, 0, ())
    return {"key": best[0], "chosen": best[1]}


def forced_inclusion(groups, row_mask_bit, unconstrained_key,
                     row_amounts=None):
    """Optimal objective when the given row must be matched.

    Returns (status, key, loss) where status is one of:
      "infeasible"        -- row belongs to no feasible group at all
      "equal_alternative" -- an optimal-quality solution includes the row
      "loss"              -- forcing the row strictly worsens the objective
    """
    carriers = [g for g in groups if g.mask & row_mask_bit]
    if not carriers:
        return "infeasible", None, None
    best_key = None
    for g in carriers:
        sub_groups = [h for h in groups if not (h.mask & g.mask)]
        res = exact_search(sub_groups, row_amounts=row_amounts)
        sub_key = res["key"] if res["key"] is not None else (ZERO, 0, 0)
        key = (sub_key[0] + g.amount_abs,
               sub_key[1] + g.row_count,
               sub_key[2] - g.spread)
        if best_key is None or key > best_key:
            best_key = key
    if unconstrained_key is None:
        return "unverified", best_key, None
    if best_key == unconstrained_key:
        return "equal_alternative", best_key, None
    loss = {
        "amount_abs_loss": str(unconstrained_key[0] - best_key[0]),
        "row_loss": unconstrained_key[1] - best_key[1],
        "spread_increase": unconstrained_key[2] - best_key[2],
    }
    return "loss", best_key, loss


def _key_json(key):
    if key is None:
        return None
    return {
        "matched_amount_abs": str(key[0]),
        "matched_rows": key[1],
        "neg_total_spread": key[2],
        "total_group_spread": -key[2],
    }


def solve(instance, node_budget=None, run_cross_check=True,
          run_forced_analysis=True):
    """Solve a split/merge instance; return a JSON-serializable report."""
    left, right = instance.left, instance.right
    nl = len(left)
    groups = enumerate_groups(instance)
    row_amounts = [r.abs_amount for r in left] + [r.abs_amount for r in right]
    result = exact_search(groups, node_budget=node_budget,
                          row_amounts=row_amounts)
    key = result["key"]
    chosen = result["chosen"] if result["chosen"] is not None else ()

    used_mask = 0
    for i in chosen:
        used_mask |= groups[i].mask

    group_list = []
    for i in sorted(chosen, key=lambda j: groups[j].canonical_id):
        g = groups[i]
        group_list.append({
            "id": g.canonical_id,
            "left": [left[j].id for j in g.left_idx],
            "right": [right[j].id for j in g.right_idx],
            "shared_keys": list(g.shared_keys),
            "left_sum": str(g.left_sum),
            "right_sum": str(g.right_sum),
            "spread": g.spread,
        })

    proven = result["status"] == "proven_optimal"
    unmatched = {"left": [], "right": []}
    for side, rows, base in (("left", left, 0), ("right", right, nl)):
        for j, row in enumerate(rows):
            bit = 1 << (base + j)
            if used_mask & bit:
                continue
            entry = {"id": row.id}
            if run_forced_analysis:
                status, fkey, loss = forced_inclusion(
                    groups, bit, key, row_amounts=row_amounts)
                entry["forced_inclusion"] = {
                    "status": status,
                    "objective_if_forced": _key_json(fkey),
                    "loss": loss,
                    "verified_against_proven_optimum": proven,
                }
            unmatched[side].append(entry)

    report = {
        "engine": "splitmerge",
        "objective": _key_json(key),
        "groups": group_list,
        "unmatched": unmatched,
        "search": {
            "method": "exact branch-and-bound set packing",
            "status": result["status"],
            "nodes_explored": result["nodes"],
            "lower_bound": _key_json(key),
            "upper_bound": _key_json(result["upper_bound"]),
            "proven_optimal": proven,
        },
    }
    if run_cross_check:
        check = exhaustive_cross_check(groups)
        report["cross_check"] = {
            "method": "independent exhaustive enumeration of all packings",
            "objective_agrees": check["key"] == key,
            "solution_agrees": set(check["chosen"]) == set(chosen),
        }
    return report
