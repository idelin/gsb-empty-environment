"""Run sample: hierarchical folding, moves, snapshots, time filtering."""

from hyperfold import AddEdge, AddGroup, AddVertex, Graph, Move, View


def show(view, title):
    print(f"\n== {title} (t={view.time}) ==")
    print("  supernodes:", sorted(view.supernodes()))
    for key in sorted(view.hedges):
        src, dst = key
        ids = view.hedges[key]
        print(f"  {src} -> {dst}  x{len(ids)}  sources={sorted(ids)}")


def main():
    g = Graph()
    g.apply([
        AddVertex("A"), AddVertex("B"), AddVertex("C"),
        AddVertex("D"), AddVertex("Z"),           # Z stays isolated
        AddGroup("G"), AddGroup("H"),
        Move("A", "G"), Move("B", "G"), Move("C", "H"),
        AddEdge("ab", "A", "B"),                  # intra-G once G folds
        AddEdge("e1", "A", "C"),
        AddEdge("e2", "B", "C"),
        AddEdge("e5", "A", "C"),                  # multiedge, parallel to e1
        AddEdge("e3", "C", "D", start=1.0, end=4.0),   # active on [1, 4)
        AddEdge("e4", "D", "A"),
    ])

    v1 = View(g, time=2.0)
    v1.collapse("G")
    show(v1, "v1: G collapsed")
    print("  member_count:", {k: g.member_count[k] for k in sorted(g.member_count)})
    print("  ab internal ->", v1.locate("ab"), "| e2 lives at", v1.locate("e2"))

    snap = v1.snapshot()
    g.move("C", "G")
    show(v1, "v1 after moving C into G (e1/e2/e5 turn internal)")
    print("  member_count:", {k: g.member_count[k] for k in sorted(g.member_count)})

    g.undo()
    v1.restore(snap)
    show(v1, "v1 after undo + restore(snapshot)")

    v2 = View(g, time=2.0)          # independent second view
    v2.collapse("H")
    show(v2, "v2: H collapsed instead (independent view)")

    v1.set_time(5.0)                # e3 expired: [1, 4) is left-closed right-open
    show(v1, "v1 at t=5 (e3 no longer active)")


if __name__ == "__main__":
    main()
