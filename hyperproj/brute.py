"""Independent brute-force re-projection, used as a test/demo oracle.

Recomputes everything from the primitive state alone, with no incremental
index, so tests can diff the incremental engine against ground truth.
"""

from __future__ import annotations

from collections import defaultdict


def brute_projection(vertices, groups, edges, collapsed, at=None) -> dict:
    def visible(vid: str) -> str:
        node = vid
        g = vertices[vid]
        while g is not None:
            if g in collapsed:
                node = g
            g = groups[g]
        return node

    cross: dict[tuple[str, str], list[str]] = defaultdict(list)
    internal: dict[str, list[str]] = defaultdict(list)
    for eid, e in edges.items():
        if at is not None and not e.active_at(at):
            continue
        u, v = visible(e.src), visible(e.dst)
        if u == v:
            internal[u].append(eid)
        else:
            cross[(u, v)].append(eid)

    counts = {g: 0 for g in groups}
    for vid in vertices:
        g = vertices[vid]
        while g is not None:
            counts[g] += 1
            g = groups[g]

    nodes = {visible(v) for v in vertices} | set(collapsed)
    return {
        "nodes": nodes,
        "cross": {k: sorted(v) for k, v in cross.items()},
        "internal": {k: sorted(v) for k, v in internal.items()},
        "counts": counts,
        "visible": {v: visible(v) for v in vertices},
    }
