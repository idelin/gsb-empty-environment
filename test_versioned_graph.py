import random
import unittest

from versioned_graph import VersionedGraph


def build_example():
    """题目示例：v1 加 AB，v2 加 BC，v3 删 AB。"""
    g = VersionedGraph()
    g.add_edge("AB", "A", "B", version=1)
    g.add_edge("BC", "B", "C", version=2)
    g.remove_edge("AB", version=3)
    return g


class ExampleScenarioTest(unittest.TestCase):
    def test_connectivity_across_versions(self):
        g = build_example()
        g.query_connected(1, "A", "B")   # True：AB 刚加入（左闭）
        g.query_connected(2, "A", "C")   # True：A-B-C
        g.query_connected(3, "A", "C")   # False：AB 已删（右开）
        g.query_connected(3, "B", "C")   # True：BC 仍在
        g.query_connected(1, "A", "C")   # False：C 尚未出现
        self.assertEqual(g.execute(), [True, True, False, True, False])

    def test_component_sizes_with_isolated_vertex(self):
        g = build_example()
        g.add_vertex("D", version=2)     # 孤立点
        g.query_component_size(1, "A")   # 2: {A,B}
        g.query_component_size(2, "A")   # 3: {A,B,C}
        g.query_component_size(3, "A")   # 1: {A}
        g.query_component_size(3, "B")   # 2: {B,C}
        g.query_component_size(2, "D")   # 1: 孤立点自成分量
        g.query_component_size(1, "D")   # 0: D 尚未注册
        self.assertEqual(g.execute(), [2, 3, 1, 2, 1, 0])

    def test_deletion_only_affects_later_versions(self):
        g = build_example()
        for v in (1, 2, 3, 4):
            g.query_connected(v, "A", "B")
        self.assertEqual(g.execute(), [True, True, False, False])


class MultigraphTest(unittest.TestCase):
    def test_parallel_edges_keep_connectivity(self):
        g = VersionedGraph()
        g.add_edge("e1", "A", "B", version=1)
        g.add_edge("e2", "A", "B", version=1)   # 重边
        g.remove_edge("e1", version=2)
        g.query_connected(1, "A", "B")          # True
        g.query_connected(2, "A", "B")          # True：e2 仍在
        g.query_component_size(2, "B")          # 2
        self.assertEqual(g.execute(), [True, True, 2])

    def test_self_loop(self):
        g = VersionedGraph()
        g.add_edge("loop", "A", "A", version=1)
        g.query_connected(1, "A", "A")
        g.query_component_size(1, "A")
        self.assertEqual(g.execute(), [True, 1])


class QuerySemanticsTest(unittest.TestCase):
    def test_query_order_does_not_matter(self):
        specs = [(2, "A", "C"), (2, "B", "C"), (2, "A", "B"), (2, "D", "A"),
                 (3, "A", "C"), (3, "B", "C")]

        def run(order):
            g = build_example()
            g.add_vertex("D", version=2)
            for i in order:
                v, u, w = specs[i]
                g.query_connected(v, u, w)
            return g.execute()

        order = [3, 0, 5, 1, 4, 2]
        forward = run(range(len(specs)))
        shuffled = run(order)
        restored = [shuffled[order.index(i)] for i in range(len(specs))]
        self.assertEqual(forward, restored)
        self.assertEqual(forward, [True, True, True, False, False, True])

    def test_unknown_vertices(self):
        g = build_example()
        g.query_connected(1, "A", "Z")    # False
        g.query_connected(1, "Y", "Z")    # False
        g.query_component_size(1, "Z")    # 0
        self.assertEqual(g.execute(), [False, False, 0])

    def test_execute_returns_in_registration_order_and_clears_queue(self):
        g = build_example()
        g.query_connected(2, "A", "C")
        g.query_component_size(2, "A")
        self.assertEqual(g.execute(), [True, 3])
        self.assertEqual(g.execute(), [])   # 队列已清空

    def test_sparse_large_versions(self):
        g = VersionedGraph()
        g.add_edge("e", "A", "B", version=10**6)
        g.remove_edge("e", version=10**9)
        g.query_connected(10**6, "A", "B")        # True（左闭）
        g.query_connected(10**9 - 1, "A", "B")    # True
        g.query_connected(10**9, "A", "B")        # False（右开）
        self.assertEqual(g.execute(), [True, True, False])


class ValidationTest(unittest.TestCase):
    def test_invalid_operations(self):
        g = VersionedGraph()
        g.add_edge("e", "A", "B", version=1)
        with self.assertRaises(KeyError):
            g.remove_edge("missing", version=2)
        with self.assertRaises(ValueError):
            g.add_edge("e", "A", "C", version=2)      # 边 ID 重复
        with self.assertRaises(ValueError):
            g.remove_edge("e", version=1)             # 删除版本必须更晚
        g.remove_edge("e", version=2)
        with self.assertRaises(ValueError):
            g.remove_edge("e", version=3)             # 重复删除
        with self.assertRaises(ValueError):
            g.add_vertex("X", version=-1)
        with self.assertRaises(ValueError):
            g.add_edge("bad", "", "B", version=1)


class BruteForceComparisonTest(unittest.TestCase):
    """随机事件流下与“逐版本重建图”的暴力解对照。"""

    def test_matches_brute_force(self):
        rng = random.Random(20260924)
        vertices = [f"v{i}" for i in range(6)]
        horizon = 25
        g = VersionedGraph()
        births: dict[str, int] = {}
        edges: list[list] = []   # [u, v, add, delete|None]
        live: list[int] = []

        def touch(v, t):
            if v not in births or t < births[v]:
                births[v] = t

        for t in range(1, horizon):
            for _ in range(rng.randint(0, 2)):
                action = rng.choice(["add", "add", "del", "vertex"])
                if action == "add":
                    u, v = rng.choice(vertices), rng.choice(vertices)
                    eid = f"e{len(edges)}"
                    g.add_edge(eid, u, v, version=t)
                    edges.append([u, v, t, None])
                    live.append(len(edges) - 1)
                    touch(u, t)
                    touch(v, t)
                elif action == "del" and live:
                    i = live.pop(rng.randrange(len(live)))
                    edges[i][3] = t
                    g.remove_edge(f"e{i}", version=t)
                elif action == "vertex":
                    v = rng.choice(vertices)
                    g.add_vertex(v, version=t)
                    touch(v, t)

        def brute(t, kind, u, v=None):
            active = {x for x, b in births.items() if b <= t}
            parent = {x: x for x in active}

            def find(x):
                while parent[x] != x:
                    x = parent[x]
                return x

            for a, b, add, delete in edges:
                if add <= t and (delete is None or t < delete):
                    ra, rb = find(a), find(b)
                    if ra != rb:
                        parent[ra] = rb
            if kind == "connected":
                return u in active and v in active and find(u) == find(v)
            if u not in active:
                return 0
            root = find(u)
            return sum(1 for x in active if find(x) == root)

        expected = []
        for _ in range(400):
            t = rng.randrange(0, horizon + 3)
            u, v = rng.choice(vertices), rng.choice(vertices)
            if rng.random() < 0.5:
                g.query_connected(t, u, v)
                expected.append(brute(t, "connected", u, v))
            else:
                g.query_component_size(t, u)
                expected.append(brute(t, "size", u))
        self.assertEqual(g.execute(), expected)


if __name__ == "__main__":
    unittest.main()
