"""Bounded exhaustive starvation checker for the hierarchical DRR.

The scheduler is fully deterministic (fixed DFS order, fixed credit rules,
no events), so its reachable state graph is a single path that eventually
closes into a cycle (a functional graph).  We enumerate states until one
repeats or a step bound is hit.

State = (deficit of every node in fixed DFS order,
         stream position of every stream-fed leaf).

A leaf is *servable* if its cyclic stream contains at least one task whose
cost <= the minimum cap along its path (infeasible tasks are explicitly
rejected by the scheduler, not starved).  If a servable leaf is never served
inside the terminal cycle, it is permanently starved: the cycle plus the
prefix leading to it is a counterexample.  Because the system is
deterministic, the first repeated state yields the shortest counterexample.

If no state repeats within `max_steps`, the check is inconclusive and we
report the explored-state count together with the a-priori state-space bound
prod(cap_i + 1) * prod(len(stream_j)).
"""
from __future__ import annotations

from math import prod
from typing import Any

from hdr_core import HierDRR, Node


def state_space_bound(sched: HierDRR) -> int:
    nodes = list(sched._walk(sched.root))
    b = prod(n.cap + 1 for n in nodes)
    for leaf in sched.leaves():
        if leaf.stream:
            b *= len(leaf.stream)
    return b


def check_starvation(sched: HierDRR, max_steps: int = 10000) -> dict[str, Any]:
    nodes = list(sched._walk(sched.root))
    leaves = sched.leaves()
    stream_leaves = [l for l in leaves if l.stream]

    def state() -> tuple:
        return (tuple(n.deficit for n in nodes),
                tuple(l.stream_pos % len(l.stream) for l in stream_leaves))

    seen: dict[tuple, int] = {}
    served_per_round: list[set] = []
    records: list[dict] = []

    for step in range(max_steps):
        s = state()
        if s in seen:
            start = seen[s]
            cycle_records = records[start:]
            cycle_served = served_per_round[start:]
            starved = []
            for leaf in stream_leaves:
                servable = any(c <= leaf.min_path_cap() for c in leaf.stream)
                if servable and all(leaf.nid not in r for r in cycle_served):
                    starved.append(leaf.nid)
            result = {
                "states_explored": step,
                "state_space_bound": state_space_bound(sched),
                "cycle_start_round": start + 1,
                "cycle_length": step - start,
                "starved_leaves": starved,
                "counterexample": {
                    "prefix_rounds": start,
                    "cycle_trace": cycle_records,
                } if starved else None,
            }
            result["result"] = "STARVATION" if starved else "NO_STARVATION"
            return result
        seen[s] = step
        rec = sched.run_round()
        records.append(rec)
        served_per_round.append({d["leaf"] for d in rec["dequeues"] if d.get("served")})

    return {
        "result": "INCONCLUSIVE",
        "reason": f"no repeated state within {max_steps} steps",
        "states_explored": max_steps,
        "state_space_bound": state_space_bound(sched),
    }
