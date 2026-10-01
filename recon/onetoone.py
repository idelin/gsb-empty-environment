"""One-to-one reconciliation matching.

Feasibility: a left row and a right row may match iff they share at least one
candidate business key, have exactly equal amounts, and their time difference
does not exceed the window.  Each row is used at most once.

Objectives (lexicographic):
  1. maximize number of matched pairs;
  2. minimize total time difference;
  3. minimize the sorted list of pair IDs lexicographically.

Objective 1 is enforced by computing a *maximum* flow; objectives 2 and 3 are
encoded as exact tuple costs.  For tie-break 3, every candidate edge gets a
rank by its pair ID ``"<left_id>::<right_id>"`` and weight ``2^(E-1-rank)``.
Because any two competing max-cardinality solutions contain the same number
of pairs, comparing these binary weights is exactly equivalent to comparing
the sorted pair-ID lists lexicographically (the most significant differing
bit dominates the sum of all lower bits).
"""
from __future__ import annotations

from collections import deque
from decimal import Decimal

from .flow import has_negative_cycle, min_cost_max_flow


def pair_id(left_id, right_id):
    return f"{left_id}::{right_id}"


def solve_onetoone(left, right, window):
    """Solve the one-to-one problem and return a JSON-serializable report."""
    window = Decimal(window)
    nL, nR = len(left), len(right)

    edges = []
    adj = [[] for _ in range(nL)]  # li -> [ri]
    for i, lrow in enumerate(left):
        for j, rrow in enumerate(right):
            shared = lrow.keys & rrow.keys
            if not shared:
                continue
            if lrow.amount != rrow.amount:
                continue
            td = abs(lrow.time - rrow.time)
            if td > window:
                continue
            adj[i].append(j)
            edges.append({"li": i, "ri": j, "td": td, "shared": sorted(shared)})
    edges.sort(key=lambda e: pair_id(left[e["li"]].id, right[e["ri"]].id))

    nE = len(edges)
    source, sink = nL + nR, nL + nR + 1
    arcs = []
    for i in range(nL):
        arcs.append((source, i, 1, (Decimal(0), 0)))
    for j in range(nR):
        arcs.append((nL + j, sink, 1, (Decimal(0), 0)))
    for rank, e in enumerate(edges):
        lexw = 1 << (nE - 1 - rank)
        cost = (e["td"], (1 << nE) - lexw)  # non-negative tuple cost
        arcs.append((e["li"], nL + e["ri"], 1, cost))

    flow, graph, refs = min_cost_max_flow(sink + 1, source, sink, arcs)

    match_l, match_r = {}, {}
    for k, e in enumerate(edges):
        if refs[nL + nR + k][1] == 0:  # arc saturated -> matched
            match_l[e["li"]] = e["ri"]
            match_r[e["ri"]] = e["li"]

    pairs = []
    for li, ri in sorted(match_l.items(),
                         key=lambda kv: pair_id(left[kv[0]].id, right[kv[1]].id)):
        e = next(e for e in edges if e["li"] == li and e["ri"] == ri)
        pairs.append({
            "pair_id": pair_id(left[li].id, right[ri].id),
            "left": left[li].id,
            "right": right[ri].id,
            "amount": str(left[li].amount),
            "time_diff": str(e["td"]),
            "shared_keys": e["shared"],
        })

    # --- Optimality evidence -------------------------------------------------
    # (a) Cardinality: Koenig's theorem.  Alternating BFS from unmatched left
    #     rows yields a minimum vertex cover; |cover| == |matching| proves
    #     that no larger matching exists, and that no augmenting path exists.
    # (b) Cost: the residual graph of a min-cost flow is optimal iff it has
    #     no negative-cost cycle (checked exactly with tuple costs).
    start = [i for i in range(nL) if i not in match_l]
    vis_l, vis_r = set(start), set()
    queue = deque(start)
    while queue:
        u = queue.popleft()
        for v in adj[u]:
            if match_l.get(u) == v:
                continue  # alternate: non-matching edge L->R
            if v in vis_r:
                continue
            vis_r.add(v)
            w = match_r.get(v)
            if w is not None and w not in vis_l:
                vis_l.add(w)
                queue.append(w)
    cover_left = [i for i in range(nL) if i not in vis_l]
    cover_right = [j for j in range(nR) if j in vis_r]
    cover_size = len(cover_left) + len(cover_right)

    unmatched_right_reachable = [j for j in vis_r if j not in match_r]
    augmenting_path_free = not unmatched_right_reachable
    neg_cycle = has_negative_cycle(graph, sink + 1)

    verification = {
        "matching_size": flow,
        "vertex_cover_size": cover_size,
        "cardinality_optimal": cover_size == flow,
        "augmenting_path_free": augmenting_path_free,
        "residual_negative_cycle_found": neg_cycle,
        "cost_optimal": not neg_cycle,
    }
    verification["optimal"] = (
        verification["cardinality_optimal"]
        and verification["augmenting_path_free"]
        and verification["cost_optimal"]
    )

    # --- Unmatched-row explanation ------------------------------------------
    unmatched = []
    for i, lrow in enumerate(left):
        if i in match_l:
            continue
        if not adj[i]:
            unmatched.append({
                "side": "left", "id": lrow.id, "reason": "no_candidate",
                "evidence": {
                    "detail": "no right row shares a business key with equal "
                              "amount within the time window",
                },
            })
        else:
            unmatched.append({
                "side": "left", "id": lrow.id, "reason": "competition",
                "evidence": {
                    "compatible_right": [right[j].id for j in adj[i]],
                    "all_compatible_right_matched":
                        all(j in match_r for j in adj[i]),
                    "alternating_reachable_right":
                        sorted(right[j].id for j in vis_r),
                    "note": "every compatible right row is matched to another "
                            "left row, and no alternating path from this row "
                            "reaches an unmatched right row (no augmenting "
                            "path exists), so leaving it unmatched is forced "
                            "by global competition",
                },
            })
    for j, rrow in enumerate(right):
        if j in match_r:
            continue
        compat = [i for i in range(nL) if j in adj[i]]
        if not compat:
            unmatched.append({
                "side": "right", "id": rrow.id, "reason": "no_candidate",
                "evidence": {
                    "detail": "no left row shares a business key with equal "
                              "amount within the time window",
                },
            })
        else:
            unmatched.append({
                "side": "right", "id": rrow.id, "reason": "competition",
                "evidence": {
                    "compatible_left": [left[i].id for i in compat],
                    "all_compatible_left_matched":
                        all(i in match_l for i in compat),
                    "note": "every compatible left row is matched elsewhere "
                            "and the vertex-cover certificate proves the "
                            "matching is maximum",
                },
            })

    return {
        "mode": "onetoone",
        "window": str(window),
        "objectives": {
            "matched_pairs": flow,
            "total_time_diff": str(sum(match_td for match_td in
                                       (abs(left[i].time - right[j].time)
                                        for i, j in match_l.items()))),
        },
        "pairs": pairs,
        "unmatched": unmatched,
        "verification": verification,
    }
