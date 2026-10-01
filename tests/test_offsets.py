import unittest

from calpart import OffsetTable
from tests.helpers import U, europe_like, repeat_hour_table, wild_jump_table


class TestOffsetTable(unittest.TestCase):
    def test_fixed_offset(self):
        table = OffsetTable.fixed(19800)
        self.assertEqual(table.offset_at(U(2024, 5, 1)), 19800)
        self.assertEqual(table.to_local(U(2024, 5, 1)), U(2024, 5, 1, 5, 30))

    def test_base_offset_before_first_transition(self):
        table = europe_like()
        self.assertEqual(table.offset_at(U(2020, 1, 1)), 3600)

    def test_transition_is_inclusive(self):
        table = europe_like()
        self.assertEqual(table.offset_at(U(2024, 3, 31, 0, 59, 59)), 3600)
        self.assertEqual(table.offset_at(U(2024, 3, 31, 1)), 7200)

    def test_preimage_single_contiguous_for_normal_day(self):
        table = europe_like()
        pre = table.preimage_local_interval(U(2024, 10, 26), U(2024, 10, 27))
        self.assertEqual(pre, [(U(2024, 10, 25, 22), U(2024, 10, 26, 22))])

    def test_preimage_of_repeated_local_hour_is_disjoint(self):
        table = repeat_hour_table()
        pre = table.preimage_local_interval(U(2024, 1, 2, 8, 30), U(2024, 1, 2, 9, 30))
        self.assertEqual(
            pre,
            [(U(2024, 1, 2, 8, 30), U(2024, 1, 2, 9, 30)),
             (U(2024, 1, 2, 10, 30), U(2024, 1, 2, 11, 30))],
        )

    def test_preimage_of_full_day_can_be_disjoint(self):
        table = wild_jump_table()
        pre = table.preimage_local_interval(U(2024, 1, 2), U(2024, 1, 3))
        self.assertEqual(
            pre,
            [(U(2024, 1, 2), U(2024, 1, 2, 12)),
             (U(2024, 1, 3, 6), U(2024, 1, 4))],
        )
        total = sum(b - a for a, b in pre)
        self.assertEqual(total, 30 * 3600)

    def test_preimage_merges_touching_pieces(self):
        table = europe_like()
        pre = table.preimage_local_interval(U(2024, 10, 27), U(2024, 10, 28))
        self.assertEqual(pre, [(U(2024, 10, 26, 22), U(2024, 10, 27, 23))])
        self.assertEqual(pre[0][1] - pre[0][0], 25 * 3600)

    def test_local_revision_replaces_only_its_span(self):
        table = europe_like()
        table.revise(U(2025, 1, 1), U(2026, 1, 1), [])
        self.assertEqual(table.offset_at(U(2025, 6, 1)), 3600)
        self.assertEqual(table.offset_at(U(2024, 6, 1)), 7200)
        table.revise(U(2025, 1, 1), U(2026, 1, 1), [(U(2025, 3, 30, 1), 7200)])
        self.assertEqual(table.offset_at(U(2025, 6, 1)), 7200)
        self.assertEqual(table.offset_at(U(2025, 12, 1)), 7200)

    def test_revision_rejects_out_of_span_transition(self):
        table = europe_like()
        with self.assertRaises(ValueError):
            table.revise(U(2025, 1, 1), U(2025, 6, 1), [(U(2025, 7, 1), 0)])


if __name__ == "__main__":
    unittest.main()
