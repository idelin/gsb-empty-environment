"""Random graph generators and mutators shared by the test-suite."""
from __future__ import annotations

import random
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from graphpatch import Edge, Graph, Interval, Vertex  # noqa: E402

ATTR_KEYS = ("color", "weight", "label", "flag", "rank")
ATTR_VALUES = ("red", "blue", "green", 0, 1, 2, 3.5, True, False, None)


def random_attrs(rng: random.Random, max_keys: int = 3) -> dict:
    keys = rng.sample(list(ATTR_KEYS), rng.randint(0, max_keys))
    return {k: rng.choice(ATTR_VALUES) for k in keys}


def random_interval(rng: random.Random) -> Interval:
    start = rng.choice([float("-inf"), 0.0, 1.0, 2.0, 5.0])
    end = rng.choice([3.0, 4.0, 7.0, 10.0, float("inf")])
    if not start < end:
        start, end = 0.0, 10.0
    return Interval(start, end)


def random_graph(
    rng: random.Random, n_vertices: int = 8, n_edges: int = 12, prefix: str = ""
) -> Graph:
    """Random directed multigraph: parallel edges and isolated vertices."""
    g = Graph()
    for i in range(n_vertices):
        g.add_vertex(Vertex(f"{prefix}v{i}", random_attrs(rng), random_interval(rng)))
    vids = sorted(g.vertices)
    for j in range(n_edges):
        src = rng.choice(vids)
        dst = rng.choice(vids)  # self-loops and parallel edges allowed
        g.add_edge(
            Edge(f"{prefix}e{j}", src, dst, random_attrs(rng), random_interval(rng))
        )
    return g


def mutate(
    rng: random.Random, g: Graph, tag: str = "m"
) -> tuple:
    """Apply random mutations; returns (new_graph, rename_map)."""
    g2 = g.copy()
    renames: dict[str, str] = {}

    # rename a few vertices (edges follow automatically)
    vids = sorted(g2.vertices)
    rng.shuffle(vids)
    taken = set(g2.vertices)
    for vid in vids[: rng.randint(0, min(2, len(vids)))]:
        new = f"{vid}_{tag}"
        if new not in taken:
            renames[vid] = new
            taken.add(new)
    for old, new in renames.items():
        v = g2.vertices.pop(old)
        g2.vertices[new] = Vertex(new, dict(v.attrs), v.interval)
        for eid, e in list(g2.edges.items()):
            src = new if e.src == old else e.src
            dst = new if e.dst == old else e.dst
            if (src, dst) != (e.src, e.dst):
                g2.edges[eid] = Edge(eid, src, dst, dict(e.attrs), e.interval)

    # remove some vertices together with their incident edges
    victims = rng.sample(
        sorted(g2.vertices), rng.randint(0, min(2, len(g2.vertices)))
    )
    for vid in victims:
        for eid in [
            eid
            for eid, e in g2.edges.items()
            if e.src == vid or e.dst == vid
        ]:
            g2.remove_edge(eid)
        g2.remove_vertex(vid)

    # remove some edges
    eids = sorted(g2.edges)
    rng.shuffle(eids)
    for eid in eids[: rng.randint(0, min(2, len(eids)))]:
        g2.remove_edge(eid)

    # add vertices (possibly isolated) and edges (possibly parallel)
    n_new_v = rng.randint(0, 2)
    for i in range(n_new_v):
        vid = f"{tag}new{i}"
        if vid not in g2.vertices:
            g2.add_vertex(Vertex(vid, random_attrs(rng), random_interval(rng)))
    vids = sorted(g2.vertices)
    if vids:
        for i in range(rng.randint(0, 3)):
            eid = f"{tag}e{i}"
            if eid not in g2.edges:
                g2.add_edge(
                    Edge(
                        eid,
                        rng.choice(vids),
                        rng.choice(vids),
                        random_attrs(rng),
                        random_interval(rng),
                    )
                )

    # tweak attributes and intervals of surviving entities
    for vid in rng.sample(
        sorted(g2.vertices), rng.randint(0, min(3, len(g2.vertices)))
    ):
        v = g2.vertices[vid]
        attrs = dict(v.attrs)
        key = rng.choice(ATTR_KEYS)
        if key in attrs and rng.random() < 0.4:
            del attrs[key]
        else:
            attrs[key] = rng.choice(ATTR_VALUES)
        interval = random_interval(rng) if rng.random() < 0.4 else v.interval
        g2.vertices[vid] = Vertex(vid, attrs, interval)
    for eid in rng.sample(
        sorted(g2.edges), rng.randint(0, min(3, len(g2.edges)))
    ):
        e = g2.edges[eid]
        attrs = dict(e.attrs)
        key = rng.choice(ATTR_KEYS)
        if key in attrs and rng.random() < 0.4:
            del attrs[key]
        else:
            attrs[key] = rng.choice(ATTR_VALUES)
        interval = random_interval(rng) if rng.random() < 0.4 else e.interval
        g2.edges[eid] = Edge(eid, e.src, e.dst, attrs, interval)

    # a renamed vertex that was later removed is just a removal
    renames = {o: n for o, n in renames.items() if n in g2.vertices}
    return g2, renames
