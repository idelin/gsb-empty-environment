"""运行样例。

场景一：题目给定序列 —— 版本1 加 AB，版本2 加 BC，版本3 删 AB。
        预期版本2 上 A、C 连通，版本3 上不连通。
场景二：孤立点保留 + 重边（多重边）语义。
"""

from versioned_graph import VersionedGraph


def scenario_basic() -> None:
    print("== 场景一：版本轴上的增删与历史查询 ==")
    g = VersionedGraph()
    g.add_edge("e1", "A", "B", version=1)
    g.add_edge("e2", "B", "C", version=2)
    g.remove_edge("e1", version=3)      # 删除只影响版本3及以后

    g.query_connected("A", "C", version=2)   # 期望 True：A-B-C
    g.query_connected("A", "C", version=3)   # 期望 False：AB 已删
    g.query_connected("B", "C", version=3)   # 期望 True：BC 仍在
    g.query_component_size("A", version=2)   # 期望 3：{A,B,C}
    g.query_component_size("A", version=3)   # 期望 1：A 成为孤立点
    g.query_component_size("B", version=3)   # 期望 2：{B,C}

    for res in g.solve():
        print(" ", res)


def scenario_multiedge_isolated() -> None:
    print("== 场景二：孤立点保留与重边 ==")
    g = VersionedGraph()
    g.add_vertex("D", version=1)              # 显式孤立点
    g.add_edge("m1", "X", "Y", version=1)
    g.add_edge("m2", "X", "Y", version=2)     # 与 m1 平行的重边
    g.remove_edge("m1", version=3)            # 删掉一条，仍连通

    g.query_connected("X", "Y", version=3)    # 期望 True：m2 仍在
    g.query_component_size("X", version=3)    # 期望 2
    g.query_component_size("D", version=3)    # 期望 1：孤立点保留
    g.query_connected("X", "D", version=2)    # 期望 False

    for res in g.solve():
        print(" ", res)


if __name__ == "__main__":
    scenario_basic()
    scenario_multiedge_isolated()
