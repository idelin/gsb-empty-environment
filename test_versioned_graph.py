"""versioned_graph 的单元测试。

覆盖：题目示例、删除只影响后续版本、孤立点保留、多重边、
同版本查询顺序无关性、边界与异常，以及与暴力实现的随机对拍。
"""

import random
import unittest

from versioned_graph import RollbackDSU, VersionedGraph


def brute_force(ops, queries):
    """参照实现：对每个查询版本从头重放全部操作（允许慢，用于对拍）。"""
    vertices = set()
    for op in ops:
        if op[0] == "add_vertex":
            vertices.add(op[1])
        elif op[0] == "add_edge":
            vertices.update((op[2], op[3]))

    def state_at(version):
        parent = {v: v for v in vertices}
        size = {v: 1 for v in vertices}

        def find(x):
            while parent[x] != x:
                parent[x] = parent[parent[x]]
                x = parent[x]
            return x

        active = {}
        for op in ops:
            kind = op[0]
            if kind == "add_edge":
                _, eid, u, v, ver = op
                if ver <= version:
                    active[eid] = (u, v)
            elif kind == "remove_edge":
                _, eid, ver = op
                if ver <= version:
                    active.pop(eid, None)
        for u, v in active.values():
            ru, rv = find(u), find(v)
            if ru != rv:
                parent[ru] = rv
                size[rv] += size[ru]
        return find, size

    answers = []
    for kind, args, version in queries:
        find, size = state_at(version)
        if kind == "connected":
            answers.append(find(args[0]) == find(args[1]))
        else:
            answers.append(size[find(args[0])])
    return answers


class TestGivenExample(unittest.TestCase):
    """版本1 加 AB，版本2 加 BC，版本3 删 AB。"""

    def setUp(self):
        g = VersionedGraph()
        g.add_edge("e1", "A", "B", version=1)
        g.add_edge("e2", "B", "C", version=2)
        g.remove_edge("e1", version=3)
        self.g = g

    def test_connectivity_across_versions(self):
        self.g.query_connected("A", "C", version=2)
        self.g.query_connected("A", "C", version=3)
        r1, r2 = self.g.solve()
        self.assertIs(r1.value, True)   # 版本2：A-B-C 连通
        self.assertIs(r2.value, False)  # 版本3：AB 已删，不连通

    def test_component_sizes(self):
        self.g.query_component_size("A", version=2)
        self.g.query_component_size("A", version=3)
        self.g.query_component_size("B", version=3)
        r1, r2, r3 = self.g.solve()
        self.assertEqual((r1.value, r2.value, r3.value), (3, 1, 2))

    def test_delete_does_not_affect_earlier_versions(self):
        self.g.query_connected("A", "B", version=1)
        self.g.query_connected("A", "B", version=2)
        self.g.query_connected("A", "B", version=3)
        r1, r2, r3 = self.g.solve()
        self.assertEqual([r1.value, r2.value, r3.value], [True, True, False])


class TestSemantics(unittest.TestCase):
    def test_isolated_vertex_kept(self):
        g = VersionedGraph()
        g.add_vertex("Solo", version=1)
        g.add_edge("e", "X", "Y", version=1)
        g.remove_edge("e", version=2)
        g.query_component_size("Solo", version=2)
        g.query_component_size("X", version=2)  # 删边后仍是孤立点
        g.query_connected("X", "Solo", version=2)
        r1, r2, r3 = g.solve()
        self.assertEqual((r1.value, r2.value), (1, 1))
        self.assertIs(r3.value, False)

    def test_multiedges(self):
        g = VersionedGraph()
        g.add_edge("m1", "P", "Q", version=1)
        g.add_edge("m2", "P", "Q", version=1)  # 平行重边
        g.remove_edge("m1", version=2)
        g.query_connected("P", "Q", version=2)  # m2 仍在
        g.query_component_size("P", version=2)
        g.remove_edge("m2", version=3)
        g.query_connected("P", "Q", version=3)
        r1, r2, r3 = g.solve()
        self.assertIs(r1.value, True)
        self.assertEqual(r2.value, 2)
        self.assertIs(r3.value, False)

    def test_same_version_add_then_delete_is_never_visible(self):
        g = VersionedGraph()
        g.add_edge("e", "A", "B", version=2)
        g.remove_edge("e", version=2)  # 左闭右开：区间为空
        g.query_connected("A", "B", version=2)
        (res,) = g.solve()
        self.assertIs(res.value, False)

    def test_edge_id_reusable_after_removal(self):
        g = VersionedGraph()
        g.add_edge("e", "A", "B", version=1)
        g.remove_edge("e", version=2)
        g.add_edge("e", "A", "B", version=3)  # 同 ID 重新加入
        g.query_connected("A", "B", version=1)
        g.query_connected("A", "B", version=2)
        g.query_connected("A", "B", version=3)
        r1, r2, r3 = g.solve()
        self.assertEqual([r1.value, r2.value, r3.value], [True, False, True])

    def test_query_order_within_version_irrelevant(self):
        def build(order):
            g = VersionedGraph()
            g.add_edge("e1", "A", "B", version=1)
            g.add_edge("e2", "B", "C", version=2)
            g.remove_edge("e1", version=3)
            queries = {
                "conn_ac": lambda: g.query_connected("A", "C", version=2),
                "size_a": lambda: g.query_component_size("A", version=2),
                "conn_ab": lambda: g.query_connected("A", "B", version=2),
            }
            handles = {name: queries[name]() for name in order}
            results = g.solve()
            return {name: results[h].value for name, h in handles.items()}

        forward = build(["conn_ac", "size_a", "conn_ab"])
        reverse = build(["conn_ab", "size_a", "conn_ac"])
        self.assertEqual(forward, reverse)
        self.assertEqual(forward, {"conn_ac": True, "size_a": 3, "conn_ab": True})

    def test_string_ids_and_long_chain(self):
        g = VersionedGraph()
        names = [f"node-{i:03d}" for i in range(50)]
        for i in range(49):
            g.add_edge(f"edge-{i}", names[i], names[i + 1], version=i + 1)
        g.query_connected(names[0], names[-1], version=49)
        g.query_component_size(names[25], version=49)
        g.remove_edge("edge-24", version=50)  # 从中间断开
        g.query_connected(names[0], names[-1], version=50)
        g.query_component_size(names[0], version=50)
        r1, r2, r3, r4 = g.solve()
        self.assertIs(r1.value, True)
        self.assertEqual(r2.value, 50)
        self.assertIs(r3.value, False)
        self.assertEqual(r4.value, 25)


class TestErrors(unittest.TestCase):
    def test_remove_missing_edge(self):
        g = VersionedGraph()
        with self.assertRaises(KeyError):
            g.remove_edge("nope", version=1)

    def test_double_remove(self):
        g = VersionedGraph()
        g.add_edge("e", "A", "B", version=1)
        g.remove_edge("e", version=2)
        with self.assertRaises(KeyError):
            g.remove_edge("e", version=3)

    def test_duplicate_active_edge_id(self):
        g = VersionedGraph()
        g.add_edge("e", "A", "B", version=1)
        with self.assertRaises(ValueError):
            g.add_edge("e", "A", "C", version=2)

    def test_query_unknown_vertex(self):
        g = VersionedGraph()
        g.add_edge("e", "A", "B", version=1)
        g.query_connected("A", "Ghost", version=1)
        with self.assertRaises(KeyError):
            g.solve()

    def test_invalid_version(self):
        g = VersionedGraph()
        with self.assertRaises(ValueError):
            g.add_edge("e", "A", "B", version=0)

    def test_mutation_after_solve_rejected(self):
        g = VersionedGraph()
        g.add_edge("e", "A", "B", version=1)
        g.solve()
        with self.assertRaises(RuntimeError):
            g.add_edge("e2", "B", "C", version=2)


class TestRollbackDSU(unittest.TestCase):
    def test_rollback_restores_state(self):
        dsu = RollbackDSU()
        dsu.union("a", "b")
        mark = dsu.mark()
        dsu.union("b", "c")
        dsu.union("c", "d")
        self.assertTrue(dsu.connected("a", "d"))
        self.assertEqual(dsu.component_size("a"), 4)
        dsu.rollback(mark)
        self.assertFalse(dsu.connected("a", "c"))
        self.assertEqual(dsu.component_size("a"), 2)


class TestRandomizedAgainstBruteForce(unittest.TestCase):
    """随机操作序列 + 随机历史查询，与暴力重放实现对拍。"""

    def test_random_differential(self):
        rng = random.Random(20260924)
        for trial in range(30):
            n_vertices = rng.randint(2, 8)
            vertices = [f"v{i}" for i in range(n_vertices)]
            max_version = rng.randint(1, 25)
            ops, queries = [], []
            active = {}
            edge_seq = 0
            g = VersionedGraph()
            for v in vertices:  # 显式注册全部顶点（含可能从未连边的孤立点）
                ver = rng.randint(1, max_version)
                g.add_vertex(v, ver)
                ops.append(("add_vertex", v, ver))
            for _ in range(rng.randint(0, 40)):
                ver = rng.randint(1, max_version)
                if active and rng.random() < 0.35:
                    eid = rng.choice(list(active))
                    g.remove_edge(eid, ver)
                    ops.append(("remove_edge", eid, ver))
                    del active[eid]
                else:
                    eid = f"e{edge_seq}"
                    edge_seq += 1
                    u, w = rng.choice(vertices), rng.choice(vertices)
                    g.add_edge(eid, u, w, ver)
                    ops.append(("add_edge", eid, u, w, ver))
                    active[eid] = ver
            for _ in range(rng.randint(1, 30)):
                ver = rng.randint(1, max_version)
                if rng.random() < 0.5:
                    u, w = rng.choice(vertices), rng.choice(vertices)
                    g.query_connected(u, w, ver)
                    queries.append(("connected", (u, w), ver))
                else:
                    u = rng.choice(vertices)
                    g.query_component_size(u, ver)
                    queries.append(("component_size", (u,), ver))
            got = [r.value for r in g.solve()]
            want = brute_force(ops, queries)
            self.assertEqual(got, want, f"对拍失败：trial={trial}")


if __name__ == "__main__":
    unittest.main(verbosity=2)
