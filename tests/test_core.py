import unittest

from temporal import (
    MISSING,
    Change,
    ChangeKind,
    Delta,
    Segment,
    Timeline,
    TimePoint,
    apply,
    diff,
    invert,
    summarize,
    scan_equal,
    verify_inverse,
    verify_summary,
)


def tp(n):
    return TimePoint(n)


def tl(*spec):
    return Timeline(Segment(tp(a), tp(b), v) for a, b, v in spec)


class TestTimeline(unittest.TestCase):
    def test_adjacent_same_value_segments_coalesce(self):
        timeline = tl((0, 2, "A"), (2, 5, "A"), (5, 9, "B"), (9, 12, "B"))
        self.assertEqual(len(timeline), 2)
        self.assertEqual(timeline.segments[0], Segment(tp(0), tp(5), "A"))
        self.assertEqual(timeline.segments[1], Segment(tp(5), tp(12), "B"))

    def test_zero_length_segment_rejected(self):
        with self.assertRaises(ValueError):
            Segment(tp(3), tp(3), "A")

    def test_overlap_rejected(self):
        with self.assertRaises(ValueError):
            tl((0, 5, "A"), (4, 9, "B"))

    def test_value_at_and_missing(self):
        timeline = tl((0, 5, "A"), (7, 9, "B"))
        self.assertEqual(timeline.value_at(tp(0)), "A")
        self.assertIs(timeline.value_at(tp(6)), MISSING)
        self.assertIs(timeline.value_at(tp(5)), MISSING)  # half-open


class TestDiff(unittest.TestCase):
    def test_empty_diff_for_identical_timelines(self):
        base = tl((0, 5, "A"), (7, 9, "B"))
        self.assertEqual(diff(base, base), ())

    def test_distinguishes_insert_delete_update(self):
        base = tl((0, 5, "A"), (10, 15, "B"))
        new = tl((2, 5, "A"), (5, 8, "N"), (10, 15, "C"))
        changes = diff(base, new)
        kinds = [c.kind for c in changes]
        self.assertEqual(kinds, [ChangeKind.DELETE, ChangeKind.INSERT, ChangeKind.UPDATE])
        self.assertEqual(changes[0], Change(ChangeKind.DELETE, tp(0), tp(2), "A", MISSING))
        self.assertEqual(changes[1], Change(ChangeKind.INSERT, tp(5), tp(8), MISSING, "N"))
        self.assertEqual(changes[2], Change(ChangeKind.UPDATE, tp(10), tp(15), "B", "C"))

    def test_minimal_continuous_segments(self):
        base = tl((0, 10, "A"))
        new = tl((0, 4, "A"), (4, 10, "B"))
        self.assertEqual(diff(base, new),
                         (Change(ChangeKind.UPDATE, tp(4), tp(10), "A", "B"),))

    def test_same_value_merges_but_kinds_do_not(self):
        # Same new value "Y" on both sides of t=5, but UPDATE and INSERT
        # are different change kinds and must stay separate segments.
        base = tl((0, 5, "X"))
        new = tl((0, 5, "Y"), (5, 8, "Y"))
        changes = diff(base, new)
        self.assertEqual(changes, (
            Change(ChangeKind.UPDATE, tp(0), tp(5), "X", "Y"),
            Change(ChangeKind.INSERT, tp(5), tp(8), MISSING, "Y"),
        ))

    def test_zero_length_change_not_constructible(self):
        with self.assertRaises(ValueError):
            Change(ChangeKind.UPDATE, tp(4), tp(4), "A", "B")

    def test_change_shape_validation(self):
        with self.assertRaises(ValueError):
            Change(ChangeKind.INSERT, tp(0), tp(1), "A", "B")
        with self.assertRaises(ValueError):
            Change(ChangeKind.UPDATE, tp(0), tp(1), "A", "A")


class TestApplyInvert(unittest.TestCase):
    def test_apply_reproduces_target(self):
        base = tl((0, 5, "A"), (10, 15, "B"))
        target = tl((2, 5, "A"), (5, 8, "N"), (10, 15, "C"))
        changes = diff(base, target)
        result = apply(base, changes)
        self.assertTrue(scan_equal(result.timeline, target))

    def test_inverse_delta_restores_base(self):
        base = tl((0, 5, "A"), (10, 15, "B"))
        target = tl((0, 3, "Z"), (6, 10, "Q"), (10, 12, "B"))
        changes = diff(base, target)
        forward = apply(base, changes)
        restored = apply(forward.timeline, invert(changes))
        self.assertTrue(scan_equal(restored.timeline, base))
        self.assertTrue(verify_inverse(base, changes))

    def test_apply_checks_base_values(self):
        base = tl((0, 5, "A"))
        bogus = Change(ChangeKind.UPDATE, tp(0), tp(5), "WRONG", "B")
        with self.assertRaises(ValueError):
            apply(base, [bogus])

    def test_overlapping_changes_rejected(self):
        base = tl((0, 10, "A"))
        changes = [
            Change(ChangeKind.UPDATE, tp(0), tp(6), "A", "B"),
            Change(ChangeKind.UPDATE, tp(4), tp(10), "A", "C"),
        ]
        with self.assertRaises(ValueError):
            apply(base, changes)


class TestStructuralSharing(unittest.TestCase):
    def test_unchanged_nodes_are_shared_and_copies_counted(self):
        base = tl((0, 5, "A"), (5, 10, "B"), (10, 15, "C"))
        change = Change(ChangeKind.UPDATE, tp(5), tp(10), "B", "D")
        result = apply(base, [change])
        self.assertEqual(result.shared_nodes, 2)
        self.assertEqual(result.copied_nodes, 1)
        self.assertIs(result.timeline.segments[0], base.segments[0])
        self.assertIs(result.timeline.segments[2], base.segments[2])
        self.assertIsNot(result.timeline.segments[1], base.segments[1])
        # The old snapshot is untouched and still valid.
        self.assertEqual(base.value_at(tp(7)), "B")

    def test_no_change_means_full_sharing(self):
        base = tl((0, 5, "A"), (5, 10, "B"))
        result = apply(base, [])
        self.assertEqual(result.copied_nodes, 0)
        self.assertEqual(result.shared_nodes, 2)


class TestSummary(unittest.TestCase):
    def test_baseline_summary_verifies(self):
        base = tl((0, 5, "A"), (5, 10, "B"))
        baseline = summarize(base)
        self.assertTrue(verify_summary(base, baseline))
        changed = apply(base, [Change(ChangeKind.UPDATE, tp(0), tp(5), "A", "Z")])
        self.assertFalse(verify_summary(changed.timeline, baseline))

    def test_summary_fields(self):
        base = tl((0, 5, "A"), (5, 10, "B"))
        summary = summarize(base)
        self.assertEqual(summary.nodes, 2)
        self.assertEqual(summary.start, tp(0))
        self.assertEqual(summary.end, tp(10))
        self.assertEqual(len(summary.digest), 64)


class TestDeltaCorrections(unittest.TestCase):
    def test_correction_supersedes_and_retraction_restores(self):
        base = tl((0, 10, "X"))
        delta = Delta()
        delta.add(Change(ChangeKind.UPDATE, tp(0), tp(10), "X", "Y", identity="c1"))
        self.assertEqual(apply(base, delta.changes()).timeline.value_at(tp(3)), "Y")
        # A correction with the same identity supersedes the earlier change.
        delta.correct(Change(ChangeKind.UPDATE, tp(0), tp(10), "X", "Z", identity="c1"))
        self.assertEqual(len(delta), 1)
        self.assertEqual(apply(base, delta.changes()).timeline.value_at(tp(3)), "Z")
        # Retraction withdraws the correction: the base value returns.
        delta.retract("c1")
        self.assertEqual(len(delta), 0)
        self.assertTrue(scan_equal(apply(base, delta.changes()).timeline, base))

    def test_retraction_is_by_identity_not_current_value(self):
        # Both intervals currently read "Y", but only [0,5) became "Y"
        # through change c1.  Retracting c1 must restore "X" on [0,5)
        # while the genuinely-"Y" interval [5,10) is untouched -- a
        # value-based elimination could not tell the two apart.
        base = tl((0, 5, "X"), (5, 10, "Y"))
        delta = Delta()
        delta.add(Change(ChangeKind.UPDATE, tp(0), tp(5), "X", "Y", identity="c1"))
        current = apply(base, delta.changes()).timeline
        self.assertEqual(current.value_at(tp(2)), current.value_at(tp(7)))
        delta.retract("c1")
        restored = apply(base, delta.changes()).timeline
        self.assertTrue(scan_equal(restored, base))
        self.assertEqual(restored.value_at(tp(2)), "X")
        self.assertEqual(restored.value_at(tp(7)), "Y")

    def test_correction_requires_existing_identity(self):
        delta = Delta()
        with self.assertRaises(KeyError):
            delta.correct(Change(ChangeKind.UPDATE, tp(0), tp(1), "X", "Y",
                                 identity="nope"))


if __name__ == "__main__":
    unittest.main()
