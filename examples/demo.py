"""Runnable demo: versioned walk counting + lexicographic k-th walk.

Run from the repository root:  python3.11 examples/demo.py
"""

import os
import sys

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from walkgraph import KthStatus, VersionedGraph  # noqa: E402


def show_kth(view, s, t, length, k):
    res = view.kth_walk(s, t, length, k)
    if res.status is KthStatus.OK:
        print(f"  k={k}: vertices={res.vertices} edges={res.edges} "
              f"(total={res.total})")
    else:
        print(f"  k={k}: {res.status.value}")


def main():
    g = VersionedGraph()
    with g.batch():
        g.add_vertex("ISO")                      # isolated vertex, kept
        g.add_edge("e_sa", "S", "A")
        g.add_edge("e_loop1", "A", "A")          # self-loop (multi-edge 1/2)
        g.add_edge("e_loop2", "A", "A")          # self-loop (multi-edge 2/2)
        g.add_edge("e_at", "A", "T")
        g.add_edge("e_sb", "S", "B")
        g.add_edge("e_bt", "B", "T")
        g.add_edge("e_sx", "S", "X")
        g.add_edge("e_xy", "X", "Y")
        g.add_edge("e_yx", "Y", "X")             # unrelated cycle, no way to T
    v_inf = g.version
    print(f"version {v_inf}: S->A(+2 self-loops)->T, S->B->T, "
          f"S->X<->Y (unrelated cycle), ISO isolated")

    view = g.at(v_inf)
    res = view.count_walks("S", "T", 0, None)    # infinite-length mode
    print(f"\n[1] count S->T over [0, +inf): infinite={res.infinite}")
    ev = res.evidence
    print(f"    cycle={ev.cycle} via edges={ev.cycle_edges}")
    print(f"    reachable prefix={ev.prefix}  suffix to T={ev.suffix}")
    print(f"    reachable slice={sorted(res.reachable_slice)}")

    with g.batch():                              # batch delete: ONE commit
        g.remove_edge("e_loop1")
        g.remove_edge("e_loop2")
    v_fin = g.version
    print(f"\n[2] batch-deleted both self-loops -> version {v_fin}")
    fin = g.at(v_fin)
    res = fin.count_walks("S", "T", 0, None)
    print(f"    infinite revoked: infinite={res.infinite}, count={res.count}")
    old = g.at(v_inf).count_walks("S", "T", 0, None)
    print(f"    old version {v_inf} retained: infinite={old.infinite}")

    print("\n[3] interval counts on the finite version (left-closed, "
          "right-open):")
    for lo, hi in [(0, 1), (0, 2), (2, 3), (0, 5), (3, 9)]:
        r = fin.count_walks("S", "T", lo, hi)
        print(f"    [{lo}, {hi}): {r.count}")
    r = fin.count_walks("S", "T", 0, 5, mod=10**9 + 7)
    print(f"    [0, 5) mod 1e9+7: {r.count}")
    print(f"    length 0, S==S: {fin.count_walks('S', 'S', 0, 1).count} "
          f"| length 0, S!=T: {fin.count_walks('S', 'T', 0, 1).count}")

    print("\n[4] lexicographic k-th walk, length 2, S->T:")
    for k in range(3):
        show_kth(fin, "S", "T", 2, k)

    print("\n[5] budget guard: length 100 with max_length=16 ->",
          fin.kth_walk("S", "T", 100, 0, max_length=16).status.value)
    print("    genuine no-path X->T ->",
          fin.kth_walk("X", "T", 3, 0).status.value)


if __name__ == "__main__":
    main()
