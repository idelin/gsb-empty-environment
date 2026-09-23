"""运行样例：python3 demo.py"""
from dynamic_graph import DynamicMultigraph


def show(g: DynamicMultigraph, t: float, label: str) -> None:
    a = g.analyze(t)
    print(f"--- {label} (t={t}) ---")
    print("桥:            ", sorted(a.bridges) or "无")
    print("割点:          ", sorted(a.articulation_points) or "无")
    print("双连通块(边集):", [sorted(b) for b in a.blocks])
    print("连通分量(点集):", [sorted(c) for c in a.components])


def main() -> None:
    g = DynamicMultigraph()
    # 三角形 A-B-C，尾接 D；E、F 间两条平行边；G 上自环；H 是孤立点
    g.apply_batch(adds=[
        ("ab", "A", "B"), ("bc", "B", "C"), ("ca", "C", "A"),
        ("cd", "C", "D"),
        ("ef1", "E", "F"), ("ef2", "E", "F"),
        ("gg", "G", "G"),
    ])
    g.add_vertex("H")

    show(g, 0, "初始图")
    print("cd 失效分开:  ", [sorted(s) for s in g.separated_sets("cd")])
    print("ab 失效分开:  ", g.separated_sets("ab"), "（非桥，分不开）")
    print("ef1 失效分开: ", g.separated_sets("ef1"), "（平行边，分不开）")

    # 批量更新：删掉一条平行边，新增只在 [10, 20) 存活的边 D-E
    g.apply_batch(adds=[("de", "D", "E", 10, 20)], removes=["ef2"])

    show(g, 0, "批量更新后：ef1 变桥；de 尚未生效")
    print("ef1 失效分开: ", [sorted(s) for s in g.separated_sets("ef1")])

    show(g, 15, "t=15：de 在 [10,20) 内存活，两个分量连成一片")
    print("de 失效分开:  ", [sorted(s) for s in g.separated_sets("de", 15)])

    show(g, 20, "t=20：de 到期（左闭右开，20 不存活）")


if __name__ == "__main__":
    main()
