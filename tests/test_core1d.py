import unittest

from sparse_intervals import Config, Segment, Timeline


def cfg(id, start, end, value, key="k", priority=0, version=0):
    return Config(id=id, key=key, priority=priority, version=version,
                  start=start, end=end, value=value)


class TestAcceptance(unittest.TestCase):
    """验收边界：同值相邻合并；撤销后不得保留缓存前半段。"""

    def test_same_value_adjacent_merge_then_revoke(self):
        tl = Timeline()
        tl.add(cfg("a", 0, 5, "x"))
        tl.add(cfg("b", 5, 10, "x"))
        segs = tl.segments("k")
        self.assertEqual(segs, [Segment(0, 10, "x")], "同值相邻应合并为一段")

        affected = tl.revoke("a")
        segs = tl.segments("k")
        self.assertEqual(segs, [Segment(5, 10, "x")], "撤销 a 后仅剩 [5,10)")
        self.assertEqual(len(affected), 1)
        a = affected[0]
        self.assertEqual((a.start, a.end), (0, 5))
        self.assertEqual(a.old_value, "x")
        self.assertIsNone(a.new_value)
        # 缓存中不得残留 [0,5)
        self.assertIsNone(tl.value_at("k", 2))
        self.assertEqual(tl.value_at("k", 7), "x")

    def test_half_open_boundary(self):
        tl = Timeline()
        tl.add(cfg("a", 0, 5, "A"))
        tl.add(cfg("b", 5, 10, "B"))
        self.assertEqual(tl.value_at("k", 5), "B")  # 5 属于 [5,10)
        self.assertEqual(tl.value_at("k", 4.999), "A")
        self.assertIsNone(tl.value_at("k", 10))


class TestWinnerOrder(unittest.TestCase):
    def test_priority_beats_version_beats_id(self):
        tl = Timeline()
        tl.add(cfg("low", 0, 10, "L", priority=1, version=9))
        tl.add(cfg("high", 0, 10, "H", priority=2, version=1))
        self.assertEqual(tl.value_at("k", 5), "H")

        tl2 = Timeline()
        tl2.add(cfg("v1", 0, 10, "V1", priority=1, version=1))
        tl2.add(cfg("v2", 0, 10, "V2", priority=1, version=2))
        self.assertEqual(tl2.value_at("k", 5), "V2")

        tl3 = Timeline()
        tl3.add(cfg("b", 0, 10, "B", priority=1, version=1))
        tl3.add(cfg("a", 0, 10, "A", priority=1, version=1))
        self.assertEqual(tl3.value_at("k", 5), "A")  # id 小者胜

    def test_partial_overlap_segments(self):
        tl = Timeline()
        tl.add(cfg("base", 0, 10, "base", priority=1))
        tl.add(cfg("over", 3, 6, "over", priority=2))
        self.assertEqual(
            tl.segments("k"),
            [Segment(0, 3, "base"), Segment(3, 6, "over"), Segment(6, 10, "base")],
        )

    def test_gap_not_merged(self):
        tl = Timeline()
        tl.add(cfg("a", 0, 2, "x"))
        tl.add(cfg("b", 4, 6, "x"))
        self.assertEqual(tl.segments("k"), [Segment(0, 2, "x"), Segment(4, 6, "x")])


class TestIncremental(unittest.TestCase):
    def test_revoke_reveals_shadowed(self):
        tl = Timeline()
        tl.add(cfg("top", 2, 8, "T", priority=2))
        tl.add(cfg("bot", 0, 10, "B", priority=1))
        self.assertEqual(tl.value_at("k", 5), "T")
        affected = tl.revoke("top")
        self.assertEqual(tl.segments("k"), [Segment(0, 10, "B")])
        self.assertEqual(len(affected), 1)
        self.assertEqual((affected[0].start, affected[0].end), (2, 8))
        self.assertEqual((affected[0].old_value, affected[0].new_value), ("T", "B"))

    def test_revoke_unaffected_region_kept(self):
        tl = Timeline()
        tl.add(cfg("a", 0, 5, "A"))
        tl.add(cfg("b", 10, 15, "B"))
        affected = tl.revoke("a")
        self.assertEqual(tl.segments("k"), [Segment(10, 15, "B")])
        self.assertEqual(len(affected), 1)
        self.assertEqual((affected[0].start, affected[0].end), (0, 5))

    def test_revoke_unknown_raises(self):
        tl = Timeline()
        with self.assertRaises(KeyError):
            tl.revoke("nope")

    def test_add_reports_affected(self):
        tl = Timeline()
        tl.add(cfg("a", 0, 10, "A"))
        affected = tl.add(cfg("b", 3, 7, "B", priority=1))
        self.assertEqual(len(affected), 1)
        self.assertEqual((affected[0].start, affected[0].end), (3, 7))
        self.assertEqual((affected[0].old_value, affected[0].new_value), ("A", "B"))

    def test_revoke_merges_across_boundary(self):
        # 撤销中间段后，两侧同值段应合并
        tl = Timeline()
        tl.add(cfg("l", 0, 5, "x"))
        tl.add(cfg("m", 5, 8, "y"))
        tl.add(cfg("r", 8, 10, "x"))
        tl.revoke("m")
        self.assertEqual(tl.segments("k"), [Segment(0, 5, "x"), Segment(8, 10, "x")])
        # 再添加同值中段，应整体合并
        tl.add(cfg("m2", 5, 8, "x"))
        self.assertEqual(tl.segments("k"), [Segment(0, 10, "x")])


class TestExplain(unittest.TestCase):
    def test_shadowed_list(self):
        tl = Timeline()
        tl.add(cfg("win", 0, 10, "W", priority=2))
        tl.add(cfg("lose1", 2, 6, "L1", priority=1))
        tl.add(cfg("lose2", 3, 5, "L2", priority=0))
        ex = tl.explain("k")
        # [0,2): 无遮蔽; [2,3): lose1; [3,5): lose1,lose2; [5,6): lose1; [6,10): 无
        self.assertEqual(len(ex), 5)
        self.assertEqual(ex[0].shadowed, ())
        self.assertEqual(ex[1].shadowed, ("lose1",))
        self.assertEqual(ex[2].shadowed, ("lose1", "lose2"))
        self.assertEqual(ex[3].shadowed, ("lose1",))
        self.assertEqual(ex[4].shadowed, ())
        for e in ex:
            self.assertEqual(e.winner, "win")
            self.assertEqual(e.value, "W")

    def test_explain_after_revoke(self):
        tl = Timeline()
        tl.add(cfg("top", 0, 10, "T", priority=2))
        tl.add(cfg("bot", 0, 10, "B", priority=1))
        tl.revoke("top")
        ex = tl.explain("k")
        self.assertEqual(len(ex), 1)
        self.assertEqual(ex[0].winner, "bot")
        self.assertEqual(ex[0].shadowed, ())


class TestMultiKey(unittest.TestCase):
    def test_keys_isolated(self):
        tl = Timeline()
        tl.add(cfg("a", 0, 5, "A", key="k1"))
        tl.add(cfg("b", 0, 5, "B", key="k2"))
        self.assertEqual(tl.segments("k1"), [Segment(0, 5, "A")])
        self.assertEqual(tl.segments("k2"), [Segment(0, 5, "B")])
        tl.revoke("a")
        self.assertEqual(tl.segments("k1"), [])
        self.assertEqual(tl.segments("k2"), [Segment(0, 5, "B")])


class TestBruteForceCrossCheck(unittest.TestCase):
    """随机场景：增量维护的段与暴力点查询逐点对照。"""

    def test_random_revoke_matches_bruteforce(self):
        import random

        rng = random.Random(20261001)
        for trial in range(30):
            tl = Timeline()
            ids = []
            for n in range(rng.randint(1, 8)):
                s = rng.randint(0, 15)
                e = s + rng.randint(1, 8)
                cid = f"c{n}"
                ids.append(cid)
                tl.add(cfg(cid, s, e, rng.choice("xyz"),
                           priority=rng.randint(0, 3), version=rng.randint(0, 3)))
            rng.shuffle(ids)
            for cid in ids[: rng.randint(0, len(ids))]:
                tl.revoke(cid)
            # 段与暴力点查询一致
            for t10 in range(0, 240):
                t = t10 / 10
                want = tl.value_at("k", t)
                got = None
                for seg in tl.segments("k"):
                    if seg.start <= t < seg.end:
                        got = seg.value
                        break
                self.assertEqual(got, want, f"trial={trial} t={t}")
            # 段内无相邻同值（最少段数性质）
            segs = tl.segments("k")
            for s1, s2 in zip(segs, segs[1:]):
                self.assertFalse(s1.end == s2.start and s1.value == s2.value)


if __name__ == "__main__":
    unittest.main()
