"""Demo: friend-of-friend recommendation with a negative edge constraint.

Pattern: a knows b, b knows c, but a does NOT know c  ->  suggest c to a.
Shows incremental batch updates, stable mapping ids, add/remove deltas,
candidate-touch stats, and paged enumeration with resume.
"""
import sys
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parents[1]))

from graphmatch import (Edge, IncrementalMatcher, PatternEdge, PatternGraph,
                        PatternVertex, TargetGraph, Vertex)


def build_graph() -> TargetGraph:
    g = TargetGraph()
    for name in ("ann", "bob", "cid", "dan", "eve"):
        g.add_vertex(Vertex(name, "person"))
    g.add_edge(Edge("k1", "ann", "bob", "knows"))
    g.add_edge(Edge("k2", "bob", "cid", "knows"))
    g.add_edge(Edge("k3", "cid", "dan", "knows"))
    g.add_edge(Edge("k4", "dan", "eve", "knows"))
    g.add_edge(Edge("k5", "eve", "ann", "knows"))
    return g


def build_pattern() -> PatternGraph:
    return PatternGraph(
        [PatternVertex("a", "person"), PatternVertex("b", "person"),
         PatternVertex("c", "person")],
        [PatternEdge("ab", "a", "b", "knows"),
         PatternEdge("bc", "b", "c", "knows"),
         PatternEdge("ac", "a", "c", "knows", exists=False)])


def show(matcher, mids):
    for mid in mids:
        print("   ", matcher.get_mapping(mid))


def main() -> None:
    matcher = IncrementalMatcher(build_graph(), build_pattern())

    print("== initial matches (paged enumeration, 2 per page) ==")
    page, cursor = matcher.enumerate(limit=2)
    show(matcher, page)
    print(f" -- interrupted, saved cursor: {cursor}")
    while cursor is not None:
        page, cursor = matcher.enumerate(limit=2, cursor=cursor)
        show(matcher, page)
    print(" -- resumed to completion on the same graph version")

    print("\n== batch update 1: ann meets cid (k6), bob forgets cid (k2) ==")
    delta = matcher.apply_update(added_edges=[Edge("k6", "ann", "cid", "knows")],
                                 removed_edge_ids=["k2"])
    print(f"version={delta.version}")
    print(" added:", list(delta.added))
    print(" removed:", list(delta.removed))
    print(f" candidates touched: {matcher.last_op.candidates_touched}")

    print("\n== batch update 2: cid meets ann (k7) ==")
    delta = matcher.apply_update(added_edges=[Edge("k7", "cid", "ann", "knows")])
    print(f"version={delta.version}")
    print(" added:", list(delta.added))
    print(" removed:", list(delta.removed))
    print(f" candidates touched: {matcher.last_op.candidates_touched}")

    print("\n== final matches ==")
    show(matcher, matcher.results())
    print(f"\ncumulative candidates touched: {matcher.stats.candidates_touched}")


if __name__ == "__main__":
    main()
