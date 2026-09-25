import unittest
from fractions import Fraction

from priority_timeline import ConflictError, Segment, TimePoint, Timeline


def bounds(segs):
    """[(start, end)] 以有理数表示，便于断言。"""
    return [(str(s.start.offset), str(s.end.offset)) for s in segs]


class TestPriorityOverlay(unittest.TestCase):
    def test_high_priority_covers_middle_only(self):
        """高优先 [3,7) 只覆盖低优先 [0,10) 的中段，两端仍是低优先。"""
        tl = Timeline()
        low = tl.add(TimePoint(0), TimePoint(10), "低", priority=0, source="base")
        high = tl.add(TimePoint(3), TimePoint(7), "高", priority=1, source="override")

        self.assertEqual(bounds(tl.segments()), [("0", "3"), ("3", "7"), ("7", "10")])
        self.assertEqual([s.value for s in tl.segments()], ["低", "高", "低"])
        self.assertEqual([s.priority for s in tl.segments()], [0, 1, 0])
        self.assertEqual(tl.segments()[0].sources, ("base",))
        self.assertEqual(tl.segments()[1].sources, ("override",))
        # 每个生效段都能回溯到具体原始条目
        self.assertEqual(tl.segments()[1].entries[0].id, high)
        self.assertEqual(tl.segments()[0].entries[0].id, low)

    def test_withdraw_high_restores_underlying(self):
        """撤回高优先后底层恢复，且首尾重新合并成完整的一段。"""
        tl = Timeline()
        low = tl.add(0, 10, "低", priority=0, source="base")
        high = tl.add(3, 7, "高", priority=1, source="override")
        removed = tl.remove(high)

        self.assertEqual(removed.value, "高")
        self.assertEqual(bounds(tl.segments()), [("0", "10")])
        only = tl.segments()[0]
        self.assertEqual(only.value, "低")
        self.assertEqual(only.sources, ("base",))
        self.assertEqual(only.entries[0].id, low)

    def test_equal_priority_conflict_is_kept_explicitly(self):
        """同级冲突显式保留全部候选，不静默取舍。"""
        tl = Timeline()
        tl.add(0, 10, "A", priority=1, source="src-a")
        tl.add(3, 7, "B", priority=1, source="src-b")

        segs = tl.segments()
        self.assertEqual(bounds(segs), [("0", "3"), ("3", "7"), ("7", "10")])
        conflict = segs[1]
        self.assertTrue(conflict.is_conflict)
        self.assertEqual(conflict.values, ("A", "B"))
        self.assertEqual(conflict.sources, ("src-a", "src-b"))
        self.assertEqual(tl.conflicts(), [conflict])
        with self.assertRaises(ConflictError):
            conflict.value  # noqa: B018

        # 段内仍可精确取到每个候选的证据
        entries = conflict.entries
        self.assertEqual([e.source for e in entries], ["src-a", "src-b"])
        self.assertEqual([e.start.offset for e in entries], [Fraction(0), Fraction(3)])

    def test_withdraw_one_conflict_candidate_resolves_segment(self):
        tl = Timeline()
        a = tl.add(0, 10, "A", priority=1, source="src-a")
        b = tl.add(3, 7, "B", priority=1, source="src-b")
        tl.remove(b)
        self.assertEqual(bounds(tl.segments()), [("0", "10")])
        self.assertFalse(tl.segments()[0].is_conflict)
        self.assertEqual(tl.segments()[0].value, "A")
        self.assertEqual(tl.segments()[0].entries[0].id, a)

    def test_adjacent_same_value_different_source_not_merged(self):
        """相邻同值但不同来源：证据不得误丢，必须保留为两段。"""
        tl = Timeline()
        first = tl.add(0, 5, "X", priority=0, source="sensor-1")
        second = tl.add(5, 10, "X", priority=0, source="sensor-2")
        segs = tl.segments()
        self.assertEqual(len(segs), 2)
        self.assertEqual(bounds(segs), [("0", "5"), ("5", "10")])
        self.assertEqual(segs[0].sources, ("sensor-1",))
        self.assertEqual(segs[1].sources, ("sensor-2",))
        self.assertNotEqual(segs[0].entries, segs[1].entries)

        # 同一来源相邻则合并
        tl2 = Timeline()
        tl2.add(0, 5, "X", source="same")
        tl2.add(5, 10, "X", source="same")
        self.assertEqual(bounds(tl2.segments()), [("0", "10")])


class TestLocalUpdate(unittest.TestCase):
    def test_add_only_changes_overlapping_window(self):
        tl = Timeline()
        tl.add(0, 10, "L", priority=0, source="base")
        before = tl.segments()[0]
        tl.add(3, 7, "H", priority=1, source="override")
        left, _, right = tl.segments()
        # 窗口外的两个段与原段来自同一个条目（证据未动）
        self.assertEqual(left.entries, before.entries)
        self.assertEqual(right.entries, before.entries)
        self.assertEqual(bounds([left, right]), [("0", "3"), ("7", "10")])

    def test_remove_only_changes_its_window(self):
        tl = Timeline()
        tl.add(0, 20, "L0", priority=0, source="base-0")
        h = tl.add(3, 7, "H", priority=1, source="override")
        other = tl.add(15, 18, "L1", priority=1, source="far")
        segs_before = tl.segments()
        far = next(s for s in segs_before if s.entries[0].id == other)
        tl.remove(h)
        far_after = tl.segment_at(16)
        # 远处分段完全不受撤回影响（同一 Segment 对象）
        self.assertIs(far_after, far)


class TestQueriesAndSources(unittest.TestCase):
    def test_segment_at(self):
        tl = Timeline()
        tl.add(0, 10, "L", priority=0, source="base")
        tl.add(3, 7, "H", priority=1, source="override")
        self.assertEqual(tl.segment_at(3).value, "H")   # 左闭
        self.assertEqual(tl.segment_at(7).value, "L")   # 右开：[3,7)，7 处恢复低优先
        self.assertIsNone(tl.segment_at(10))            # 整体右开：[0,10)
        self.assertIsNone(tl.segment_at(-1))
        self.assertEqual(tl.segment_at(1).value, "L")

    def test_range_query_is_clipped(self):
        tl = Timeline()
        tl.add(0, 10, "L", priority=0, source="base")
        tl.add(3, 7, "H", priority=1, source="override")
        view = tl.segments(2, 8)
        self.assertEqual(bounds(view), [("2", "3"), ("3", "7"), ("7", "8")])

    def test_withdraw_source(self):
        tl = Timeline()
        tl.add(0, 10, "A", priority=1, source="dev-1")
        tl.add(2, 4, "B", priority=1, source="dev-1")
        tl.add(5, 8, "C", priority=1, source="dev-2")
        removed_ids = tl.withdraw_source("dev-1")
        self.assertEqual(len(removed_ids), 2)
        self.assertEqual(bounds(tl.segments()), [("5", "8")])
        self.assertEqual(tl.segments()[0].sources, ("dev-2",))
        self.assertEqual([e.source for e in tl.entries()], ["dev-2"])

    def test_entries_are_source_evidence(self):
        tl = Timeline()
        tl.add(0, 5, "X", source="s1")
        tl.add(5, 10, "X", source="s2")
        self.assertEqual([e.source for e in tl.entries("s1")], ["s1"])


class TestExactOffsets(unittest.TestCase):
    def test_fraction_boundaries(self):
        tl = Timeline()
        tl.add(0, Fraction(1), "L", priority=0, source="base")
        tl.add(Fraction(1, 3), Fraction(2, 3), "H", priority=1, source="override")
        segs = tl.segments()
        self.assertEqual(bounds(segs), [("0", "1/3"), ("1/3", "2/3"), ("2/3", "1")])
        # 边界点不丢精度
        self.assertIsInstance(segs[0].end.offset, Fraction)

    def test_invalid_interval_rejected(self):
        tl = Timeline()
        with self.assertRaises(ValueError):
            tl.add(5, 5, "x")
        with self.assertRaises(ValueError):
            tl.add(5, 4, "x")


class TestThreeLayerStack(unittest.TestCase):
    def test_multiple_layers_and_gap(self):
        tl = Timeline()
        tl.add(0, 10, "L", priority=0, source="base")
        tl.add(2, 8, "M", priority=1, source="mid")
        tl.add(4, 6, "H", priority=2, source="top")
        self.assertEqual(bounds(tl.segments()),
                         [("0", "2"), ("2", "4"), ("4", "6"), ("6", "8"), ("8", "10")])
        self.assertEqual([s.priority for s in tl.segments()], [0, 1, 2, 1, 0])
        tl.withdraw_source("mid")
        self.assertEqual(bounds(tl.segments()), [("0", "4"), ("4", "6"), ("6", "10")])
        self.assertEqual([s.priority for s in tl.segments()], [0, 2, 0])


if __name__ == "__main__":
    unittest.main()
