"""dynamic_graph 的单元测试。

包含用户指定的关键场景：
- A、B 间两条平行边均非桥；删一条后剩余边变桥；
- 三角形尾接 D 时，连接 D 的边是桥；
- 批量更新结果与全量（朴素）算法一致；
- 平行边 / 自环的多重图语义；孤立点保留；时间区间左闭右开。
"""
import math
import random
import unittest

from dynamic_graph import DynamicMultigraph


# --------------------------------------------------------------------- #
# 朴素全量参考实现（用于交叉验证）
# --------------------------------------------------------------------- #
def _component_count(vertices, edges):
    """并查集数连通分量。edges 为 (u, v) 列表。"""
    parent = {v: v for v in vertices}

    def find(x):
        while parent[x] != x:
            parent[x] = parent[parent[x]]
            x = parent[x]
        return x

    for u, v in edges:
        ru, rv = find(u), find(v)
        if ru != rv:
            parent[ru] = rv
    return len({find(v) for v in vertices})


def naive_bridges(g: DynamicMultigraph, t: float) -> set[str]:
    active = g.active_edges(t)
    base = _component_count(g.vertices, [(e.u, e.v) for e in active])
    out = set()
    for e in active:
        if e.is_loop():
            continue  # 自环永远不是桥
        rest = [(x.u, x.v) for x in active if x.edge_id != e.edge_id]
        if _component_count(g.vertices, rest) > base:
            out.add(e.edge_id)
    return out


def naive_articulations(g: DynamicMultigraph, t: float) -> set[str]:
    active = [(e.u, e.v) for e in g.active_edges(t)]
    base = _component_count(g.vertices, active)
    out = set()
    for v in g.vertices:
        rest_v = g.vertices - {v}
        rest_e = [(a, b) for a, b in active if a != v and b != v]
        if _component_count(rest_v, rest_e) > base:
            out.add(v)
    return out


def naive_separation(g: DynamicMultigraph, edge_id: str, t: float):
    """朴素法：删掉该边后，原连通块被分成的顶点集合对；分不开则 None。"""
    active = g.active_edges(t)
    edge = next((e for e in active if e.edge_id == edge_id), None)
    if edge is None or edge.is_loop():
        return None
    rest = [(e.u, e.v) for e in active if e.edge_id != edge_id]
    if _component_count(g.vertices, rest) <= _component_count(
            g.vertices, [(e.u, e.v) for e in active]):
        return None
    # 从两端各自 BFS（无权图，用邻接表）
    adj = {v: [] for v in g.vertices}
    for a, b in rest:
        adj[a].append(b)
        adj[b].append(a)

    def reach(s):
        seen, stack = {s}, [s]
        while stack:
            for nb in adj[stack.pop()]:
                if nb not in seen:
                    seen.add(nb)
                    stack.append(nb)
        return frozenset(seen)

    return frozenset({reach(edge.u), reach(edge.v)})


# --------------------------------------------------------------------- #
# 测试
# --------------------------------------------------------------------- #
class TestParallelEdges(unittest.TestCase):
    """A、B 间两条平行边：均非桥；删一条后剩余边变桥。"""

    def setUp(self):
        self.g = DynamicMultigraph()
        self.g.add_edge("e1", "A", "B")
        self.g.add_edge("e2", "A", "B")

    def test_neither_parallel_edge_is_bridge(self):
        self.assertEqual(self.g.bridges(), set())
        self.assertEqual(self.g.articulation_points(), set())
        # 两条平行边构成一个双连通块（长度为 2 的环）
        self.assertEqual(self.g.biconnected_components(),
                         [frozenset({"e1", "e2"})])
        # 平行边失效不会分开任何顶点
        self.assertIsNone(self.g.separated_sets("e1"))

    def test_remaining_edge_becomes_bridge_after_delete(self):
        self.g.apply_batch(removes=["e2"])
        self.assertEqual(self.g.bridges(), {"e1"})
        # 两点一边的图没有割点（删任一端点，剩余单点仍连通）
        self.assertEqual(self.g.articulation_points(), set())
        self.assertEqual(self.g.biconnected_components(), [frozenset({"e1"})])
        sides = self.g.separated_sets("e1")
        self.assertEqual(frozenset(sides), frozenset({frozenset("A"), frozenset("B")}))


class TestTriangleWithTail(unittest.TestCase):
    """三角形 A-B-C 尾接 D：连接 D 的边是桥，C 是割点。"""

    def setUp(self):
        self.g = DynamicMultigraph()
        for eid, u, v in [("ab", "A", "B"), ("bc", "B", "C"),
                          ("ca", "C", "A"), ("cd", "C", "D")]:
            self.g.add_edge(eid, u, v)

    def test_tail_edge_is_bridge(self):
        self.assertEqual(self.g.bridges(), {"cd"})
        self.assertEqual(self.g.articulation_points(), {"C"})
        self.assertEqual(
            set(self.g.biconnected_components()),
            {frozenset({"ab", "bc", "ca"}), frozenset({"cd"})},
        )

    def test_separated_sets_of_tail(self):
        sides = self.g.separated_sets("cd")
        self.assertEqual(frozenset(sides),
                         frozenset({frozenset({"A", "B", "C"}), frozenset({"D"})}))
        # 三角形上的边失效不会分开任何顶点
        self.assertIsNone(self.g.separated_sets("ab"))


class TestSelfLoopsAndIsolatedVertices(unittest.TestCase):
    def test_self_loop_semantics(self):
        g = DynamicMultigraph()
        g.add_edge("loop", "A", "A")
        g.add_edge("ab", "A", "B")
        g.add_edge("bc", "B", "C")
        # 自环不是桥、不影响割点判定；ab、bc 是桥，B 是割点
        self.assertEqual(g.bridges(), {"ab", "bc"})
        self.assertEqual(g.articulation_points(), {"B"})
        # 自环单独成块
        self.assertEqual(set(g.biconnected_components()),
                         {frozenset({"ab"}), frozenset({"bc"}), frozenset({"loop"})})
        self.assertIsNone(g.separated_sets("loop"))

    def test_isolated_vertices_kept(self):
        g = DynamicMultigraph()
        g.add_vertex("lonely")
        g.add_edge("ab", "A", "B")
        self.assertIn("lonely", g.vertices)
        self.assertIn(frozenset({"lonely"}), g.components())
        self.assertEqual(g.articulation_points(), set())
        # 删除边后顶点保留为孤立点
        g.remove_edge("ab")
        self.assertEqual(g.components(),
                         [frozenset({"A"}), frozenset({"B"}), frozenset({"lonely"})])
        self.assertEqual(g.bridges(), set())


class TestTimeIntervals(unittest.TestCase):
    """时间区间左闭右开 [start, end)。"""

    def setUp(self):
        self.g = DynamicMultigraph()
        self.g.add_edge("e1", "A", "B", start=2, end=5)
        self.g.add_edge("e2", "A", "B")  # 永久平行边

    def test_left_closed_right_open(self):
        self.assertEqual(self.g.active_edges(1), [self.g._edges["e2"]])
        self.assertEqual({e.edge_id for e in self.g.active_edges(2)}, {"e1", "e2"})
        self.assertEqual({e.edge_id for e in self.g.active_edges(4.999)}, {"e1", "e2"})
        self.assertEqual({e.edge_id for e in self.g.active_edges(5)}, {"e2"})

    def test_bridge_status_changes_over_time(self):
        # t<2 或 t>=5：只剩 e2，是桥；2<=t<5：平行边都在，无桥
        self.assertEqual(self.g.bridges(0), {"e2"})
        self.assertEqual(self.g.bridges(2), set())
        self.assertEqual(self.g.bridges(5), {"e2"})
        self.assertIsNone(self.g.separated_sets("e1", t=0))  # e1 此刻不存活
        sides = self.g.separated_sets("e2", t=10)
        self.assertEqual(frozenset(sides), frozenset({frozenset("A"), frozenset("B")}))


class TestBatchUpdates(unittest.TestCase):
    def test_batch_add_remove_atomic_view(self):
        g = DynamicMultigraph()
        g.apply_batch(adds=[("ab", "A", "B"), ("bc", "B", "C"), ("ca", "C", "A"),
                            ("cd", "C", "D"), ("ef1", "E", "F"), ("ef2", "E", "F")])
        self.assertEqual(g.bridges(), {"cd"})
        g.apply_batch(adds=[("de", "D", "E", 10, 20)], removes=["ef2"])
        # 批量后：ef1 变桥；de 只在 [10,20) 存活
        self.assertEqual(g.bridges(0), {"cd", "ef1"})
        self.assertEqual(g.bridges(15), {"cd", "ef1", "de"})
        self.assertEqual(g.articulation_points(15), {"C", "D", "E"})
        self.assertEqual(g.bridges(20), {"cd", "ef1"})

    def test_batch_equals_incremental_build(self):
        """一次性批量构建与逐条添加结果一致。"""
        specs = [("e1", "A", "B"), ("e2", "B", "C"), ("e3", "C", "A"),
                 ("e4", "C", "D"), ("e5", "D", "D"), ("e6", "A", "B")]
        g1 = DynamicMultigraph()
        g1.apply_batch(adds=specs)
        g2 = DynamicMultigraph()
        for s in specs:
            g2.add_edge(*s)
        for t in (0, 1):
            self.assertEqual(g1.bridges(t), g2.bridges(t))
            self.assertEqual(g1.articulation_points(t), g2.articulation_points(t))
            self.assertEqual(set(g1.biconnected_components(t)),
                             set(g2.biconnected_components(t)))
            self.assertEqual(set(g1.components(t)), set(g2.components(t)))

    def test_errors(self):
        g = DynamicMultigraph()
        g.add_edge("e1", "A", "B")
        with self.assertRaises(ValueError):
            g.add_edge("e1", "A", "C")          # 边 ID 重复
        with self.assertRaises(ValueError):
            g.add_edge("e2", "A", "C", 5, 5)    # 空区间
        with self.assertRaises(KeyError):
            g.remove_edge("nope")
        with self.assertRaises(KeyError):
            g.separated_sets("nope")


class TestAgainstNaiveFullRecompute(unittest.TestCase):
    """随机多重图 + 随机批量更新：结果必须与朴素全量算法一致。"""

    def test_randomized(self):
        rng = random.Random(20260923)
        for trial in range(60):
            g = DynamicMultigraph()
            verts = [chr(ord("A") + i) for i in range(rng.randint(1, 7))]
            for v in verts:
                g.add_vertex(v)
            counter = 0
            live: list[str] = []
            for _ in range(rng.randint(1, 6)):  # 若干轮批量更新
                adds, removes = [], []
                removable = list(live)  # 只能删更早批次已存在的边
                for _ in range(rng.randint(0, 5)):
                    u, v = rng.choice(verts), rng.choice(verts)  # 允许自环/平行边
                    if rng.random() < 0.4:
                        s = rng.randint(0, 3)
                        e = s + rng.randint(1, 3)
                        adds.append((f"e{counter}", u, v, s, e))
                    else:
                        adds.append((f"e{counter}", u, v))
                    live.append(f"e{counter}")
                    counter += 1
                for _ in range(rng.randint(0, min(3, len(removable)))):
                    victim = removable.pop(rng.randrange(len(removable)))
                    live.remove(victim)
                    removes.append(victim)
                g.apply_batch(adds=adds, removes=removes)
                for t in (0, 1, 2, 3, 4, 5):
                    with self.subTest(trial=trial, t=t):
                        self.assertEqual(g.bridges(t), naive_bridges(g, t))
                        self.assertEqual(g.articulation_points(t),
                                         naive_articulations(g, t))
                        # 每座桥的分离集合与朴素法一致
                        for b in g.bridges(t):
                            sides = g.separated_sets(b, t)
                            self.assertEqual(frozenset(sides),
                                             naive_separation(g, b, t))
                        # 非桥边不会分开图
                        for e in g.active_edges(t):
                            if e.edge_id not in g.bridges(t):
                                self.assertIsNone(
                                    g.separated_sets(e.edge_id, t))
                        # 块性质：划分所有存活边；单点块必为桥或自环
                        blocks = g.biconnected_components(t)
                        all_edges = [e.edge_id for e in g.active_edges(t)]
                        self.assertEqual(
                            sorted(x for blk in blocks for x in blk),
                            sorted(all_edges))
                        for blk in blocks:
                            if len(blk) == 1:
                                (only,) = blk
                                e = g._edges[only]
                                self.assertTrue(
                                    only in g.bridges(t) or e.is_loop())


if __name__ == "__main__":
    unittest.main(verbosity=2)
