import unittest

from cardian import analyze, verify_witness
from reference.enumerator import brute_force

from helpers import (
    ALL_NULL_STATS,
    CORR_QUERY,
    CORR_STATS,
    DUP_JOIN_QUERY,
    DUP_JOIN_STATS,
    JOINT_QUERY,
    JOINT_STATS,
    NULL_JOIN_QUERY,
    NULL_JOIN_STATS,
)


def _marginals(rows, ncols, domains):
    out = []
    for ci in range(ncols):
        col = {}
        for v in domains[ci]:
            col[v] = sum(1 for r in rows if r[ci] == v)
        col[None] = sum(1 for r in rows if r[ci] is None)
        out.append(col)
    return out


class TestCorrelationWitnesses(unittest.TestCase):
    """Two tables with identical marginals but opposite correlation must be
    produced as the min/max witnesses of the same query."""

    def test_bounds_and_witnesses(self):
        res = analyze(CORR_STATS, CORR_QUERY)
        self.assertTrue(res["consistent"])
        self.assertTrue(res["exact"])
        self.assertEqual(res["min"]["lower"], 0)
        self.assertEqual(res["min"]["upper"], 0)
        self.assertEqual(res["max"]["lower"], 2)
        self.assertEqual(res["max"]["upper"], 2)

        min_rows = res["min"]["witness"]["tables"]["T"]["rows"]
        max_rows = res["max"]["witness"]["tables"]["T"]["rows"]
        domains = [(0, 1), (0, 1)]
        # identical marginal histograms
        self.assertEqual(_marginals(min_rows, 2, domains),
                         _marginals(max_rows, 2, domains))
        # opposite correlation: min-witness is anti-correlated, max-witness correlated
        self.assertEqual(sorted(map(tuple, min_rows)),
                         [(0, 1), (0, 1), (1, 0), (1, 0)])
        self.assertEqual(sorted(map(tuple, max_rows)),
                         [(0, 0), (0, 0), (1, 1), (1, 1)])
        for side in ("min", "max"):
            verdict = verify_witness(CORR_STATS, CORR_QUERY, res[side]["witness"])
            self.assertTrue(verdict["ok"], verdict["errors"])

    def test_cross_check_enumerator(self):
        res = analyze(CORR_STATS, CORR_QUERY)
        ref = brute_force(CORR_STATS, CORR_QUERY)
        self.assertEqual((res["min"]["lower"], res["max"]["upper"]),
                         (ref["min"], ref["max"]))


class TestDuplicateJoin(unittest.TestCase):
    def test_multiplicity_preserved(self):
        res = analyze(DUP_JOIN_STATS, DUP_JOIN_QUERY)
        self.assertTrue(res["exact"])
        # key 1: up to 2 filtered left rows x 2 right rows; key 2: up to 1 x 1
        self.assertEqual(res["min"]["lower"], 3)
        self.assertEqual(res["max"]["upper"], 4)
        ref = brute_force(DUP_JOIN_STATS, DUP_JOIN_QUERY)
        self.assertEqual((ref["min"], ref["max"]), (3, 4))
        for side in ("min", "max"):
            verdict = verify_witness(DUP_JOIN_STATS, DUP_JOIN_QUERY,
                                     res[side]["witness"])
            self.assertTrue(verdict["ok"], verdict["errors"])

    def test_null_keys_never_match(self):
        res = analyze(NULL_JOIN_STATS, NULL_JOIN_QUERY)
        self.assertTrue(res["exact"])
        # 2 non-null left rows with key 1, 2 right rows with key 1 -> 4;
        # the NULL left row contributes nothing.
        self.assertEqual(res["min"]["lower"], 4)
        self.assertEqual(res["max"]["upper"], 4)
        ref = brute_force(NULL_JOIN_STATS, NULL_JOIN_QUERY)
        self.assertEqual((ref["min"], ref["max"]), (4, 4))


class TestMutuallyExclusive(unittest.TestCase):
    def test_zero_rows(self):
        stats = {"tables": {"T": {
            "row_count": 4,
            "columns": {"a": {"domain": [0, 1], "null_count": 0,
                              "histogram": {"0": 2, "1": 2}}},
        }}}
        query = {"select": {"table": "T", "where": {
            "and": [{"eq": ["a", 0]}, {"eq": ["a", 1]}]}}}
        res = analyze(stats, query)
        self.assertTrue(res["exact"])
        self.assertEqual(res["min"]["lower"], 0)
        self.assertEqual(res["max"]["upper"], 0)
        ref = brute_force(stats, query)
        self.assertEqual((ref["min"], ref["max"]), (0, 0))


class TestAllNullThreeValuedLogic(unittest.TestCase):
    def test_excluded_middle_fails_with_nulls(self):
        query = {"select": {"table": "N", "where": {
            "or": [{"eq": ["n", 1]}, {"not": {"eq": ["n", 1]}}]}}}
        res = analyze(ALL_NULL_STATS, query)
        self.assertTrue(res["exact"])
        self.assertEqual(res["min"]["lower"], 0)
        self.assertEqual(res["max"]["upper"], 0)
        ref = brute_force(ALL_NULL_STATS, query)
        self.assertEqual((ref["min"], ref["max"]), (0, 0))

    def test_range_on_null_column(self):
        query = {"select": {"table": "N", "where": {"range": ["n", 1, 2]}}}
        res = analyze(ALL_NULL_STATS, query)
        self.assertEqual(res["max"]["upper"], 0)


class TestJointStatistics(unittest.TestCase):
    def test_cross_check_with_enumerator(self):
        res = analyze(JOINT_STATS, JOINT_QUERY)
        self.assertTrue(res["exact"])
        ref = brute_force(JOINT_STATS, JOINT_QUERY)
        self.assertEqual((res["min"]["lower"], res["max"]["upper"]),
                         (ref["min"], ref["max"]))
        for side in ("min", "max"):
            verdict = verify_witness(JOINT_STATS, JOINT_QUERY, res[side]["witness"])
            self.assertTrue(verdict["ok"], verdict["errors"])


class TestEstimateLabelling(unittest.TestCase):
    def test_estimate_is_not_a_bound(self):
        res = analyze(CORR_STATS, CORR_QUERY)
        est = res["estimate"]
        self.assertFalse(est["is_bound"])
        self.assertIn("independence", est["method"])
        self.assertAlmostEqual(est["value"], 1.0)


if __name__ == "__main__":
    unittest.main()
