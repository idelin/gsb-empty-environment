import random
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from symchol.mindeg import minimum_degree_order
from symchol.oracle import oracle_order, oracle_symbolic
from symchol.incremental import IncrementalSymbolicCholesky


def random_graph(rng, n, p):
    edges = set()
    for u in range(n):
        for v in range(u + 1, n):
            if rng.random() < p:
                edges.add((u, v))
    return edges


def traces_equal(res, ref):
    """逐步对比排序轨迹：置换、每步度数、活跃邻居、填充。"""
    if res.perm != ref["perm"]:
        return False
    for a, b in zip(res.steps, ref["steps"]):
        if (a["vertex"], a["degree"], a["active_neighbors"], a["fill"]) != \
           (b["vertex"], b["degree"], b["active_neighbors"], b["fill"]):
            return False
    return res.fill_edges == ref["fill_edges"] and res.l_pattern == ref["l_pattern"]


class TestOrdering(unittest.TestCase):
    def test_random_graphs_match_oracle(self):
        for seed in range(30):
            rng = random.Random(1000 + seed)
            n = rng.randint(1, 14)
            p = rng.choice([0.15, 0.3, 0.5, 0.8])
            edges = random_graph(rng, n, p)
            res = minimum_degree_order(n, edges)
            ref = oracle_order(n, edges)
            self.assertTrue(traces_equal(res, ref),
                            f"seed={seed} n={n} p={p} edges={sorted(edges)}")

    def test_four_cycle_fill_chord(self):
        edges = {(0, 1), (1, 2), (2, 3), (0, 3)}
        res = minimum_degree_order(4, edges)
        self.assertEqual(res.fill_edges, {(1, 3)})
        self.assertEqual(res.steps[0]["vertex"], 0)
        self.assertEqual(res.steps[0]["active_neighbors"], [1, 3])
        self.assertEqual(res.steps[0]["fill"], [(1, 3)])
        self.assertTrue(traces_equal(res, oracle_order(4, edges)))

    def test_star_high_degree_center(self):
        # 星形：中心 0 高度数，叶子先消元且零填充
        edges = {(0, i) for i in range(1, 6)}
        res = minimum_degree_order(6, edges)
        # 叶子依次消元；最后中心 0 与叶子 5 同为度 1，并列取最小 ID
        self.assertEqual(res.perm, [1, 2, 3, 4, 0, 5])
        self.assertEqual(res.fill_edges, set())
        self.assertTrue(traces_equal(res, oracle_order(6, edges)))

    def test_overlapping_elements_degree_dedup(self):
        # 消元 0、1 后，顶点 2 同时被元素 {2,3}（两次）与变量 4 覆盖；
        # 精确度数为 |{3,4}| = 2。若误用元素大小求和会得到 5，从而错选 4。
        edges = {(0, 2), (0, 3), (1, 2), (1, 3), (2, 4), (3, 4)}
        res = minimum_degree_order(5, edges)
        self.assertEqual(res.steps[0]["vertex"], 0)
        self.assertEqual(res.steps[1]["vertex"], 1)
        self.assertEqual(res.steps[2]["vertex"], 2)
        self.assertEqual(res.steps[2]["degree"], 2)
        self.assertTrue(traces_equal(res, oracle_order(5, edges)))

    def test_isolated_vertices_and_components(self):
        edges = {(0, 1), (1, 2), (4, 5)}  # 顶点 3 孤立；两个不连通块
        res = minimum_degree_order(6, edges)
        self.assertEqual(res.perm[0], 3)  # 孤立点度 0 最先消元
        self.assertTrue(traces_equal(res, oracle_order(6, edges)))

    def test_memory_report_separates_internal_and_output(self):
        rng = random.Random(7)
        edges = random_graph(rng, 10, 0.4)
        res = minimum_degree_order(10, edges)
        rep = res.memory_report()
        self.assertIn("internal_quotient_peak", rep)
        self.assertEqual(rep["output_l_nonzeros"],
                         10 + sum(len(c) for c in res.l_pattern))


class TestIncremental(unittest.TestCase):
    def assert_matches_oracle(self, ana):
        rows, parent = oracle_symbolic(ana.n, ana.edges, ana.perm)
        self.assertEqual([set(c) for c in rows], ana.l_pattern())
        self.assertEqual(list(parent), ana.etree())

    def test_initial_build_matches_oracle(self):
        rng = random.Random(42)
        edges = random_graph(rng, 9, 0.4)
        ana = IncrementalSymbolicCholesky(9, edges)
        self.assert_matches_oracle(ana)

    def test_delete_edge_fill_still_supported(self):
        # 四边形 + 对角线 (1,3)，恒等置换。
        # 位置 (3,1) 由原始边 (1,3) 与第 0 列填充共同支撑。
        edges = {(0, 1), (1, 2), (2, 3), (0, 3), (1, 3)}
        ana = IncrementalSymbolicCholesky(4, edges)
        self.assertIn(3, ana.l_pattern()[1])
        ana.update(delete=[(1, 3)])
        # 原始边删除后，(3,1) 仍由第 0 列填充路径支撑
        self.assertIn(3, ana.l_pattern()[1])
        self.assert_matches_oracle(ana)
        # 再删 (0,3)：填充支撑失效，(3,1) 级联删除；但 (3,2) 由边 (2,3) 保留
        ana.update(delete=[(0, 3)])
        self.assertNotIn(3, ana.l_pattern()[1])
        self.assertIn(3, ana.l_pattern()[2])
        self.assert_matches_oracle(ana)

    def test_random_batches_match_oracle(self):
        for seed in range(20):
            rng = random.Random(5000 + seed)
            n = rng.randint(4, 12)
            edges = random_graph(rng, n, 0.35)
            perm = list(range(n))
            rng.shuffle(perm)
            ana = IncrementalSymbolicCholesky(n, edges, perm)
            self.assert_matches_oracle(ana)
            for _ in range(8):
                adds, dels = [], []
                for _ in range(rng.randint(1, 4)):
                    u, v = rng.sample(range(n), 2)
                    e = (min(u, v), max(u, v))
                    if e in ana.edges and rng.random() < 0.6:
                        dels.append(e)
                    elif e not in ana.edges:
                        adds.append(e)
                if not adds and not dels:
                    continue
                ana.update(add=adds, delete=dels)
                self.assert_matches_oracle(ana)

    def test_atomic_rollback(self):
        edges = {(0, 1), (1, 2), (2, 3)}
        ana = IncrementalSymbolicCholesky(4, edges)
        snap = ana.snapshot()
        # 同一事务里既有合法删除又有非法删除 -> 整体回滚
        with self.assertRaises(ValueError):
            ana.update(delete=[(0, 1), (2, 3), (0, 3)])
        self.assertEqual(ana.edges, {(0, 1), (1, 2), (2, 3)})
        # 自环导致失败，同事务的合法新增也不得生效
        with self.assertRaises(ValueError):
            ana.update(add=[(0, 2), (1, 1)])
        self.assertEqual(ana.edges, {(0, 1), (1, 2), (2, 3)})
        # 旧快照保持原模式与消元树
        self.assertEqual(snap.l_pattern(), tuple(frozenset(c) for c in ana.l_pattern()))
        self.assertEqual(snap.etree(), tuple(ana.etree()))
        self.assert_matches_oracle(ana)

    def test_snapshot_is_immutable(self):
        edges = {(0, 1), (1, 2), (2, 3), (0, 3)}
        ana = IncrementalSymbolicCholesky(4, edges)
        snap = ana.snapshot()
        before = (snap.l_pattern(), snap.etree(), snap.edges)
        ana.update(add=[(0, 2)], delete=[(1, 2)])
        self.assertEqual((snap.l_pattern(), snap.etree(), snap.edges), before)

    def test_locality_across_components(self):
        # 两个不连通块 {0,1,2} 与 {4,5}，顶点 3 孤立；恒等置换。
        edges = {(0, 1), (1, 2), (0, 2), (4, 5)}
        ana = IncrementalSymbolicCholesky(6, edges)
        rec = ana.update(delete=[(0, 2)])
        # 访问列只能落在被修改分量内部
        self.assertTrue(set(rec["visited_columns"]) <= {0, 1, 2})
        self.assertNotIn(4, rec["visited_columns"])
        self.assertNotIn(5, rec["visited_columns"])
        rec2 = ana.update(add=[(4, 5)])  # 已存在 -> no-op，零访问
        self.assertEqual(rec2["visited_columns"], [])
        rec3 = ana.update(delete=[(4, 5)])
        self.assertTrue(set(rec3["visited_columns"]) <= {4, 5})
        self.assert_matches_oracle(ana)

    def test_worst_case_propagates_to_root(self):
        # 路径图 + 恒等置换：消元树是链。加边 (0, n-1) 使填充沿树传到根。
        n = 9
        edges = {(i, i + 1) for i in range(n - 1)}
        ana = IncrementalSymbolicCholesky(n, edges)
        self.assertEqual(ana.etree(), [1, 2, 3, 4, 5, 6, 7, 8, None])
        rec = ana.update(add=[(0, n - 1)])
        # 列 n-2 原本就含行 n-1（路径边），新填充波及列 0..n-3，沿树直到根
        self.assertEqual(rec["visited_columns"], list(range(n - 2)))
        # 填充级联后第 0..n-2 列都获得行 n-1
        for c in range(n - 1):
            self.assertIn(n - 1, ana.l_pattern()[c])
        self.assert_matches_oracle(ana)

    def test_fixed_permutation_never_changes_on_update(self):
        edges = {(0, 1), (1, 2), (2, 3)}
        ana = IncrementalSymbolicCholesky(4, edges, perm=[2, 0, 3, 1])
        ana.update(add=[(0, 3)], delete=[(1, 2)])
        self.assertEqual(ana.perm, [2, 0, 3, 1])
        # 显式重排序才允许改变置换
        res = ana.reorder()
        self.assertEqual(ana.perm, res.perm)
        self.assert_matches_oracle(ana)

    def test_replayable_log(self):
        rng = random.Random(99)
        n = 8
        edges = random_graph(rng, n, 0.4)
        ana = IncrementalSymbolicCholesky(n, edges)
        for _ in range(6):
            adds, dels = [], []
            for _ in range(3):
                u, v = rng.sample(range(n), 2)
                e = (min(u, v), max(u, v))
                (dels if e in ana.edges else adds).append(e)
            ana.update(add=adds, delete=dels)
        ana.reorder()
        ana.update(add=[(0, n - 1)])
        clone = IncrementalSymbolicCholesky.replay(n, ana.log)
        self.assertEqual(clone.l_pattern(), ana.l_pattern())
        self.assertEqual(clone.etree(), ana.etree())
        self.assertEqual(clone.edges, ana.edges)
        self.assertEqual(clone.perm, ana.perm)


if __name__ == "__main__":
    unittest.main(verbosity=2)
