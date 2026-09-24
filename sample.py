"""End-to-end demo of graphpatch: diff, reversible patch, 3-way replay.

Run:  python3.11 sample.py
"""
from graphpatch import (
    Edge,
    Graph,
    Interval,
    Patch,
    PreconditionError,
    Vertex,
    compose,
    diff,
    replay,
)


def show(title):
    print("\n" + "=" * 68)
    print(title)
    print("=" * 68)


def build_v1() -> Graph:
    g = Graph()
    g.add_vertex(Vertex("alice", {"role": "admin"}, Interval(0.0, 10.0)))
    g.add_vertex(Vertex("bob", {"role": "user"}))
    g.add_vertex(Vertex("carol"))
    g.add_vertex(Vertex("dave"))                      # isolated vertex
    g.add_edge(Edge("e1", "alice", "bob", {"w": 1}, Interval(1.0, 5.0)))
    g.add_edge(Edge("e2", "alice", "bob", {"w": 2}))  # parallel edge
    g.add_edge(Edge("e3", "bob", "carol"))
    return g


def build_v2(g: Graph) -> Graph:
    g2 = g.copy()
    # rename alice -> alex (edges follow the rename)
    v = g2.vertices.pop("alice")
    g2.vertices["alex"] = Vertex("alex", dict(v.attrs), v.interval)
    for eid, e in list(g2.edges.items()):
        src = "alex" if e.src == "alice" else e.src
        dst = "alex" if e.dst == "alice" else e.dst
        g2.edges[eid] = Edge(eid, src, dst, dict(e.attrs), e.interval)
    # attribute change
    g2.vertices["bob"] = Vertex("bob", {"role": "moderator", "dept": "eng"})
    # interval change (left-closed right-open)
    g2.edges["e1"] = Edge("e1", "alex", "bob", {"w": 1}, Interval(1.0, 8.0))
    # removals (carol leaves with her edge; parallel edge e2 removed)
    g2.remove_edge("e3")
    g2.remove_vertex("carol")
    g2.remove_edge("e2")
    # additions
    g2.add_vertex(Vertex("eve", {"role": "guest"}))
    g2.add_edge(Edge("e4", "alex", "eve"))
    return g2


def main() -> None:
    g1 = build_v1()
    g2 = build_v2(g1)

    show("1. diff(v1, v2) with explicit rename map {alice: alex}")
    patch = diff(g1, g2, renames={"alice": "alex"})
    for op in patch.ops:
        print("  ", op)
    print("  note: renaming alice did NOT delete+re-add her edges")

    show("2. apply(patch, v1) == v2, and the patch is reversible")
    result = patch.apply(g1)
    print("   apply(patch, v1) == v2 :", result == g2)
    back = patch.inverse().apply(g2)
    print("   apply(inverse, v2) == v1:", back == g1)
    print("   edge multiplicity alex->bob in v2:",
          result.edge_multiplicity("alex", "bob"))
    print("   isolated vertex dave preserved:", "dave" in result.vertices)

    show("3. the patch is a serializable artifact (JSON)")
    text = patch.to_json()
    print("   patch JSON size:", len(text), "bytes;",
          len(patch.ops), "ops")
    decoded = Patch.from_json(text)
    print("   decoded patch applies cleanly:", decoded.apply(g1) == g2)

    show("4. baseline verification: wrong base is rejected, graph untouched")
    wrong = g1.copy()
    wrong.vertices["bob"] = Vertex("bob", {"role": "intruder"})
    before = wrong.canonical()
    try:
        patch.apply(wrong)
        print("   ERROR: should have raised")
    except PreconditionError as exc:
        print("   rejected:", exc)
    print("   wrong baseline left unchanged:", wrong.canonical() == before)

    show("5. digest collision: weak digest matches, content does not")
    weak = lambda canonical: "COLLIDE"  # deliberately weak digest
    p2 = diff(g1, g2, {"alice": "alex"}, digest_fn=weak)
    print("   weak digests collide:",
          weak(wrong.canonical()) == p2.base_digest)
    try:
        p2.apply(wrong, digest_fn=weak)
        print("   ERROR: collision accepted!")
    except PreconditionError as exc:
        print("   rejected despite collision:", exc)
    print("   genuine base still accepted:", p2.apply(g1, digest_fn=weak) == g2)

    show("6. three-way replay onto a modified baseline (relocation)")
    alt = g1.copy()
    alt.vertices["bob"] = Vertex("bob", {"role": "user", "nick": "bobby"})
    alt.add_vertex(Vertex("frank"))  # baseline's own new vertex
    result = replay(patch, alt)
    print("   conflicts:", result.conflicts)
    relocated = result.patch.apply(alt)
    print("   rename relocated:", "alex" in relocated.vertices)
    print("   baseline's own changes kept:",
          relocated.vertices["bob"].attrs.get("nick") == "bobby"
          and "frank" in relocated.vertices)

    show("7. structured conflicts: rename-vs-delete")
    alt2 = g1.copy()
    alt2.remove_edge("e1")
    alt2.remove_edge("e2")
    alt2.remove_vertex("alice")  # baseline deleted the renamed vertex
    result2 = replay(patch, alt2)
    for c in result2.conflicts:
        print(f"   [{c.kind}] {c.message}")

    show("8. structured conflicts: same name, different identity")
    alt3 = g1.copy()
    alt3.add_vertex(Vertex("alex", {"role": "stranger"}))
    result3 = replay(patch, alt3)
    for c in result3.conflicts:
        print(f"   [{c.kind}] {c.message}")

    show("9. composition: v1 -> v2 -> v3 collapses to one patch")
    g3 = build_v2(g1).copy()
    g3.vertices["bob"] = Vertex("bob", {"role": "owner", "dept": "eng"})
    g3.remove_vertex("dave")
    p12 = diff(g1, g2, {"alice": "alex"})
    p23 = diff(g2, g3)
    composed = compose(p12, p23, g1)
    print("   composed ops:", len(composed.ops),
          "(vs", len(p12.ops), "+", len(p23.ops), "separate)")
    print("   apply(composed, v1) == v3:", composed.apply(g1) == g3)
    print("   inverse recovers v1:",
          composed.inverse().apply(g3) == g1)

    show("10. half-open intervals [start, end)")
    iv = Interval(1.0, 5.0)
    print(f"   {iv} contains 1.0: {iv.contains(1.0)}, "
          f"contains 5.0: {iv.contains(5.0)}")
    print("   active vertices at t=1.0 in v1:",
          sorted(g1.active_at(1.0).vertices))


if __name__ == "__main__":
    main()
