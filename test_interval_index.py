"""interval_index 的 unittest 测试套件。"""

import random
import unittest

from interval_index import (
    Interval,
    IntervalIndex,
    parse_interval,
    tokenize,
)


def spec(lo, lo_closed, hi, hi_closed):
    iv = Interval.from_spec(lo, lo_closed, hi, hi_closed)
    assert iv is not None, "测试辅助函数不应构造空区间"
    return iv


class TestParseAndNormalize(unittest.TestCase):
    def test_closed_open_mixed(self):
        self.assertEqual(parse_interval("range=[0,5)"), Interval(0, 4))
        self.assertEqual(parse_interval("range=(0,5]"), Interval(1, 5))
        self.assertEqual(parse_interval("range=[0,5]"), Interval(0, 5))
        self.assertEqual(parse_interval("range=(0,5)"), Interval(1, 4))

    def test_missing_endpoints(self):
        self.assertEqual(parse_interval("range=[3,)"), Interval(3, None))
        self.assertEqual(parse_interval("range=(,8]"), Interval(None, 8))
        self.assertEqual(parse_interval("range=(,)"), Interval(None, None))

    def test_negative_and_spaces(self):
        self.assertEqual(parse_interval("range = [ -3 , 2 )"), Interval(-3, 1))

    def test_point_interval(self):
        self.assertEqual(parse_interval("range=[5,5]"), Interval(5, 5))

    def test_empty_interval_is_missing(self):
        self.assertIsNone(parse_interval("range=(3,3)"))
        self.assertIsNone(parse_interval("range=[3,3)"))
        self.assertIsNone(parse_interval("range=(2,3)"))  # 相邻整数间无整数

    def test_missing_attribute(self):
        self.assertIsNone(parse_interval("没有任何区间属性"))

    def test_first_spec_wins(self):
        self.assertEqual(parse_interval("range=[1,2] range=[9,9]"), Interval(1, 2))

    def test_empty_query_interval_rejected(self):
        with self.assertRaises(ValueError):
            Interval(5, 4)


class TestTokenize(unittest.TestCase):
    def test_rules(self):
        toks = tokenize("Doc-7 苹果,Apple range=[0,5) APPLE2")
        self.assertIn("苹果", toks)
        self.assertIn("apple", toks)      # casefold 小写化
        self.assertIn("apple2", toks)
        self.assertIn("doc", toks)        # 非词字符切分
        self.assertIn("7", toks)
        self.assertNotIn("range", toks)   # range= 规范被剔除
        self.assertNotIn("0", toks)       # 区间数字不进入词元？0 在规范内被剔除


class TestIntervalRelations(unittest.TestCase):
    def test_half_open_adjacent_not_intersect(self):
        a = spec(0, True, 5, False)   # [0,5)
        b = spec(5, True, 8, False)   # [5,8)
        self.assertFalse(a.intersects(b))
        self.assertFalse(b.intersects(a))

    def test_closed_touching_intersect(self):
        a = spec(0, True, 5, True)    # [0,5]
        b = spec(5, True, 8, False)   # [5,8)
        self.assertTrue(a.intersects(b))

    def test_point_interval(self):
        p = spec(5, True, 5, True)    # 点区间 [5,5]
        self.assertTrue(p.intersects(spec(5, True, 5, True)))
        self.assertTrue(p.intersects(spec(0, True, 10, True)))
        self.assertFalse(p.intersects(spec(5, False, 8, False)))  # (5,8) 不含 5
        self.assertFalse(p.intersects(spec(0, True, 5, False)))   # [0,5) 不含 5
        self.assertTrue(p.contained_by(spec(5, True, 5, True)))
        self.assertTrue(p.contains(spec(5, True, 5, True)))

    def test_contains_and_contained_by(self):
        big = spec(0, True, 10, True)
        small = spec(2, True, 4, True)
        self.assertTrue(big.contains(small))
        self.assertTrue(small.contained_by(big))
        self.assertFalse(small.contains(big))
        self.assertTrue(big.contains(big))  # 自包含

    def test_infinite_bounds(self):
        allv = Interval(None, None)
        self.assertTrue(allv.contains(spec(0, True, 5, True)))
        self.assertTrue(allv.intersects(Interval(None, None)))
        self.assertFalse(spec(0, True, 5, True).contains(Interval(None, 5)))
        self.assertTrue(Interval(None, 5).contains(Interval(None, 3)))


class TestIndexBasics(unittest.TestCase):
    def test_intersect_query(self):
        idx = IntervalIndex()
        idx.upsert("a", "甲 range=[0,5)")
        idx.upsert("b", "乙 range=[5,8)")
        idx.upsert("c", "丙 range=[3,6]")
        idx.upsert("d", "丁 无区间属性")
        # [0,5) 与 [5,8) 不相交
        self.assertEqual(idx.intersect(spec(0, True, 5, False)), ["a", "c"])
        self.assertEqual(idx.intersect(spec(5, True, 8, False)), ["b", "c"])
        # 缺失属性的文档永不命中
        self.assertNotIn("d", idx.intersect(Interval(None, None)))

    def test_contains_and_contained_by_query(self):
        idx = IntervalIndex()
        idx.upsert("big", "range=[0,10]")
        idx.upsert("mid", "range=[2,6]")
        idx.upsert("pt", "range=[3,3]")
        idx.upsert("neg", "range=(,0]")   # (-inf, 0]
        q = spec(2, True, 4, True)         # [2,4]
        self.assertEqual(idx.contains(q), ["big", "mid"])
        self.assertEqual(idx.contained_by(q), ["pt"])
        self.assertEqual(idx.contained_by(Interval(None, None)),
                         ["big", "mid", "neg", "pt"])

    def test_update_removes_old_entries(self):
        idx = IntervalIndex()
        idx.upsert("x", "range=[0,5)")
        self.assertEqual(idx.intersect(spec(0, True, 5, False)), ["x"])
        idx.upsert("x", "range=[100,200]")   # 更新：旧项必须删除
        self.assertEqual(idx.intersect(spec(0, True, 5, False)), [])
        self.assertEqual(idx.intersect(spec(150, True, 160, True)), ["x"])
        idx.upsert("x", "没有区间了")          # 更新为缺失属性
        self.assertEqual(idx.intersect(Interval(None, None)), [])
        idx.check_invariants()

    def test_delete(self):
        idx = IntervalIndex()
        idx.upsert("x", "range=[0,5)")
        idx.upsert("y", "range=[1,2]")
        idx.delete("x")
        idx.delete("x")  # 重复删除静默
        idx.delete("zzz")
        self.assertEqual(idx.intersect(Interval(None, None)), ["y"])
        idx.check_invariants()

    def test_no_duplicate_ids(self):
        idx = IntervalIndex()
        for i in range(50):
            idx.upsert(f"doc{i}", f"range=[{i % 7},{i % 7 + 3}]")
        res = idx.intersect(spec(2, True, 4, True))
        self.assertEqual(len(res), len(set(res)))

    def test_token_postings(self):
        idx = IntervalIndex()
        idx.upsert("a", "苹果 Apple range=[0,1]")
        idx.upsert("b", "苹果 banana")
        self.assertEqual(idx.docs_with_token("苹果"), ["a", "b"])
        self.assertEqual(idx.docs_with_token("APPLE"), ["a"])
        idx.delete("a")
        self.assertEqual(idx.docs_with_token("苹果"), ["b"])


class TestIndexVsFullScan(unittest.TestCase):
    """随机化一致性：索引结果必须与全扫描一致且无重复。"""

    def random_spec(self, rng):
        lo = rng.choice([None, rng.randint(-10, 10)])
        hi = rng.choice([None, rng.randint(-10, 10)])
        return (lo, rng.random() < 0.5, hi, rng.random() < 0.5)

    def test_randomized(self):
        rng = random.Random(20260926)
        idx = IntervalIndex()
        live = []
        # 随机 upsert/delete 200 步
        for step in range(200):
            doc = f"d{rng.randint(0, 39)}"
            if doc in live and rng.random() < 0.25:
                idx.delete(doc)
                live.remove(doc)
                continue
            lo, lc, hi, hc = self.random_spec(rng)
            text = f"合成 文档 range={'[' if lc else '('}{'' if lo is None else lo},{'' if hi is None else hi}{']' if hc else ')'}"
            idx.upsert(doc, text)
            if doc not in live:
                live.append(doc)
        idx.check_invariants()
        # 随机查询 300 次，三种语义都与全扫描一致
        for _ in range(300):
            while True:
                lo, lc, hi, hc = self.random_spec(rng)
                q = Interval.from_spec(lo, lc, hi, hc)
                if q is not None:
                    break
            for query_fn, scan_fn in (
                (idx.intersect, idx.full_scan_intersect),
                (idx.contains, idx.full_scan_contains),
                (idx.contained_by, idx.full_scan_contained_by),
            ):
                got = query_fn(q)
                want = scan_fn(q)
                self.assertEqual(got, want, f"q={q}")
                self.assertEqual(len(got), len(set(got)), "结果含重复 ID")


if __name__ == "__main__":
    unittest.main(verbosity=2)
