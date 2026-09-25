"""End-to-end demo. Run from the repo root:  python3.11 -m examples.demo"""
from pattern_matcher import (DedupStrategy, Edge, IncrementalMatchEngine,
                             MatchEnumerator, PatternEdge, PatternGraph,
                             PatternVertex, TargetGraph, Vertex,
                             enumerate_matches)


def show(title, matches):
    print(f"\n== {title} ({len(matches)}) ==")
    for m in matches:
        emb = f" edges={m.edge_assignment}" if m.edge_assignment else ""
        print(f"  {m.map_id}  {dict(m.mapping)}{emb}")


def main():
    # Target graph: people with ages, "knows"/"blocked" edges, one edge with
    # a validity interval [100, 200).
    g = TargetGraph()
    g.add_vertices([
        Vertex("ann", "person", {"age": 34}),
        Vertex("bob", "person", {"age": 17}),
        Vertex("cid", "person", {"age": 52}),
        Vertex("dan", "person", {"age": 28}),
    ])
    g.add_edges([
        Edge("k1", "ann", "cid", "knows"),
        Edge("k2", "ann", "dan", "knows"),
        Edge("k3", "cid", "dan", "knows", valid_from=100, valid_to=200),
        Edge("k4", "ann", "bob", "knows"),   # bob is under 18
    ])

    # Pattern: adult (age >= 18) who knows two other people who do NOT know
    # each other (negative edge), at time t=150.
    pattern = PatternGraph(
        [PatternVertex("p", "person", {"age": ("ge", 18)}),
         PatternVertex("q", "person"),
         PatternVertex("r", "person")],
        [PatternEdge("e1", "p", "q", "knows"),
         PatternEdge("e2", "p", "r", "knows"),
         PatternEdge("e3", "q", "r", "knows", negative=True)])

    engine = IncrementalMatchEngine(g, pattern, at_time=150)
    show("initial matches (t=150)", engine.matches)
    print(f"  initial candidates_touched={engine.last_metrics.candidates_touched}")

    # Batch update: adding the forbidden edge cid->dan and removing k2
    # revoke matches; adding dan->ann / dan->bob creates a new one.
    diff = engine.apply_updates(
        add_edges=[Edge("k5", "cid", "dan", "knows"),
                   Edge("k6", "dan", "ann", "knows"),
                   Edge("k7", "dan", "bob", "knows")],
        remove_edge_ids=["k2"])
    print(f"\n== batch update -> diff (version {diff.version}) ==")
    for m in diff.added:
        print(f"  + {m.map_id}  {dict(m.mapping)}")
    for m in diff.removed:
        print(f"  - {m.map_id}  {dict(m.mapping)}")
    print(f"  update metrics: candidates_touched="
          f"{diff.metrics.candidates_touched}, "
          f"matches_revalidated={diff.metrics.matches_revalidated}")
    show("matches after update", engine.matches)

    # Same-state check: incremental result equals full re-enumeration.
    ref = enumerate_matches(g, pattern, at_time=150)
    assert [m.map_id for m in engine.matches] == [m.map_id for m in ref]
    print("\nincremental state == full re-enumeration: OK")

    # Interruptible enumeration with resume on the same graph version.
    print("\n== paged enumeration with resume ==")
    enum = MatchEnumerator(g, pattern, at_time=150)
    page1 = enum.next_batch(1)
    print(f"  page1: {[m.map_id for m in page1]}  cursor={enum.cursor}")
    resumed = MatchEnumerator(g, pattern, at_time=150,
                              resume_after=enum.cursor)
    rest = [m.map_id for m in resumed]
    print(f"  resumed rest: {rest}")
    assert [m.map_id for m in page1] + rest == [m.map_id for m in ref]

    # Dedup strategies on parallel edges.
    g2 = TargetGraph()
    g2.add_vertices([Vertex("x", "n"), Vertex("y", "n")])
    g2.add_edges([Edge("p1", "x", "y", "link"), Edge("p2", "x", "y", "link")])
    pat2 = PatternGraph([PatternVertex("a", "n"), PatternVertex("b", "n")],
                        [PatternEdge("e", "a", "b", "link")])
    vm = enumerate_matches(g2, pat2, dedup=DedupStrategy.VERTEX_MAPPING)
    ee = enumerate_matches(g2, pat2, dedup=DedupStrategy.EDGE_EMBEDDING)
    print(f"\n== dedup strategies on parallel edges ==")
    print(f"  vertex_mapping: {len(vm)} match(es); "
          f"edge_embedding: {len(ee)} match(es)")


if __name__ == "__main__":
    main()
