import types
import unittest
from datetime import date

from calpart.exact import WallTime
from calpart.offsets import OffsetTable, Transition
from tests.helpers import FALLBACK, REPEAT_DAY, SKIP_DAY, SPRING, UTC, utc


class TestOffsetQueries(unittest.TestCase):
    def test_offset_at_around_transition(self):
        self.assertEqual(FALLBACK.offset_at(utc(2023, 10, 29, 0)), 7200)
        self.assertEqual(FALLBACK.offset_at(utc(2023, 10, 29, 1)), 3600)
        self.assertEqual(UTC.offset_at(123456789), 0)

    def test_instant_to_wall(self):
        w = FALLBACK.instant_to_wall(utc(2023, 10, 29, 0))
        self.assertEqual((w.day, w.seconds), (date(2023, 10, 29), 7200))
        w2 = FALLBACK.instant_to_wall(utc(2023, 10, 29, 2))
        self.assertEqual((w2.day, w2.seconds), (date(2023, 10, 29), 3 * 3600))

    def test_wall_to_instants_unique(self):
        pre = UTC.wall_to_instants(WallTime(date(2024, 5, 6), 3600))
        self.assertEqual(pre, (utc(2024, 5, 6, 1),))

    def test_wall_to_instants_ambiguous_fallback(self):
        # Local 02:30 happens twice on the 25-hour day.
        pre = FALLBACK.wall_to_instants(WallTime(date(2023, 10, 29), 2 * 3600 + 1800))
        self.assertEqual(pre, (utc(2023, 10, 29, 0, 30), utc(2023, 10, 29, 1, 30)))

    def test_wall_to_instants_gap(self):
        # Local 02:30 never happens on the spring-forward day.
        pre = SPRING.wall_to_instants(WallTime(date(2023, 3, 26), 2 * 3600 + 1800))
        self.assertEqual(pre, ())

    def test_wall_to_instants_repeated_midnight(self):
        # After the -30h jump, wall midnight 2020-06-14 occurs twice.
        pre = REPEAT_DAY.wall_to_instants(WallTime(date(2020, 6, 14), 0))
        self.assertEqual(pre, (utc(2020, 6, 14, 0), utc(2020, 6, 15, 6)))

    def test_skipped_date_has_no_midnight(self):
        pre = SKIP_DAY.wall_to_instants(WallTime(date(2011, 12, 30), 0))
        self.assertEqual(pre, ())


class TestRevision(unittest.TestCase):
    def setUp(self):
        self.table = OffsetTable(0, [Transition(1000, 3600), Transition(5000, 7200)])

    def test_revise_leaves_outside_unchanged(self):
        revised = self.table.revise(2000, 4000, [Transition(2500, -3600)])
        for t in (0, 500, 1500, 1999, 4000, 4500, 4999, 5000, 6000):
            self.assertEqual(revised.offset_at(t), self.table.offset_at(t), t)
            self.assertEqual(revised.instant_to_wall(t), self.table.instant_to_wall(t), t)

    def test_revise_changes_inside(self):
        revised = self.table.revise(2000, 4000, [Transition(2500, -3600)])
        self.assertEqual(revised.offset_at(2000), 3600)
        self.assertEqual(revised.offset_at(3000), -3600)
        self.assertEqual(self.table.offset_at(3000), 3600)

    def test_revise_clips_new_transitions_to_range(self):
        revised = self.table.revise(2000, 4000, [Transition(1500, 99), Transition(2500, -3600)])
        self.assertNotIn(99, [tr.offset for tr in revised.transitions])

    def test_revise_empty_replacement_restores_offset(self):
        revised = self.table.revise(2000, 6000, [])
        self.assertEqual(revised.offset_at(3000), 3600)
        self.assertEqual(revised.offset_at(5500), 3600)
        self.assertEqual(revised.offset_at(6000), self.table.offset_at(6000))


class TestPagination(unittest.TestCase):
    def setUp(self):
        transitions = [Transition(utc(2000 + i, 1, 1), i * 60) for i in range(25)]
        self.table = OffsetTable(0, transitions)

    def test_pages_are_lazy_and_sized(self):
        pages = self.table.iter_transition_pages(utc(1999, 6, 1), utc(2026, 1, 1), 10)
        self.assertIsInstance(pages, types.GeneratorType)
        sizes = [len(p) for p in pages]
        self.assertEqual(sizes, [10, 10, 5])

    def test_pages_match_eager_query(self):
        eager = self.table.transitions_between(utc(2001, 1, 1), utc(2011, 1, 1))
        paged = tuple(
            tr
            for page in self.table.iter_transition_pages(utc(2001, 1, 1), utc(2011, 1, 1), 3)
            for tr in page
        )
        self.assertEqual(paged, eager)

    def test_invalid_page_size(self):
        with self.assertRaises(ValueError):
            list(self.table.iter_transition_pages(0, 10, 0))


if __name__ == "__main__":
    unittest.main()
