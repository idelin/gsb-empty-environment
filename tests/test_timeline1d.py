"""一维规范时间线测试。所有数据均为合成数据。"""

import random
import unittest

from sparse_intervals.timeline1d import Rule, Timeline


def seg_tuples(tl):
    return [(s.start, s.end, s.value) for s in tl.segments()]


class AcceptanceTest(unittest.TestCase):
    """验收边界：同值相邻段合并；撤销后不得保留缓存前半段。"""

    def test_merge_then_revoke(self):
        tl = Timeline([
            Rule("a", 1, 1, 0, 5, "X"),
            Rule("b", 1, 1, 5, 10, "X"),
        ])
        # 同值配置 a[0,5) b[5,10) 合并为一段
        self.assertEqual(seg_tuples(tl), [(0, 10, "X")])
        # 撤销 a 后仅剩 [5,10)，不能保留缓存前半段
        affected = tl.revoke("a")
        self.assertEqual(seg_tuples(tl), [(5, 10, "X")])
        self.assertEqual(
            affected,
            [{"start": 0, "end": 5, "old_value": "X", "new_value": None}],
        )

    def test_revoke_middle_reveals_and_merges(self):
        tl = Timeline([
            Rule("a", 1, 1, 0, 5, "X"),
            Rule("b", 2, 1, 2, 6, "Y"),
            Rule("c", 1, 1, 5, 10, "X"),
        ])
        self.assertEqual(
            seg_tuples(tl), [(0, 2, "X"), (2, 6, "Y"), (6, 10, "X")])
        affected = tl.revoke("b")
        # b 撤销后 [2,6) 显现为 X，并与两侧同值段合并
        self.assertEqual(seg_tuples(tl), [(0, 10, "X")])
        self.assertEqual(
            affected,
            [{"start": 2, "end": 6, "old_value": "Y", "new_value": "X"}],
        )


class RankingTest(unittest.TestCase):
    def test_priority_beats_version_beats_id(self):
        tl = Timeline([
            Rule("low", 1, 9, 0, 10, "L"),
            Rule("high", 2, 1, 0, 10, "H"),
        ])
        self.assertEqual(seg_tuples(tl), [(0, 10, "H")])

        tl = Timeline([
            Rule("v1", 1, 1, 0, 10, "V1"),
            Rule("v2", 1, 2, 0, 10, "V2"),
        ])
        self.assertEqual(seg_tuples(tl), [(0, 10, "V2")])

        tl = Timeline([
            Rule("b", 1, 1, 0, 10, "B"),
            Rule("a", 1, 1, 0, 10, "A"),
        ])
        self.assertEqual(seg_tuples(tl), [(0, 10, "A")])

    def test_half_open_boundary(self):
        tl = Timeline([
            Rule("a", 1, 1, 0, 5, "A"),
            Rule("b", 1, 1, 5, 10, "B"),
        ])
        segs = tl.segments()
        self.assertEqual(seg_tuples(tl), [(0, 5, "A"), (5, 10, "B")])
        # 半开：t=5 属于 b
        self.assertEqual(segs[1].start, 5)


class ExplainTest(unittest.TestCase):
    def test_shadowed_rules(self):
        tl = Timeline([
            Rule("win", 2, 1, 0, 10, "W"),
            Rule("lose1", 1, 1, 0, 10, "L"),
            Rule("lose2", 1, 2, 2, 8, "L"),
        ])
        segs = tl.segments()
        self.assertEqual(len(segs), 1)
        self.assertEqual(segs[0].winners, ("win",))
        self.assertEqual(set(segs[0].shadowed), {"lose1", "lose2"})

    def test_merged_segment_winners_union(self):
        tl = Timeline([
            Rule("a", 1, 1, 0, 5, "X"),
            Rule("b", 1, 1, 5, 10, "X"),
            Rule("c", 0, 1, 0, 10, "Y"),
        ])
        segs = tl.segments()
        self.assertEqual(len(segs), 1)
        self.assertEqual(set(segs[0].winners), {"a", "b"})
        self.assertEqual(segs[0].shadowed, ("c",))


class IncrementalTest(unittest.TestCase):
    """增量撤销必须与全量重建一致，且受影响区间恰好覆盖变化点。"""

    def test_revoke_matches_full_rebuild(self):
        rng = random.Random(20261001)
        for _ in range(80):
            rules = []
            for k in range(rng.randint(1, 8)):
                s = rng.randint(0, 20)
                e = s + rng.randint(1, 8)
                rules.append(Rule(
                    f"r{k}", rng.randint(0, 3), rng.randint(0, 3),
                    s, e, rng.choice("ABC")))
            tl = Timeline(rules)
            remaining = list(rules)
            order = [r.rid for r in rules]
            rng.shuffle(order)
            for rid in order:
                before = seg_tuples(tl)
                affected = tl.revoke(rid)
                remaining = [r for r in remaining if r.rid != rid]
                fresh = Timeline(remaining)
                after = seg_tuples(tl)
                # 1) 增量结果 == 全量重建
                self.assertEqual(after, seg_tuples(fresh))
                # 2) 受影响区间之外不得有变化；之内变化点必须被覆盖
                for x2 in range(-1, 60):
                    pos = x2 / 2 + 0.25  # 避开整数/半数边界
                    old_v = _value_of(before, pos)
                    new_v = _value_of(after, pos)
                    in_affected = any(
                        a["start"] <= pos < a["end"] for a in affected)
                    if old_v != new_v:
                        self.assertTrue(
                            in_affected,
                            f"changed at {pos} but not reported affected")
                    else:
                        self.assertFalse(
                            in_affected,
                            f"unchanged at {pos} but reported affected")
                    if in_affected:
                        rec = next(a for a in affected
                                   if a["start"] <= pos < a["end"])
                        self.assertEqual(rec["old_value"], old_v)
                        self.assertEqual(rec["new_value"], new_v)

    def test_revoke_unknown_id(self):
        tl = Timeline([Rule("a", 1, 1, 0, 5, "X")])
        with self.assertRaises(KeyError):
            tl.revoke("nope")


def _value_of(segs, pos):
    for s, e, v in segs:
        if s <= pos < e:
            return v
    return None


if __name__ == "__main__":
    unittest.main()
