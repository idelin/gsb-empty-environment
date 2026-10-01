"""Independent, deliberately naive reference BFS.

Plain in-memory sets and dicts: no compression, no disk frontier, no
checkpointing, no shared code with the engine.  Used to cross-check the
engine's reachable set and per-state shortest distances on small models.
"""
from __future__ import annotations

from .normalizers import IdentityNormalizer


def reference_bfs(model, normalizer=None):
    normalizer = normalizer or IdentityNormalizer()
    distances = {}        # canonical key -> BFS depth
    representatives = {}  # canonical key -> first concrete state seen
    frontier = []
    for state0 in model.initial_states():
        key, _ = normalizer.normalize(state0)
        if key not in distances:
            distances[key] = 0
            representatives[key] = state0
            frontier.append(state0)
    first_violation = None

    def check(state, dist):
        nonlocal first_violation
        if first_violation is not None:
            return
        for name, predicate in model.invariants():
            if not predicate(state):
                first_violation = (dist, name)
                return

    for state0 in frontier:
        check(state0, 0)
    dist = 0
    while frontier:
        next_frontier = []
        for state in frontier:
            for _, succ in model.successors(state):
                key, _ = normalizer.normalize(succ)
                if key in distances:
                    continue
                distances[key] = dist + 1
                representatives[key] = succ
                check(succ, dist + 1)
                next_frontier.append(succ)
        frontier = next_frontier
        dist += 1
    return {
        "distances": distances,
        "representatives": representatives,
        "first_violation": first_violation,
    }
