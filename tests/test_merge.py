import unittest

from temporal import (
    MISSING,
    Change,
    ChangeKind,
    Segment,
    Timeline,
    TimePoint,
    apply,
    diff,
    merge3,
    rebase,
    scan_equal,
)


def tp(n):
    return TimePoint(n)


def tl(*spec):
    return Timeline(Segment(tp(a), tp(b), v) for a, b, v in spec)


class TestMerge3(unittest.TestCase):
    def setUp(self):
        self.base = tl((0, 10, "A"), (10, 20, "B"), (20, 30, "C"))

    def test_disjoint_deltas_merge_cleanly(self):
        a = [Change(ChangeKind.UPDATE, tp(0), tp(10), "A", "A2")]
        b = [Change(ChangeKind.UPDATE, tp(20), tp(30), "C", "C2")]
        result = merge3(self.base, a, b)
        self.assertTrue(result.ok)
        self.assertTrue(scan_equal(result.timeline,
                                   tl((0, 10, "A2"), (10, 20, "B"), (20, 30, "C2"))))
        self.assertGreaterEqual(result.shared_nodes, 1)
        self.assertEqual(result.copied_nodes + result.shared_nodes,
                         len(result.timeline))

    def test_compatible_overlap_merges(self):
        a = [Change(ChangeKind.UPDATE, tp(0), tp(10), "A", "X")]
        b = [Change(ChangeKind.UPDATE, tp(0), tp(10), "A", "X"),
             Change(ChangeKind.UPDATE, tp(10), tp(15), "B", "Y")]
        result = merge3(self.base, a, b)
        self.assertTrue(result.ok)
        self.assertTrue(scan_equal(result.timeline,
                                   tl((0, 10, "X"), (10, 15, "Y"), (15, 20, "B"),
                                      (20, 30, "C"))))

    def test_incompatible_overlap_reports_minimal_conflict(self):
        base = tl((0, 20, "A"), (20, 30, "C"))
        a = [Change(ChangeKind.UPDATE, tp(0), tp(15), "A", "X")]
        b = [Change(ChangeKind.UPDATE, tp(5), tp(20), "A", "Y")]
        result = merge3(base, a, b)
        self.assertFalse(result.ok)
        # The conflict is exactly the disagreeing overlap [5,15), not the
        # whole changed span [0,20).
        self.assertEqual(len(result.conflicts), 1)
        conflict = result.conflicts[0]
        self.assertEqual((conflict.start, conflict.end), (tp(5), tp(15)))
        self.assertEqual(conflict.a_value, "X")
        self.assertEqual(conflict.b_value, "Y")
        # Non-conflicted parts of both branches still apply; the conflicted
        # region keeps its base value.
        self.assertEqual(result.timeline.value_at(tp(2)), "X")
        self.assertEqual(result.timeline.value_at(tp(7)), "A")
        self.assertEqual(result.timeline.value_at(tp(12)), "A")
        self.assertEqual(result.timeline.value_at(tp(17)), "Y")

    def test_delete_versus_update_conflicts(self):
        a = [Change(ChangeKind.DELETE, tp(0), tp(10), "A", MISSING)]
        b = diff(self.base, tl((0, 5, "A"), (5, 15, "Z"), (15, 20, "B"),
                               (20, 30, "C")))
        result = merge3(self.base, a, b)
        self.assertEqual(len(result.conflicts), 1)
        conflict = result.conflicts[0]
        self.assertEqual((conflict.start, conflict.end), (tp(5), tp(10)))
        self.assertIs(conflict.a_value, MISSING)
        self.assertEqual(conflict.b_value, "Z")
        self.assertEqual(conflict.base_value, "A")

    def test_double_delete_is_compatible(self):
        a = [Change(ChangeKind.DELETE, tp(0), tp(10), "A", MISSING)]
        b = [Change(ChangeKind.DELETE, tp(0), tp(10), "A", MISSING)]
        result = merge3(self.base, a, b)
        self.assertTrue(result.ok)
        self.assertIs(result.timeline.value_at(tp(5)), MISSING)

    def test_merge_result_verified_by_full_scan(self):
        a = diff(self.base, tl((0, 4, "A2"), (4, 10, "A"), (10, 20, "B"),
                               (20, 25, "C"), (25, 30, "C2")))
        b = diff(self.base, tl((0, 4, "A"), (4, 10, "A9"), (10, 20, "B2"),
                               (20, 30, "C")))
        result = merge3(self.base, a, b)
        self.assertTrue(result.ok)
        expected = tl((0, 4, "A2"), (4, 10, "A9"), (10, 20, "B2"),
                      (20, 25, "C"), (25, 30, "C2"))
        self.assertTrue(scan_equal(result.timeline, expected))


class TestRebase(unittest.TestCase):
    def test_rebase_expresses_delta_on_top_of_onto(self):
        base = tl((0, 10, "A"), (10, 20, "B"))
        onto = [Change(ChangeKind.UPDATE, tp(0), tp(10), "A", "A2")]
        mine = [Change(ChangeKind.UPDATE, tp(10), tp(20), "B", "B2")]
        result = rebase(base, onto, mine)
        self.assertTrue(result.ok)
        self.assertEqual(result.onto.value_at(tp(5)), "A2")
        # The rebased delta applies cleanly onto the rebased base and
        # reproduces the merged timeline (full-scan verified).
        replayed = apply(result.onto, result.changes)
        self.assertTrue(scan_equal(replayed.timeline, result.merged))
        self.assertEqual(replayed.timeline.value_at(tp(15)), "B2")

    def test_rebase_propagates_conflicts(self):
        base = tl((0, 10, "A"))
        onto = [Change(ChangeKind.UPDATE, tp(0), tp(10), "A", "X")]
        mine = [Change(ChangeKind.UPDATE, tp(0), tp(10), "A", "Y")]
        result = rebase(base, onto, mine)
        self.assertFalse(result.ok)
        self.assertEqual(len(result.conflicts), 1)
        self.assertEqual(result.conflicts[0].a_value, "X")
        self.assertEqual(result.conflicts[0].b_value, "Y")


if __name__ == "__main__":
    unittest.main()
