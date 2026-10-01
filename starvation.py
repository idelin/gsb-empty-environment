"""Bounded exhaustive search for starvation cycles of a TreeDRR schedule.

The scheduler is deterministic, so for a fixed tree configuration and
fixed periodic task streams the orbit of scheduler states from the
initial state is a "lasso": a prefix followed by a cycle. Because every
node's deficit is bounded by its cap, every cursor is bounded by its
fan-out, and every stream position is bounded by its period, the state
space is finite and the lasso is always found within ``state_bound``
steps (or the bound is reported as the checked state ceiling).

Model:
  * Every leaf has an infinite periodic stream of tasks; the queue holds
    exactly one pending task at all times (a dequeue is immediately
    replaced by the next stream task). The victim leaf's stream is the
    single cost ``victim_cost`` -- the victim task is always at the head
    until it is served.
  * A starvation cycle is the cycle part of the lasso containing no
    dequeue at the victim leaf: the victim then waits forever.

The checker deliberately bypasses enqueue-time feasibility validation so
that infeasible victims (cost above a path cap) are detected as
starvation counterexamples instead of being rejected up front.

Reports either:
  * the shortest counterexample (prefix rounds + cycle rounds, with full
    per-round event evidence), or
  * a proof that the victim is served on every pass of the terminal
    cycle (no starvation), or
  * an inconclusive result with the checked state bound.
"""
from __future__ import annotations

from tree_drr import Task, TreeDRR


def build_tree(spec: dict) -> TreeDRR:
    tree = TreeDRR()
    for nd in spec["nodes"]:
        tree.add_node(
            nd["id"], nd["parent"], nd["weight"], nd["quantum"], nd["cap"],
            leaf=nd.get("leaf", False),
        )
    return tree


def check_starvation(spec: dict, streams: dict[str, list[int]], victim_leaf: str,
                     victim_cost: int, state_bound: int = 100_000) -> dict:
    """Exhaustively search for a starvation cycle of the victim leaf.

    ``spec``    : {"nodes": [{"id","parent","weight","quantum","cap","leaf"?}]}
    ``streams`` : {leaf_id: [cost, ...]} periodic task streams for
                  non-victim leaves (default [1]).
    """
    tree = build_tree(spec)
    leaves = sorted(n.node_id for n in tree.nodes.values() if n.is_leaf)
    if victim_leaf not in leaves:
        raise ValueError(f"victim {victim_leaf!r} is not a leaf")
    stream_map = {l: tuple(streams.get(l, (1,))) for l in leaves}
    stream_map[victim_leaf] = (victim_cost,)
    pos = {l: 0 for l in leaves}
    for l in leaves:  # direct queue access: bypass feasibility validation
        tree.nodes[l].queue.append(Task(f"{l}#0", stream_map[l][0]))

    def state_key():
        ks = []
        for nid in sorted(tree.nodes):
            nd = tree.nodes[nid]
            ks.append((nid, nd.deficit, nd.cursor, nd.paused))
        return (tuple(ks), tuple(pos[l] % len(stream_map[l]) for l in leaves))

    def step():
        events = tree.run_round()
        victim_served = False
        for ev in events:
            if ev["event"] == "dequeue":
                leaf = ev["leaf"]
                if leaf == victim_leaf:
                    victim_served = True
                pos[leaf] += 1
                cost = stream_map[leaf][pos[leaf] % len(stream_map[leaf])]
                tree.nodes[leaf].queue.append(Task(f"{leaf}#{pos[leaf]}", cost))
        return events, victim_served

    seen: dict = {}
    trace: list[tuple] = []  # (state_key, events, victim_served)
    while True:
        key = state_key()
        if key in seen:
            idx = seen[key]
            prefix = trace[:idx]
            cycle = trace[idx:]
            served_in_cycle = any(sv for _, _, sv in cycle)
            base = {
                "victim_leaf": victim_leaf,
                "victim_cost": victim_cost,
                "states_explored": len(trace),
                "prefix_rounds": idx,
                "cycle_rounds": len(cycle),
                "complete": True,
            }
            if served_in_cycle:
                base.update({
                    "starvation": False,
                    "reason": "victim_served_on_every_pass_of_terminal_cycle",
                })
            else:
                base.update({
                    "starvation": True,
                    "reason": "terminal_cycle_contains_no_victim_dequeue",
                    "prefix_events": [evs for _, evs, _ in prefix],
                    "cycle_events": [evs for _, evs, _ in cycle],
                })
            return base
        if len(trace) >= state_bound:
            return {
                "starvation": None,
                "complete": False,
                "reason": "state_bound_reached",
                "states_explored": len(trace),
                "state_bound": state_bound,
                "victim_leaf": victim_leaf,
                "victim_cost": victim_cost,
            }
        seen[key] = len(trace)
        events, victim_served = step()
        trace.append((key, events, victim_served))
