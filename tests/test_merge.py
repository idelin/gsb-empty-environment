import unittest

from vtline import (
    BaselineMismatchError,
    Entry,
    Instant,
    Repository,
    Segment,
    Timeline,
    diff,
    endpoints_of,
    full_scan_equal,
    merge_three_way,
    rebase,
)


def I(n):
    return Instant(n)


A = Entry("a", 1)
L = Entry("a", 100)   # left's correction of "a"
R = Entry("a", 200)   # right's correction of "a"
B = Entry("b", 2)


def tl(*specs):
    return Timeline([Segment(I(s), I(e), entry) for s, e, entry in specs])


class MergeTests(unittest.TestCase):
    def test_non_overlapping_branches_merge_cleanly(self):
        base = tl((0, 10, A), (20, 30, B))
        left = diff(base, tl((0, 10, L), (20, 30, B)))
        right = diff(base, tl((0, 10, A), (20, 30, Entry("b", 9))))
        result = merge_three_way(base, left, right)
        self.assertEqual(result.conflicts, ())
        expected = tl((0, 10, L), (20, 30, Entry("b", 9)))
        pts = endpoints_of(base, expected, left, right)
        self.assertTrue(full_scan_equal(result.timeline, expected, pts))

    def test_identical_changes_on_both_sides_merge(self):
        base = tl((0, 10, A))
        left = diff(base, tl((0, 10, L)))
        right = diff(base, tl((0, 10, L)))
        result = merge_three_way(base, left, right)
        self.assertEqual(result.conflicts, ())
        self.assertEqual(result.timeline.entry_at(I(5)), L)

    def test_overlapping_incompatible_modify_yields_minimal_conflict(self):
        base = tl((0, 10, A))
        left = diff(base, tl((0, 2, A), (2, 6, L), (6, 10, A)))
        right = diff(base, tl((0, 4, A), (4, 8, R), (8, 10, A)))
        result = merge_three_way(base, left, right)
        # exactly one minimal conflict segment: the overlap [4, 6)
        self.assertEqual(len(result.conflicts), 1)
        c = result.conflicts[0]
        self.assertEqual((c.start, c.end), (I(4), I(6)))
        self.assertEqual((c.base, c.left, c.right), (A, L, R))
        # non-conflicting parts of both branches are applied
        self.assertEqual(result.timeline.entry_at(I(3)), L)
        self.assertEqual(result.timeline.entry_at(I(7)), R)
        # conflict region keeps base content
        self.assertEqual(result.timeline.entry_at(I(5)), A)

    def test_delete_versus_modify_conflicts(self):
        base = tl((0, 10, A))
        left = diff(base, tl((0, 2, A), (6, 10, A)))          # delete [2,6)
        right = diff(base, tl((0, 4, A), (4, 8, R), (8, 10, A)))
        result = merge_three_way(base, left, right)
        self.assertEqual(len(result.conflicts), 1)
        c = result.conflicts[0]
        self.assertEqual((c.start, c.end), (I(4), I(6)))
        self.assertIsNone(c.left)
        self.assertEqual(c.right, R)
        self.assertIsNone(result.timeline.entry_at(I(3)))     # clean delete part
        self.assertEqual(result.timeline.entry_at(I(7)), R)   # clean modify part

    def test_merge_result_verified_by_full_scan(self):
        base = tl((0, 10, A), (20, 30, B))
        left = diff(base, tl((0, 10, L), (20, 30, B), (40, 50, A)))
        right = diff(base, tl((0, 10, A), (15, 25, Entry("b", 7)), (25, 30, B)))
        result = merge_three_way(base, left, right)
        replay, _ = base.apply(result.diff)
        pts = endpoints_of(base, result.timeline, left, right, result.diff)
        self.assertTrue(full_scan_equal(replay, result.timeline, pts))

    def test_branch_not_against_base_rejected(self):
        base = tl((0, 10, A))
        other = tl((0, 10, B))
        left = diff(other, tl((0, 10, L)))
        right = diff(base, tl((0, 10, R)))
        with self.assertRaises(BaselineMismatchError):
            merge_three_way(base, left, right)


class RebaseTests(unittest.TestCase):
    def test_rebase_onto_independent_base(self):
        old_base = tl((0, 10, A))
        moving = diff(old_base, tl((0, 2, A), (2, 4, L), (4, 10, A)))
        new_base = tl((0, 10, A), (20, 30, B))
        result = rebase(moving, old_base, new_base)
        self.assertEqual(result.conflicts, ())
        rebased, _ = new_base.apply(result.diff)
        expected = tl((0, 2, A), (2, 4, L), (4, 10, A), (20, 30, B))
        pts = endpoints_of(old_base, new_base, expected, moving, result.diff)
        self.assertTrue(full_scan_equal(rebased, expected, pts))

    def test_rebase_with_conflict_is_reported(self):
        old_base = tl((0, 10, A))
        moving = diff(old_base, tl((0, 10, L)))
        new_base = tl((0, 10, R))
        result = rebase(moving, old_base, new_base)
        self.assertEqual(len(result.conflicts), 1)
        c = result.conflicts[0]
        self.assertEqual((c.left, c.right), (L, R))


class RepositoryTests(unittest.TestCase):
    def test_correction_retractable_by_identity(self):
        repo = Repository()
        repo.commit(diff(repo.head, tl((0, 10, A))))
        repo.commit(diff(repo.head, tl((0, 10, L))))  # correction of "a"
        d = repo.retract("a")
        self.assertIsNotNone(d)
        self.assertEqual(repo.head.entry_at(I(5)), A)

    def test_retraction_does_not_match_by_current_value(self):
        repo = Repository()
        same_value_other_identity = Entry("b", 100)  # same value as L
        repo.commit(diff(repo.head, tl((0, 10, A), (20, 30, same_value_other_identity))))
        repo.commit(diff(repo.head, tl((0, 10, L), (20, 30, same_value_other_identity))))
        repo.retract("a")
        # "b" holds exactly the value "a" was corrected to, yet is untouched
        self.assertEqual(repo.head.entry_at(I(25)), same_value_other_identity)
        self.assertEqual(repo.head.entry_at(I(5)), A)

    def test_retract_without_correction_raises(self):
        repo = Repository()
        repo.commit(diff(repo.head, tl((0, 10, A))))
        with self.assertRaises(KeyError):
            repo.retract("a")

    def test_history_keeps_all_versions(self):
        repo = Repository()
        repo.commit(diff(repo.head, tl((0, 10, A))))
        repo.commit(diff(repo.head, tl((0, 10, L))))
        self.assertEqual(len(repo.versions), 3)
        self.assertEqual(repo.versions[1].entry_at(I(5)), A)
        self.assertEqual(repo.versions[2].entry_at(I(5)), L)


if __name__ == "__main__":
    unittest.main()
