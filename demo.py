"""Runnable sample for the dynamic multigraph decomposition.

Run with:  python3.11 demo.py
"""

from dynamic_graph import DynamicMultigraph


def show(title, g, t=None):
    print(f"--- {title} ---")
    print("vertices:            ", sorted(g.vertices))
    print("bridges:             ", sorted(g.bridges(t)))
    print("articulation points: ", sorted(g.articulation_points(t)))
    print("biconnected blocks:  ", sorted(sorted(b) for b in g.biconnected_components(t)))
    print("connected components:", sorted(sorted(c) for c in g.connected_components(t)))
    print()


def main():
    # 1. Parallel edges between A and B: neither is a bridge.
    g = DynamicMultigraph()
    g.add_edges([("e1", "A", "B"), ("e2", "A", "B")])
    show("two parallel edges A<=>B (multigraph semantics)", g)
    print("separated if e1 fails:", g.separated_by("e1"), "\n")

    # 2. Delete one: the remaining edge becomes a bridge.
    g.remove_edges(["e1"])
    show("after removing e1", g)
    sides = g.separated_by("e2")
    print("separated if e2 fails:", sorted(sides[0]), "|", sorted(sides[1]), "\n")

    # 3. Triangle with a tail: the edge to D is a bridge, C is a cut vertex.
    g = DynamicMultigraph()
    g.add_edges(
        [("ab", "A", "B"), ("bc", "B", "C"), ("ca", "C", "A"), ("cd", "C", "D")]
    )
    show("triangle A-B-C with tail C-D", g)
    sides = g.separated_by("cd")
    print("separated if cd fails:", sorted(sides[0]), "|", sorted(sides[1]), "\n")

    # 4. Batch updates + self-loop + isolated vertex.
    g.add_vertex("Z")  # isolated vertices are kept
    g.add_edges([("de", "D", "E"), ("ee", "E", "E")])  # batch add, incl. self-loop
    show("batch add D-E and self-loop E-E, isolated Z", g)
    g.remove_edges(["cd", "de"])  # batch remove
    show("batch remove cd, de (D, E stay as isolated vertices)", g)

    # 5. Half-open time intervals [start, end).
    g = DynamicMultigraph()
    g.add_edges(
        [
            ("e1", "A", "B", 0, 10),
            ("e2", "A", "B", 3, 6),  # second A-B edge active only in [3, 6)
        ]
    )
    for t in (2, 3, 5, 6):
        print(f"t={t}: bridges={sorted(g.bridges(t))}")
    print()


if __name__ == "__main__":
    main()
