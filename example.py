"""运行样例：菱形支配关系、单边变更重算、支配查询与路径反例。"""

from dominator import DominatorEngine, Edge, Graph


def show(eng, t):
    snap = eng.snapshot(t)
    print(f"  idom: {dict(sorted(snap.idom.items(), key=lambda kv: str(kv[0])))}")
    print(f"  不可达点（单独表示，不进支配树）: {sorted(snap.unreachable)}")


def main():
    g = Graph()
    # 菱形 S -> A/B -> T
    g.add_edge(Edge("e_sa", "S", "A"))
    g.add_edge(Edge("e_sb", "S", "B"))
    g.add_edge(Edge("e_at", "A", "T"))
    g.add_edge(Edge("e_bt", "B", "T"))
    # 多重边：A -> T 再来一条（边 ID 不同，保留）
    g.add_edge(Edge("e_at_dup", "A", "T"))
    # 孤立点与不可达环
    g.add_vertex("ISO")
    g.add_edge(Edge("e_xy", "X", "Y"))
    g.add_edge(Edge("e_yx", "Y", "X"))

    eng = DominatorEngine(g, entry="S")

    print("== 1. 菱形 S->A/B->T，t=0 ==")
    show(eng, 0)

    print("\n== 2. 支配查询与路径反例 ==")
    print(f"  S 支配 T ? {eng.dominates('S', 'T', 0)}")
    r = eng.explain("A", "T", 0)
    print(f"  A 支配 T ? False  反例路径: {r.path}")
    r = eng.explain("S", "X", 0)
    print(f"  S 支配 X ? 目标不可达（状态: {r.status}）")

    print("\n== 3. 单边变更：删 e_bt (B->T)，t=0 重算受影响区域 ==")
    report = eng.remove_edge("e_bt", at=0)
    show(eng, 0)
    print(f"  受影响区域: {sorted(report.affected)}")
    print(f"  idom 变化: {report.idom_changed}")

    print("\n== 4. 时间区间左闭右开：令 e_at 只在 [0, 10) 生效 ==")
    eng.set_edge_interval("e_at", 0, 10, at=0)
    eng.set_edge_interval("e_at_dup", 0, 10, at=0)
    eng.add_edge(Edge("e_bt2", "B", "T"), at=0)  # 恢复 B->T，保证 T 在 t>=10 仍可达
    for t in (9, 10):
        snap = eng.snapshot(t)
        print(f"  t={t}: e_at 生效={g.edges['e_at'].active_at(t)}, "
              f"idom[T]={snap.idom.get('T')}")

    print("\n== 5. 加边使不可达环可达 ==")
    report = eng.add_edge(Edge("e_sx", "S", "X"), at=0)
    print(f"  新可达: {sorted(report.became_reachable)}")
    show(eng, 0)


if __name__ == "__main__":
    main()
