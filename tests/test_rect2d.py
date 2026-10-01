"""二维矩形分区测试。所有数据均为合成数据。"""

import random
import unittest

from sparse_intervals.rect2d import Rule2D, RectEngine
from sparse_intervals.solver import (
    components_lower_bound,
    greedy_partition,
    min_rect_partition,
    verify_grid_partition,
)


class WinnerSelectionTest(unittest.TestCase):
    def test_query_two_time_points(self):
        eng = RectEngine(0, 10, 0, 4)
        eng.add(Rule2D("base", 1, 1, 0, 10, 0, 4, "BASE"))
        eng.add(Rule2D("hot", 2, 1, 2, 6, 1, 3, "HOT"))
        eng.add(Rule2D("hotter", 2, 2, 3, 5, 1, 3, "HOTTER"))
        # 业务时间 4、系统版本时间 2：hot 与 hotter 同优先级，版本高者胜
        self.assertEqual(eng.value_at(4, 2), "HOTTER")
        # 业务时间 2、系统版本时间 0：hot 的版本区间不覆盖 v=0
        self.assertEqual(eng.value_at(2, 0), "BASE")
        # 业务时间 8：只有 base
        self.assertEqual(eng.value_at(8, 2), "BASE")
        # 半开边界：t=6 不再被 hot 覆盖
        self.assertEqual(eng.value_at(6, 2), "BASE")

    def test_id_tiebreak(self):
        eng = RectEngine(0, 4, 0, 4)
        eng.add(Rule2D("b", 1, 1, 0, 4, 0, 4, "B"))
        eng.add(Rule2D("a", 1, 1, 0, 4, 0, 4, "A"))
        self.assertEqual(eng.value_at(2, 2), "A")


class PartitionTest(unittest.TestCase):
    def test_partition_covers_query_rect(self):
        eng = RectEngine(0, 10, 0, 4)
        eng.add(Rule2D("r1", 2, 1, 0, 6, 0, 3, "A"))
        eng.add(Rule2D("r2", 1, 1, 3, 10, 1, 4, "B"))
        rects = eng.partition()
        self.assertEqual(eng.verify(rects), [])
        # 不重叠：验证器已检查每格恰好覆盖一次
        # 同值相邻矩形已合并（贪心）：r1 的 A 区域应为单个矩形
        a_rects = [r for r in rects if r["value"] == "A"]
        self.assertEqual(len(a_rects), 1)
        self.assertEqual(a_rects[0], {"t0": 0, "v0": 0, "t1": 6, "v1": 3,
                                      "value": "A"})

    def test_verifier_catches_tampering(self):
        eng = RectEngine(0, 4, 0, 4)
        eng.add(Rule2D("r", 1, 1, 0, 4, 0, 4, "A"))
        bad = [{"t0": 0, "v0": 0, "t1": 4, "v1": 4, "value": "WRONG"}]
        errors = eng.verify(bad)
        self.assertTrue(errors)
        bad2 = [{"t0": 0, "v0": 0, "t1": 2, "v1": 4, "value": "A"}]
        self.assertTrue(eng.verify(bad2))  # 覆盖不全

    def test_random_partitions_verify(self):
        rng = random.Random(7)
        for _ in range(30):
            eng = RectEngine(0, 12, 0, 6)
            n = rng.randint(0, 6)
            for k in range(n):
                t0 = rng.randint(0, 10)
                t1 = t0 + rng.randint(1, 6)
                v0 = rng.randint(0, 4)
                v1 = v0 + rng.randint(1, 4)
                eng.add(Rule2D(f"r{k}", rng.randint(0, 3), rng.randint(0, 2),
                               t0, t1, v0, v1, rng.choice("ABC")))
            self.assertEqual(eng.verify(), [])


class ExactSolverTest(unittest.TestCase):
    def test_known_cases(self):
        # 全同值 -> 1
        rects, status, _ = min_rect_partition([["A"] * 3 for _ in range(3)])
        self.assertEqual((len(rects), status), (1, "optimal"))
        # 2x2 棋盘 -> 4
        grid = [["A", "B"], ["B", "A"]]
        rects, status, _ = min_rect_partition(grid)
        self.assertEqual((len(rects), status), (4, "optimal"))
        self.assertEqual(verify_grid_partition(grid, rects), [])
        # 3x3 环（中心异色）-> 5
        grid = [["A", "A", "A"],
                ["A", "B", "A"],
                ["A", "A", "A"]]
        rects, status, _ = min_rect_partition(grid)
        self.assertEqual((len(rects), status), (5, "optimal"))
        self.assertEqual(verify_grid_partition(grid, rects), [])

    def test_greedy_not_always_optimal(self):
        # 该合成网格上：贪心条带合并 = 7，精确最小 = 6。
        # 因此贪心结果不得宣称全局最少。
        grid = [["B", "B", "A", "B"],
                ["B", "B", "B", "B"],
                ["B", "A", "A", "B"]]
        g = greedy_partition(grid)
        e, status, _ = min_rect_partition(grid)
        self.assertEqual(status, "optimal")
        self.assertEqual(len(g), 7)
        self.assertEqual(len(e), 6)
        self.assertEqual(verify_grid_partition(grid, e), [])

    def test_exact_never_worse_than_greedy(self):
        rng = random.Random(99)
        for _ in range(40):
            W, H = rng.randint(1, 5), rng.randint(1, 4)
            grid = [[rng.choice("AB") for _ in range(W)] for _ in range(H)]
            g = len(greedy_partition(grid))
            e, status, _ = min_rect_partition(grid, budget=100_000)
            self.assertEqual(status, "optimal")
            self.assertLessEqual(len(e), g)
            self.assertGreaterEqual(len(e), components_lower_bound(grid))
            self.assertEqual(verify_grid_partition(grid, e), [])

    def test_budget_interruption(self):
        # 含大矩形的网格：下界无法立即剪枝，预算为 1 必然中断。
        grid = [["A"] * 6 for _ in range(6)]
        grid[2][2] = "B"
        grid[4][4] = "B"
        rects, status, nodes = min_rect_partition(grid, budget=1)
        self.assertEqual(status, "budget_exceeded")
        self.assertLessEqual(nodes, 2)  # 预算硬界
        # 中断时仍返回一个合法分区（上界），但不保证最少
        self.assertEqual(verify_grid_partition(grid, rects), [])
        # 8x8 棋盘：每个单元都是孤立分量，下界即上界，可证最优 64。
        grid = [[("A" if (x + y) % 2 == 0 else "B")
                 for x in range(8)] for y in range(8)]
        rects, status, _ = min_rect_partition(grid, budget=1000)
        self.assertEqual(status, "optimal")
        self.assertEqual(len(rects), 64)


class RevokeTest(unittest.TestCase):
    def test_revoke_only_invalidates_related_region(self):
        eng = RectEngine(0, 10, 0, 4)
        eng.add(Rule2D("keep", 1, 1, 0, 10, 0, 4, "K"))
        eng.add(Rule2D("drop", 2, 1, 2, 5, 1, 3, "D"))
        res = eng.revoke("drop", verify=True)
        # 受影响区域必须落在被撤销规则的矩形内
        for a in res["affected"]:
            self.assertGreaterEqual(a["t0"], 2)
            self.assertLessEqual(a["t1"], 5)
            self.assertGreaterEqual(a["v0"], 1)
            self.assertLessEqual(a["v1"], 3)
            self.assertEqual(a["old_value"], "D")
            self.assertEqual(a["new_value"], "K")
        self.assertTrue(res["affected"])
        # 全量枚举对照：每个单元与端点零失配
        self.assertEqual(res["verification"]["mismatches"], [])
        self.assertEqual(eng.verify(), [])

    def test_revoke_random_cross_check(self):
        rng = random.Random(31337)
        for _ in range(25):
            eng = RectEngine(0, 10, 0, 5)
            rules = []
            for k in range(rng.randint(1, 6)):
                t0 = rng.randint(0, 8)
                t1 = t0 + rng.randint(1, 5)
                v0 = rng.randint(0, 4)
                v1 = v0 + rng.randint(1, 3)
                rules.append(Rule2D(f"r{k}", rng.randint(0, 3),
                                    rng.randint(0, 2), t0, t1, v0, v1,
                                    rng.choice("ABC")))
            for r in rules:
                eng.add(r)
            rng.shuffle(rules)
            for r in rules:
                res = eng.revoke(r.rid, verify=True)
                v = res["verification"]
                self.assertEqual(v["mismatches"], [],
                                 f"mismatch after revoking {r.rid}")
                self.assertGreater(v["checked_cells"], 0)
                self.assertGreater(v["checked_points"], 0)
                # 受影响区域不得越出被撤销规则（裁剪到查询矩形）
                qt0, qt1, qv0, qv1 = eng.query
                rt0, rt1 = max(r.t0, qt0), min(r.t1, qt1)
                rv0, rv1 = max(r.v0, qv0), min(r.v1, qv1)
                for a in res["affected"]:
                    self.assertGreaterEqual(a["t0"], rt0)
                    self.assertLessEqual(a["t1"], rt1)
                    self.assertGreaterEqual(a["v0"], rv0)
                    self.assertLessEqual(a["v1"], rv1)
            self.assertEqual(eng.verify(), [])

    def test_revoke_unknown_id(self):
        eng = RectEngine(0, 4, 0, 4)
        with self.assertRaises(KeyError):
            eng.revoke("nope")


class ExactPartitionEngineTest(unittest.TestCase):
    def test_engine_exact_partition(self):
        eng = RectEngine(0, 4, 0, 3)
        # 构造精确求解器能找到更优解的场景（对应 test_greedy_not_always_optimal）
        eng.add(Rule2D("base", 1, 1, 0, 4, 0, 3, "B"))
        eng.add(Rule2D("p1", 2, 1, 2, 3, 0, 1, "A"))
        eng.add(Rule2D("p2", 2, 1, 1, 3, 2, 3, "A"))
        exact = eng.exact_partition()
        self.assertEqual(exact["status"], "optimal")
        self.assertEqual(exact["rect_count"], 6)
        self.assertEqual(len(eng.partition()), 7)  # 贪心更差
        self.assertEqual(eng.verify(exact["rects"]), [])
        self.assertLessEqual(exact["lower_bound_components"],
                             exact["rect_count"])


if __name__ == "__main__":
    unittest.main()
