"""Three-valued logic and row-value comparison semantics."""

import unittest

from minirel.tvl import row_eq, tvl_and, tvl_not, tvl_or


class TestTvl(unittest.TestCase):
    def test_and_or_not_tables(self):
        vals = [True, False, None]
        and_table = {
            (True, True): True, (True, False): False, (True, None): None,
            (False, True): False, (False, False): False, (False, None): False,
            (None, True): None, (None, False): False, (None, None): None,
        }
        or_table = {
            (True, True): True, (True, False): True, (True, None): True,
            (False, True): True, (False, False): False, (False, None): None,
            (None, True): True, (None, False): None, (None, None): None,
        }
        for a in vals:
            self.assertIs(tvl_not(a), None if a is None else not a)
            for b in vals:
                self.assertIs(tvl_and(a, b), and_table[(a, b)])
                self.assertIs(tvl_or(a, b), or_table[(a, b)])

    def test_row_eq_definitely_false_despite_null(self):
        # (1,2) vs (3,null): first component is definitely unequal,
        # so the whole row comparison is FALSE, not UNKNOWN.
        self.assertIs(row_eq((1, 2), (3, None)), False)

    def test_row_eq_unknown(self):
        # (1,2) vs (1,null): equal first component, unknown second -> UNKNOWN.
        self.assertIsNone(row_eq((1, 2), (1, None)))

    def test_row_eq_true(self):
        self.assertIs(row_eq((1, 2), (1, 2)), True)

    def test_row_eq_null_outer_component(self):
        self.assertIsNone(row_eq((None, 2), (None, 2)))
        self.assertIs(row_eq((None, 2), (None, 3)), False)


if __name__ == "__main__":
    unittest.main()
