import unittest

from vtline import (
    BaselineMismatchError,
    ChangeKind,
    ChangeSpan,
    Diff,
    Entry,
    Instant,
    Segment,
    Timeline,
    compose,
    diff,
    endpoints_of,
    full_scan_equal,
    inverse,
)


def I(n):
    return Instant(n)


A = Entry("a", 1)
A2 = Entry("a", 2)  # correction of "a"
B = Entry("b", 2)   # different identity, same value as A2


def tl(*specs):
    return Timeline([Segment(I(s), I(e), entry) for s, e, entry in specs])


class NormalizeTests(unittest.TestCase):
    def test_zero_length_segments_dropped(self):
        t = Timeline([Segment(I(0), I(0), A), Segment(I(0), I(5), A)])
        self.assertEqual(len(t.segments), 1)

    def test_negative_length_rejected(self):
        with self.assertRaises(ValueError):
            Timeline([Segment(I(5), I(0), A)])

    def test_overlap_rejected(self):
        with self.assertRaises(ValueError):
            Timeline([Segment(I(0), I(5), A), Segment(I(4), I(9), B)])

    def test_adjacent_same_entry_merged(self):
        t = Timeline([Segment(I(0), I(5), A), Segment(I(5), I(9), A)])
        self.assertEqual(len(t.segments), 1)
        self.assertEqual(t.segments[0].end, I(9))


class DiffTests(unittest.TestCase):
    def test_classifies_add_delete_modify(self):
        old = tl((0, 5, A), (10, 15, A))
        new = tl((0, 5, A2), (7, 10, B))
        d = diff(old, new)
        kinds = {c.kind for c in d.changes}
        self.assertEqual(kinds, {ChangeKind.MODIFY, ChangeKind.ADD, ChangeKind.DELETE})

    def test_same_value_adjacent_spans_merge(self):
        old = tl((0, 5, A))
        new = tl((0, 10, A))
        d = diff(old, new)
        self.assertEqual(len(d.changes), 1)
        c = d.changes[0]
        self.assertEqual((c.kind, c.start, c.end), (ChangeKind.ADD, I(5), I(10)))

    def test_different_change_types_do_not_merge(self):
        old = tl((0, 5, A))
        new = tl((5, 10, B))
        d = diff(old, new)
        self.assertEqual(len(d.changes), 2)
        self.assertEqual(d.changes[0].kind, ChangeKind.DELETE)
        self.assertEqual(d.changes[1].kind, ChangeKind.ADD)

    def test_same_value_different_identity_is_a_change(self):
        # history significance is not erased by an equal current value
        old = tl((0, 5, A2))
        new = tl((0, 5, B))
        d = diff(old, new)
        self.assertEqual(len(d.changes), 1)
        self.assertEqual(d.changes[0].kind, ChangeKind.MODIFY)
        self.assertFalse(d.changes[0].is_correction)

    def test_correction_detected_by_identity(self):
        d = diff(tl((0, 5, A)), tl((0, 5, A2)))
        self.assertTrue(d.changes[0].is_correction)

    def test_zero_length_changes_not_emitted(self):
        t = tl((0, 10, A))
        self.assertFalse(diff(t, tl((0, 10, A))))
        # a boundary that appears and disappears leaves no trace
        rebuilt = Timeline([Segment(I(0), I(5), A), Segment(I(5), I(10), A)])
        self.assertFalse(diff(t, rebuilt))

    def test_diff_records_baseline_digest(self):
        old = tl((0, 5, A))
        d = diff(old, tl((0, 5, A2)))
        self.assertEqual(d.base_digest, old.summary().digest)


class ApplyTests(unittest.TestCase):
    def test_apply_then_inverse_restores_baseline(self):
        old = tl((0, 5, A), (10, 15, B), (20, 25, A2))
        new = tl((0, 3, A), (4, 12, B), (20, 22, A), (30, 35, B))
        d = diff(old, new)
        applied, _ = old.apply(d)
        restored, _ = applied.apply(inverse(d))
        pts = endpoints_of(old, new, d)
        self.assertTrue(full_scan_equal(restored, old, pts))
        self.assertTrue(full_scan_equal(applied, new, pts))

    def test_baseline_summary_verified(self):
        old = tl((0, 5, A))
        other = tl((0, 5, B))
        d = diff(old, tl((0, 5, A2)))
        with self.assertRaises(BaselineMismatchError):
            other.apply(d)

    def test_zero_length_change_span_rejected_on_apply(self):
        t = tl((0, 5, A))
        bad = Diff((ChangeSpan(I(2), I(2), ChangeKind.MODIFY, A, A2),))
        with self.assertRaises(ValueError):
            t.apply(bad)

    def test_old_snapshot_shares_unchanged_nodes(self):
        old = tl((0, 5, A), (10, 15, B), (20, 25, A2))
        d = diff(old, tl((0, 5, A), (10, 15, Entry("b", 9)), (20, 25, A2)))
        new, stats = old.apply(d)
        # only the middle segment changed: 1 copied node, 2 shared nodes
        self.assertEqual(stats.copied_nodes, 1)
        self.assertEqual(stats.shared_nodes, 2)
        self.assertIs(new.segments[0], old.segments[0])
        self.assertIs(new.segments[2], old.segments[2])
        # old snapshot is untouched
        self.assertEqual(old.segments[1].entry, B)

    def test_copied_nodes_counted_on_clip_and_insert(self):
        old = tl((0, 10, A))
        d = diff(old, tl((0, 2, A), (2, 4, B), (4, 10, A)))
        new, stats = old.apply(d)
        self.assertEqual(stats.copied_nodes, 3)
        self.assertEqual(stats.shared_nodes, 0)
        self.assertEqual(len(new.segments), 3)

    def test_summary_changes_with_content(self):
        t1 = tl((0, 5, A))
        t2 = tl((0, 5, A2))
        self.assertNotEqual(t1.summary().digest, t2.summary().digest)
        self.assertEqual(tl((0, 5, A)).summary(), t1.summary())


class ComposeTests(unittest.TestCase):
    def test_compose_matches_sequential_apply(self):
        base = tl((0, 10, A), (20, 30, B))
        d1 = diff(base, tl((0, 10, A2), (20, 30, B), (40, 50, A)))
        mid, _ = base.apply(d1)
        d2 = diff(mid, tl((0, 5, A2), (5, 10, A), (20, 25, B), (40, 45, A)))
        seq, _ = mid.apply(d2)
        combined = compose(d1, d2)
        direct, _ = base.apply(combined)
        pts = endpoints_of(base, seq, d1, d2, combined)
        self.assertTrue(full_scan_equal(direct, seq, pts))

    def test_compose_cancelling_changes_dropped(self):
        base = tl((0, 10, A))
        d1 = diff(base, tl((0, 10, A2)))
        mid, _ = base.apply(d1)
        d2 = diff(mid, tl((0, 10, A)))
        self.assertFalse(compose(d1, d2))

    def test_inverse_of_inverse_is_original_transform(self):
        base = tl((0, 10, A))
        d = diff(base, tl((0, 10, A2), (15, 20, B)))
        twice, _ = base.apply(compose(d, inverse(d)))
        pts = endpoints_of(base, d)
        self.assertTrue(full_scan_equal(twice, base, pts))


if __name__ == "__main__":
    unittest.main()
