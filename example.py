"""Runnable demo: python3 example.py"""

from versioned_walks import (
    BudgetExhausted,
    NoSuchWalk,
    TrajectoryTooLong,
    VersionedGraph,
)


def main() -> None:
    g = VersionedGraph()
    g.add_edge("e_sa", "S", "A")
    g.add_edge("e_loop", "A", "A")   # self-loop: infinitely many walks
    g.add_edge("e_at", "A", "T")
    g.add_edge("e_xy", "X", "Y")     # irrelevant cycle, unreachable from S
    g.add_edge("e_yx", "Y", "X")
    g.add_vertex("ISO")              # isolated vertex is preserved
    v1 = g.version

    print("== infinite mode (hi=None) ==")
    r = g.count("S", "T", 0, None)
    print("infinite:", r.infinite)
    ev = r.evidence
    print("evidence: reach", ev.reach_path, "cycle", ev.cycle,
          "at", ev.cycle_vertex, "exit", ev.exit_path)
    print("reachable slice:", sorted(r.reachable_slice))

    print()
    print("== interval counts, left-closed right-open ==")
    for lo, hi in [(0, 1), (2, 3), (0, 10), (2, 5)]:
        print(f"[{lo}, {hi}) ->", g.count("S", "T", lo, hi).count)

    print()
    print("== big integers and optional modulus ==")
    big = g.count("S", "T", 0, 10**18)
    print("exact  :", big.count)
    mod = g.count("S", "T", 0, 10**18, modulus=1_000_000_007)
    print("mod 1e9+7:", mod.count)

    print()
    print("== k-th walk in lexicographic edge-id order ==")
    for k in range(4):
        w = g.kth("S", "T", 0, 10, k)
        print(f"k={k}: edges={w.edges} vertices={w.vertices}")
    w = g.kth("S", "T", 0, 10, 5)
    print("rank of k=5 walk:", g.rank("S", "T", 0, 10, w.edges))

    print()
    print("== output limits ==")
    try:
        g.kth("S", "T", 0, 10**9, 12345, max_length=64)
    except TrajectoryTooLong as exc:
        print("TrajectoryTooLong:", exc)
    try:
        g.kth("S", "T", 0, 10, 3, budget=0)
    except BudgetExhausted as exc:
        print("BudgetExhausted:", exc)
    try:
        g.kth("S", "T", 0, 10, 10**6)
    except NoSuchWalk as exc:
        print("NoSuchWalk:", exc)

    print()
    print("== batch delete revokes infiniteness; old version kept ==")
    v2 = g.remove_edges(["e_loop"])
    print("v1 infinite:", g.count("S", "T", 0, None, version=v1).infinite)
    r2 = g.count("S", "T", 0, None, version=v2)
    print("v2 infinite:", r2.infinite, "count:", r2.count)
    print("v1 count [0,6) still:", g.count("S", "T", 0, 6, version=v1).count)


if __name__ == "__main__":
    main()
