import random
import unittest

from sparse_intervals import (
    Rect,
    RectConfig,
    RectTimeline,
    build_grid,
    exact_min_partition,
    greedy_partition,
    point_winner,
    rects_from_cells,
    verify_partition,
)
from sparse_intervals.rect2d import greedy_cell_partition


def rcfg(id, t0, t1, v0, v1, value, priority=0, version=0):
    return RectConfig(id=id, priority=priority, version=version,
                      t0=t0, t1=t1, v0=v0, v1=v1, value=value)


class TestBasicPartition(unittest.TestCase):
    def test_single_config_partition_and_verify(self):
        query = (0, 10, 0, 10)
        rt = RectTimeline(query)
        rt.add(rcfg("a", 2, 6, 2, 6, "A"))
        rects = rt.partition()
        self.assertEqual(verify_partition(rt.grid, rects), [])
        # 整个查询矩形被覆盖（含 None 区域）
        total = sum((r.x1 - r.x0) * (r.y1 - r.y0) for r in rects)
        self.assertEqual(total, 100)
        self.assertEqual(rt.audit(), [])

    def test_winner_order_2d(self):
        query = (0, 10, 0, 10)
        rt = RectTimeline(query)
        rt.add(rcfg("low", 0, 10, 0, 10, "L", priority=1, version=9))
        rt.add(rcfg("high", 2, 8, 2, 8, "H", priority=2, version=1))
        self.assertEqual(rt.value_at(5, 5), "H")
        self.assertEqual(rt.value_at(1, 1), "L")
        self.assertEqual(rt.audit(), [])

    def test_half_open_2d(self):
        query = (0, 10, 0, 10)
        rt = RectTimeline(query)
        rt.add(rcfg("a", 0, 5, 0, 5, "A"))
        self.assertIsNone(rt.value_at(5, 2))   # 业务时间边界外
        self.assertIsNone(rt.value_at(2, 5))   # 版本时间边界外
        self.assertEqual(rt.value_at(4, 4), "A")


class TestExactSolver(unittest.TestCase):
    def test_known_small_cases(self):
        self.assertEqual(exact_min_partition([["a"]]).count, 1)
        self.assertEqual(
            exact_min_partition([["a", "a"], ["a", "a"]]).count, 1)
        # 2x2 棋盘：任意矩形都不单色，只能逐格
        chk = [["a", "b"], ["b", "a"]]
        res = exact_min_partition(chk)
        self.assertTrue(res.optimal)
        self.assertEqual(res.count, 4)
        # 行带状
        rows = [["a", "a"], ["b", "b"]]
        self.assertEqual(exact_min_partition(rows).count, 2)

    def test_greedy_not_always_optimal(self):
        # 已知实例：局部贪心 5 块，精确最小 4 块
        vals = [
            ["b", "a", "a"],
            ["b", "b", "b"],
            ["a", "b", "b"],
        ]
        self.assertEqual(len(greedy_cell_partition(vals)), 5)
        res = exact_min_partition(vals)
        self.assertTrue(res.optimal)
        self.assertEqual(res.count, 4)

    def test_exact_leq_greedy_random(self):
        rng = random.Random(42)
        for _ in range(40):
            rows, cols = rng.randint(1, 4), rng.randint(1, 4)
            vals = [[rng.choice("abc") for _ in range(cols)] for _ in range(rows)]
            res = exact_min_partition(vals, budget=200_000)
            self.assertTrue(res.optimal)
            g = len(greedy_cell_partition(vals))
            self.assertLessEqual(res.count, g)
            # 精确解矩形同样通过独立验证
            grid = _grid_from_values(vals)
            rects = rects_from_cells(grid, res.cells)
            self.assertEqual(verify_partition(grid, rects), [])

    def test_budget_interruption_bound(self):
        # 大棋盘使精确解在极小预算下必然中断
        vals = [["a" if (r + c) % 2 == 0 else "b" for c in range(8)] for r in range(8)]
        res = exact_min_partition(vals, budget=3)
        self.assertFalse(res.optimal)
        self.assertIsNone(res.count)
        # 中断界：下界 <= 真实最优(=64) <= 上界(贪心)
        self.assertLessEqual(res.lower_bound, 64)
        self.assertGreaterEqual(res.upper_bound, 64)
        self.assertGreater(res.nodes, 0)

    def test_budget_sufficient_then_optimal(self):
        vals = [["a", "b"], ["b", "a"]]
        res = exact_min_partition(vals, budget=10_000)
        self.assertTrue(res.optimal)
        self.assertEqual(res.count, res.lower_bound)
        self.assertEqual(res.count, res.upper_bound)


class TestVerifier(unittest.TestCase):
    def setUp(self):
        self.vals = [["a", "b"], ["b", "a"]]
        self.grid = _grid_from_values(self.vals)

    def test_accepts_valid(self):
        rects = [
            Rect(0, 1, 0, 1, "a"), Rect(1, 2, 0, 1, "b"),
            Rect(0, 1, 1, 2, "b"), Rect(1, 2, 1, 2, "a"),
        ]
        self.assertEqual(verify_partition(self.grid, rects), [])

    def test_rejects_overlap(self):
        rects = [Rect(0, 2, 0, 1, "a"), Rect(0, 2, 0, 2, "a")]
        errors = verify_partition(self.grid, rects)
        self.assertTrue(errors)

    def test_rejects_wrong_value(self):
        rects = [
            Rect(0, 2, 0, 1, "a"),  # 跨 a/b 两格，非单色
            Rect(0, 2, 1, 2, "a"),
        ]
        errors = verify_partition(self.grid, rects)
        self.assertTrue(any("单色" in e for e in errors))

    def test_rejects_hole(self):
        rects = [Rect(0, 1, 0, 1, "a")]
        errors = verify_partition(self.grid, rects)
        self.assertTrue(any("覆盖次数" in e for e in errors))

    def test_rejects_misaligned(self):
        rects = [Rect(0, 1.5, 0, 2, "a")]
        errors = verify_partition(self.grid, rects)
        self.assertTrue(any("网格线" in e for e in errors))


class TestIncrementalRevoke2D(unittest.TestCase):
    def test_revoke_only_invalidates_related_region(self):
        query = (0, 10, 0, 10)
        rt = RectTimeline(query)
        rt.add(rcfg("base", 0, 10, 0, 10, "B", priority=1))
        rt.add(rcfg("top", 2, 5, 2, 5, "T", priority=2))
        affected = rt.revoke("top")
        self.assertEqual(len(affected), 1)
        a = affected[0]
        self.assertEqual((a.x0, a.x1, a.y0, a.y1), (2, 5, 2, 5))
        self.assertEqual((a.old_value, a.new_value), ("T", "B"))
        self.assertEqual(rt.audit(), [])

    def test_revoke_to_uncovered(self):
        query = (0, 10, 0, 10)
        rt = RectTimeline(query)
        rt.add(rcfg("a", 0, 4, 0, 4, "A"))
        affected = rt.revoke("a")
        self.assertEqual(len(affected), 1)
        self.assertEqual(affected[0].old_value, "A")
        self.assertIsNone(affected[0].new_value)
        self.assertEqual(rt.audit(), [])

    def test_random_revoke_crosscheck_full_enumeration(self):
        """随机配置撤销后：逐单元、逐端点与全量网格枚举对照。"""
        rng = random.Random(113)
        for trial in range(25):
            query = (0, 12, 0, 12)
            rt = RectTimeline(query)
            ids = []
            for n in range(rng.randint(1, 7)):
                t0 = rng.randint(0, 10)
                t1 = t0 + rng.randint(1, 6)
                v0 = rng.randint(0, 10)
                v1 = v0 + rng.randint(1, 6)
                cid = f"c{n}"
                ids.append(cid)
                rt.add(rcfg(cid, t0, t1, v0, v1, rng.choice("xyz"),
                            priority=rng.randint(0, 3),
                            version=rng.randint(0, 3)))
            self.assertEqual(rt.audit(), [], f"trial={trial} add 阶段")
            rng.shuffle(ids)
            for cid in ids[: rng.randint(1, len(ids))]:
                rt.revoke(cid)
                self.assertEqual(rt.audit(), [], f"trial={trial} revoke {cid}")
            # 与全新全量枚举的网格逐点对照
            fresh = RectTimeline(query)
            for cid, c in rt._configs.items():
                fresh.add(c)
            g = rt.grid
            for j in range(g.rows):
                for i in range(g.cols):
                    cx = (g.xs[i] + g.xs[i + 1]) / 2
                    cy = (g.ys[j] + g.ys[j + 1]) / 2
                    self.assertEqual(
                        rt.value_at(cx, cy), fresh.value_at(cx, cy),
                        f"trial={trial} cell=({i},{j})")
                    self.assertEqual(
                        rt.value_at(cx, cy),
                        point_winner(list(rt._configs.values()), cx, cy))
            # 分区通过独立验证
            self.assertEqual(
                verify_partition(rt.grid, rt.partition()), [],
                f"trial={trial} 分区验证")


def _grid_from_values(vals):
    rows, cols = len(vals), len(vals[0])
    from sparse_intervals import Grid
    return Grid(list(range(cols + 1)), list(range(rows + 1)),
                [list(r) for r in vals])


if __name__ == "__main__":
    unittest.main()
