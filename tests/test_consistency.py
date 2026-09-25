import unittest

from cardian import analyze
from cardian.analyze import build_constraints
from cardian.model import parse_stats
from cardian.propagate import find_feasible
from reference.enumerator import brute_force

from helpers import CORR_QUERY, CORR_STATS, INCOMPATIBLE_STATS

ANY_QUERY = {"select": {"table": "T", "where": {"eq": ["a", 0]}}}


def _constraints_for(stats_dict):
    tables = parse_stats(stats_dict)
    cons = []
    off = 0
    for table in tables.values():
        types = table.tuple_types()
        cons.extend(build_constraints(table, types, off))
        off += len(types)
    return cons, off


class TestIncompatibleJointStatistics(unittest.TestCase):
    def test_conflict_subset_is_minimal(self):
        res = analyze(INCOMPATIBLE_STATS, ANY_QUERY)
        self.assertFalse(res["consistent"])
        conflict = res["conflict"]
        self.assertTrue(conflict)

        cons, n_vars = _constraints_for(INCOMPATIBLE_STATS)
        by_name = {c.name: c for c in cons}
        chosen = [by_name[name] for name in conflict]
        # the subset as a whole is infeasible
        self.assertIsNone(find_feasible(chosen, n_vars))
        # removing ANY single member makes it consistent
        for c in chosen:
            rest = [x for x in chosen if x is not c]
            self.assertIsNotNone(
                find_feasible(rest, n_vars),
                f"removing {c.name} should restore consistency",
            )

    def test_enumerator_agrees_infeasible(self):
        ref = brute_force(INCOMPATIBLE_STATS, ANY_QUERY)
        self.assertEqual(ref["feasible"], 0)


class TestHistogramSumMismatch(unittest.TestCase):
    def test_row_count_conflict(self):
        stats = {"tables": {"T": {
            "row_count": 3,
            "columns": {"a": {"domain": [0, 1], "null_count": 0,
                              "histogram": {"0": 2}}},
        }}}
        res = analyze(stats, ANY_QUERY)
        self.assertFalse(res["consistent"])
        cons, n_vars = _constraints_for(stats)
        by_name = {c.name: c for c in cons}
        chosen = [by_name[name] for name in res["conflict"]]
        self.assertIsNone(find_feasible(chosen, n_vars))
        for c in chosen:
            rest = [x for x in chosen if x is not c]
            self.assertIsNotNone(find_feasible(rest, n_vars))


class TestConsistent(unittest.TestCase):
    def test_consistent_stats(self):
        res = analyze(CORR_STATS, CORR_QUERY)
        self.assertTrue(res["consistent"])


if __name__ == "__main__":
    unittest.main()
