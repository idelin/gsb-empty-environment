"""Runnable sample: conditional influence propagation with explanations."""

from influence import Condition, InfluenceGraph


def show(title, graph, at=None):
    print(f"== {title} ==")
    affected = graph.affected(at=at)
    for node in sorted(affected):
        print(f"  {affected[node].render()}")
    print(f"  affected set: {sorted(affected)}")
    print()


def main():
    g = InfluenceGraph()
    g.add_vertex("isolated-node")  # kept in the graph, never auto-affected

    # Conditional dependency edges (string ids, parallel edges allowed).
    g.add_edge("e-commit", "code", "build", Condition(all_of={"commit-pushed"}))
    g.add_edge("e-build", "build", "test", Condition(all_of={"build-ok"}))
    g.add_edge("e-ship", "test", "deploy",
               Condition(all_of={"qa-signoff"}), start=10, end=20)  # [10, 20)
    # Two parallel edges: independent evidence for "review".
    g.add_edge("e-human", "code", "review", Condition(all_of={"reviewer-assigned"}))
    g.add_edge("e-auto", "code", "review", Condition(all_of={"auto-review-enabled"}))
    # A detached cycle that must never self-prove.
    g.add_edge("c-ab", "loop-a", "loop-b")
    g.add_edge("c-ba", "loop-b", "loop-a")

    g.mark_changed("code")
    g.add_fact("commit-pushed", "build-ok", "reviewer-assigned")

    show("1. initial facts, t=15 (deploy blocked: no qa-signoff)", g, at=15)

    g.add_fact("qa-signoff")
    show("2. + qa-signoff, t=15 (inside [10,20): deploy affected)", g, at=15)
    show("3. same facts, t=20 (right-open interval: deploy unaffected)", g, at=20)

    g.remove_fact("reviewer-assigned")
    show("4. - reviewer-assigned (review lost all evidence -> exits)", g, at=15)

    g.add_fact("auto-review-enabled")
    show("5. + auto-review-enabled (review back via parallel edge e-auto)", g, at=15)

    print("== 6. detached cycle never self-proves ==")
    print(f"  loop-a affected: {'loop-a' in g.affected(at=15)}")
    print(f"  loop-b affected: {'loop-b' in g.affected(at=15)}")
    print(f"  isolated-node affected: {'isolated-node' in g.affected(at=15)}")
    print(f"  vertices preserved: {sorted(g.vertices)}")


if __name__ == "__main__":
    main()
