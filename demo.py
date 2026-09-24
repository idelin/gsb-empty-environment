"""运行样例：版本 1 加 AB，版本 2 加 BC，版本 3 删 AB（区间左闭右开）。"""

from versioned_graph import VersionedGraph


def main() -> None:
    g = VersionedGraph()
    g.add_vertex("D", version=1)              # 孤立点，自版本 1 起存在
    g.add_edge("AB", "A", "B", version=1)
    g.add_edge("BC", "B", "C", version=2)
    g.remove_edge("AB", version=3)            # 只影响版本 >= 3

    print("事件：v1 +AB、+孤立点D；v2 +BC；v3 -AB    （区间左闭右开 [add, del)）")
    print("批量查询结果：")

    queries = [
        ("connected", 1, "A", "B"),
        ("connected", 2, "A", "C"),
        ("connected", 3, "A", "C"),
        ("connected", 3, "B", "C"),
        ("component_size", 2, "A"),
        ("component_size", 3, "A"),
        ("component_size", 3, "B"),
        ("component_size", 3, "D"),
    ]
    for q in queries:
        if q[0] == "connected":
            _, v, u, w = q
            g.query_connected(v, u, w)
        else:
            _, v, u = q
            g.query_component_size(v, u)

    for q, ans in zip(queries, g.execute()):
        if q[0] == "connected":
            _, v, u, w = q
            print(f"  v{v} connected({u}, {w})      -> {ans}")
        else:
            _, v, u = q
            print(f"  v{v} component_size({u}) -> {ans}")

    # 重边：删除一条平行边不影响连通性
    h = VersionedGraph()
    h.add_edge("e1", "X", "Y", version=1)
    h.add_edge("e2", "X", "Y", version=1)     # 平行边
    h.remove_edge("e1", version=2)
    h.query_connected(2, "X", "Y")
    print(f"重边场景：v2 删除 e1 后 connected(X, Y) -> {h.execute()[0]}（e2 仍在）")


if __name__ == "__main__":
    main()
