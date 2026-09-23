"""Brute-force reference implementation ("full algorithm").

Every query is answered from first principles on a plain snapshot
``(vertices, edges)`` -- no incremental state, no Tarjan:

* bridges:            remove the edge, count connected components
* articulation points: remove the vertex, count connected components
* biconnected blocks: equivalence classes of non-bridge, non-loop
  edges where ``e ~ f`` iff no single vertex removal disconnects them;
  bridges and self-loops are singleton blocks

The randomized test-suite compares :class:`dynamic_graph.DynamicMultigraph`
against these functions after every batch update.
"""

from __future__ import annotations

from typing import Iterable, NamedTuple, Optional

from dynamic_graph import Edge


class FullAnalysis(NamedTuple):
    bridges: frozenset
    articulation_points: frozenset
    blocks: frozenset  # frozenset[frozenset[str]]
    components: frozenset  # frozenset[frozenset[str]]


def _active(edges: Iterable[Edge], t: Optional[float]) -> list[Edge]:
    edges = list(edges)
    if t is None:
        return edges
    return [e for e in edges if e.is_active(t)]


def _count_components(vertices: Iterable[str], edges: Iterable[Edge]) -> int:
    adj: dict[str, list[str]] = {v: [] for v in vertices}
    for e in edges:
        if e.u == e.v:
            continue  # self-loops never connect anything
        adj[e.u].append(e.v)
        adj[e.v].append(e.u)
    count = 0
    seen: set[str] = set()
    for start in adj:
        if start in seen:
            continue
        count += 1
        stack = [start]
        seen.add(start)
        while stack:
            u = stack.pop()
            for w in adj[u]:
                if w not in seen:
                    seen.add(w)
                    stack.append(w)
    return count


def _components(vertices: Iterable[str], edges: Iterable[Edge]) -> frozenset:
    adj: dict[str, list[str]] = {v: [] for v in vertices}
    for e in edges:
        if e.u == e.v:
            continue
        adj[e.u].append(e.v)
        adj[e.v].append(e.u)
    comps: set[frozenset] = set()
    seen: set[str] = set()
    for start in adj:
        if start in seen:
            continue
        comp = set()
        stack = [start]
        seen.add(start)
        while stack:
            u = stack.pop()
            comp.add(u)
            for w in adj[u]:
                if w not in seen:
                    seen.add(w)
                    stack.append(w)
        comps.add(frozenset(comp))
    return frozenset(comps)


def find_bridges(vertices: set[str], edges: Iterable[Edge]) -> frozenset:
    edges = list(edges)
    base = _count_components(vertices, edges)
    result = set()
    for e in edges:
        if e.u == e.v:
            continue  # self-loops are never bridges
        rest = [x for x in edges if x.id != e.id]
        if _count_components(vertices, rest) > base:
            result.add(e.id)
    return frozenset(result)


def find_articulation_points(vertices: set[str], edges: Iterable[Edge]) -> frozenset:
    edges = list(edges)
    base = _count_components(vertices, edges)
    result = set()
    for v in vertices:
        rest_v = vertices - {v}
        rest_e = [e for e in edges if e.u != v and e.v != v]
        if _count_components(rest_v, rest_e) > base:
            result.add(v)
    return frozenset(result)


def _linked_without(vertices: set[str], edges: list[Edge], e: Edge, f: Edge) -> bool:
    """True iff no single vertex removal separates edges e and f.

    For every vertex w, look at G - w: the surviving endpoints of e
    (those different from w) and the surviving endpoints of f must lie
    in the same connected component.  This is the standard
    characterisation of two edges belonging to the same biconnected
    block (no articulation point puts them on different sides).
    """
    for w in vertices:
        adj: dict[str, list[str]] = {}
        for x in edges:
            if x.u == x.v or x.u == w or x.v == w:
                continue
            adj.setdefault(x.u, []).append(x.v)
            adj.setdefault(x.v, []).append(x.u)
        start = e.u if e.u != w else e.v
        target = f.u if f.u != w else f.v
        seen = {start}
        stack = [start]
        while stack:
            u = stack.pop()
            for nxt in adj.get(u, ()):  # vertices with no edges stay absent
                if nxt not in seen:
                    seen.add(nxt)
                    stack.append(nxt)
        if target not in seen:
            return False
    return True


def find_blocks(vertices: set[str], edges: Iterable[Edge]) -> frozenset:
    edges = list(edges)
    bridges = find_bridges(vertices, edges)
    ordinary = [e for e in edges if e.u != e.v and e.id not in bridges]
    blocks: set[frozenset] = set()
    for e in edges:
        if e.u == e.v or e.id in bridges:
            blocks.add(frozenset({e.id}))  # loops and bridges stand alone
    remaining = set(e.id for e in ordinary)
    by_id = {e.id: e for e in ordinary}
    while remaining:
        seed = by_id[remaining.pop()]
        block = {seed.id}
        for other in list(remaining):
            if _linked_without(vertices, edges, seed, by_id[other]):
                block.add(other)
        remaining -= block
        blocks.add(frozenset(block))
    return frozenset(blocks)


def full_analysis(
    vertices: set[str], edges: Iterable[Edge], t: Optional[float] = None
) -> FullAnalysis:
    """Whole-graph decomposition from scratch at time ``t``."""
    active = _active(edges, t)
    return FullAnalysis(
        bridges=find_bridges(vertices, active),
        articulation_points=find_articulation_points(vertices, active),
        blocks=find_blocks(vertices, active),
        components=_components(vertices, active),
    )


def separated_sets(
    vertices: set[str],
    edges: Iterable[Edge],
    edge_id: str,
    t: Optional[float] = None,
) -> Optional[tuple[frozenset, frozenset]]:
    """Reference for ``DynamicMultigraph.separated_by``."""
    active = _active(edges, t)
    by_id = {e.id: e for e in active}
    if edge_id not in by_id:
        return None
    if edge_id not in find_bridges(vertices, active):
        return None
    edge = by_id[edge_id]
    rest = [e for e in active if e.id != edge_id]
    comps = _components(vertices, rest)
    side_u = side_v = None
    for comp in comps:
        if edge.u in comp:
            side_u = comp
        if edge.v in comp:
            side_v = comp
    return (side_u, side_v)
