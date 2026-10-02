"""Independent uncompressed reference BFS.

Used to cross-check the main checker on small models: it computes the
full concrete reachable set and exact shortest distances with a plain
dict/deque BFS, ignoring any canonicalization the model may define.
"""
from __future__ import annotations

from collections import deque


def reference_reach(model, max_states: int = 200_000):
    """Return (reachable_set, dist, first_bad_state_or_None).

    ``dist`` maps every reachable concrete state to its shortest distance
    from the initial states. ``first_bad`` is a bad state at minimal
    depth, if any (exploration continues afterwards to finish the set).
    """
    dist = {}
    frontier = deque()
    for s0 in model.initial():
        if s0 not in dist:
            dist[s0] = 0
            frontier.append(s0)
    first_bad = None
    while frontier:
        s = frontier.popleft()
        if first_bad is None and model.is_bad(s):
            first_bad = s
        for t in model.successors(s):
            if t not in dist:
                dist[t] = dist[s] + 1
                frontier.append(t)
        if len(dist) > max_states:
            raise RuntimeError("reference model too large to cross-check")
    return set(dist), dist, first_bad
