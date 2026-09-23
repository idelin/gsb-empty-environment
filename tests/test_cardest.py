"""Unittest suite for cardest. Run: python3 -m unittest discover -s tests -v"""
import copy
import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from cardest.constraints import check_consistency, find_minimal_conflict
from cardest.engine import analyze
from cardest.estimate import point_estimate
from cardest.query import eval_predicate, sql_and, sql_not, sql_or
from cardest.reference import enumerate_exact
from cardest.search import StaleStateError
from cardest.spec import load_spec, validate_spec
from cardest.witness import verify

EXAMPLES = os.path.join(os.path.dirname(__file__), "..", "examples")


def load_example(name):
    return load_spec(os.path.join(EXAMPLES, name))


def big_spec():
    """3x3 contingency-table problem with enough nodes to exercise budgets."""
    return {
        "relations": {
            "R": {"columns": {"a": {"domain": [1, 2, 3]}, "b": {"domain": [1, 2, 3]}}}
        },
        "statistics": [
            {"id": "rows", "type": "row_count", "relation": "R", "count": 6},
            {"id": "ha", "type": "histogram", "relation": "R", "column": "a",
             "counts": {"1": 2, "2": 2, "3": 2}},
            {"id": "hb", "type": "histogram", "relation": "R", "column": "b",
             "counts": {"1": 2, "2": 2, "3": 2}},
            {"id": "na", "type": "null_count", "relation": "R", "column": "a", "count": 0},
            {"id": "nb", "type": "null_count", "relation": "R", "column": "b", "count": 0},
        ],
        "query": {"type": "select", "from": "R",
                  "where": {"op": "and", "args": [{"op": "eq", "column": "a", "value": 1},
                                                  {"op": "eq", "column": "b", "value": 1}]}},
    }


class TestThreeValuedLogic(unittest.TestCase):
    def test_and_or_not_truth_table(self):
        T, F, U = True, False, None
        self.assertIs(sql_and([T, U]), None)
        self.assertIs(sql_and([F, U]), False)
        self.assertIs(sql_and([T, T]), True)
        self.assertIs(sql_or([F, U]), None)
        self.assertIs(sql_or([T, U]), True)
        self.assertIs(sql_or([F, F]), False)
        self.assertIs(sql_not(U), None)
        self.assertIs(sql_not(T), False)

    def test_null_comparisons_are_unknown(self):
        row = {"a": None}
        self.assertIs(eval_predicate({"op": "eq", "column": "a", "value": 1},
                                     lambda c: row[c]), None)
        self.assertIs(eval_predicate({"op": "range", "column": "a", "low": 1, "high": 5},
                                     lambda c: row[c]), None)

    def test_where_keeps_only_true(self):
        # a=1 AND b=1 on a row where b is NULL -> UNKNOWN -> row not counted
        pred = {"op": "and", "args": [{"op": "eq", "column": "a", "value": 1},
                                      {"op": "eq", "column": "b", "value": 1}]}
        row = {"a": 1, "b": None}
        self.assertIsNot(eval_predicate(pred, lambda c: row[c]), True)


class TestExamples(unittest.TestCase):
    def test_correlation_opposite_with_same_marginals(self):
        pos = analyze(load_example("correlated.json"))
        neg = analyze(load_example("anti_correlated.json"))
        self.assertEqual(pos["status"], "exact")
        self.assertEqual(neg["status"], "exact")
        # identical marginal histograms, opposite correlation
        self.assertEqual((pos["bounds"]["lower"], pos["bounds"]["upper"]), (2, 2))
        self.assertEqual((neg["bounds"]["lower"], neg["bounds"]["upper"]), (0, 0))
        # without joint counts only a range is provable
        marg = analyze(load_example("marginals_only.json"))
        self.assertEqual((marg["bounds"]["lower"], marg["bounds"]["upper"]), (0, 2))

    def test_duplicate_key_join_multiplicity(self):
        res = analyze(load_example("dup_key_join.json"))
        self.assertEqual(res["status"], "exact")
        self.assertEqual((res["bounds"]["lower"], res["bounds"]["upper"]), (12, 12))

    def test_mutually_exclusive_predicates(self):
        res = analyze(load_example("exclusive_predicates.json"))
        self.assertEqual(res["status"], "exact")
        self.assertEqual((res["bounds"]["lower"], res["bounds"]["upper"]), (0, 0))

    def test_all_null_column(self):
        res = analyze(load_example("all_null.json"))
        self.assertEqual(res["status"], "exact")
        self.assertEqual((res["bounds"]["lower"], res["bounds"]["upper"]), (0, 0))

    def test_join_with_filter_range(self):
        res = analyze(load_example("join_filtered.json"))
        self.assertEqual(res["status"], "exact")
        self.assertEqual((res["bounds"]["lower"], res["bounds"]["upper"]), (1, 3))

    def test_search_counters_recorded(self):
        res = analyze(load_example("marginals_only.json"))
        self.assertIn("nodes_explored", res["search"])
        self.assertIn("nodes_pruned", res["search"])
        self.assertGreater(res["search"]["nodes_explored"], 0)


class TestInconsistency(unittest.TestCase):
    def test_minimal_conflict_subset(self):
        spec = load_example("inconsistent.json")
        res = analyze(spec)
        self.assertEqual(res["status"], "inconsistent")
        conflict = res["conflict"]
        self.assertTrue(conflict["minimal"])
        self.assertEqual(set(conflict["conflict"]), {"R_rows", "R_a_hist"})
        # the defining property: removing ANY single member is consistent
        mus = set(conflict["conflict"])
        for check in conflict["removal_checks"]:
            self.assertEqual(check["status"], "consistent")
            sub = copy.deepcopy(spec)
            sub["statistics"] = [s for s in spec["statistics"]
                                 if s["id"] in mus - {check["removed"]}]
            self.assertEqual(check_consistency(sub)["status"], "consistent")

    def test_inconsistent_joint_statistics(self):
        spec = {
            "relations": {"R": {"columns": {"a": {"domain": [1, 2]},
                                            "b": {"domain": [1, 2]}}}},
            "statistics": [
                {"id": "rows", "type": "row_count", "relation": "R", "count": 2},
                {"id": "ha", "type": "histogram", "relation": "R", "column": "a",
                 "counts": {"1": 1, "2": 1}},
                {"id": "na", "type": "null_count", "relation": "R", "column": "a", "count": 0},
                {"id": "nb", "type": "null_count", "relation": "R", "column": "b", "count": 0},
                # joint counts demand 3 rows with a=1, histogram allows only 1
                {"id": "j", "type": "joint_count", "relation": "R", "columns": ["a", "b"],
                 "counts": [{"values": [1, 1], "count": 3}]},
            ],
            "query": {"type": "select", "from": "R"},
        }
        validate_spec(spec)
        mus = find_minimal_conflict(spec)
        self.assertEqual(mus["status"], "inconsistent")
        self.assertTrue(mus["minimal"])
        self.assertIn("j", mus["conflict"])
        self.assertIn("ha", mus["conflict"])


class TestBudgetAndResume(unittest.TestCase):
    def test_budget_exhaustion_is_honest(self):
        spec = big_spec()
        full = analyze(spec)
        part = analyze(spec, node_budget=3)
        self.assertEqual(part["status"], "budget_exhausted")
        self.assertFalse(part["exact"])  # must not claim an exact answer
        # proven bounds must cover the exact answer
        self.assertLessEqual(part["bounds"]["lower"], full["bounds"]["lower"])
        self.assertGreaterEqual(part["bounds"]["upper"], full["bounds"]["upper"])
        # partial witnesses, when present, must be feasible
        for key in ("min", "max"):
            if part.get(key):
                report = verify(spec, part[key]["witness"], part[key]["value"],
                                part["spec_fingerprint"])
                self.assertTrue(report["valid"], report)

    def test_resume_reaches_same_optimum(self):
        spec = big_spec()
        one_shot = analyze(spec)
        part = analyze(spec, node_budget=3)
        self.assertEqual(part["status"], "budget_exhausted")
        resumed = analyze(spec, node_budget=100000, resume_state=part["resume_state"])
        self.assertEqual(resumed["status"], "exact")
        self.assertEqual(resumed["bounds"]["lower"], one_shot["bounds"]["lower"])
        self.assertEqual(resumed["bounds"]["upper"], one_shot["bounds"]["upper"])
        # node counters accumulate across the resume
        self.assertEqual(resumed["search"]["nodes_explored"],
                         one_shot["search"]["nodes_explored"])

    def test_stale_state_rejected(self):
        spec = big_spec()
        part = analyze(spec, node_budget=3)
        changed = copy.deepcopy(spec)
        changed["statistics"][1]["counts"]["1"] = 1  # modify one statistic
        with self.assertRaises(StaleStateError):
            analyze(changed, resume_state=part["resume_state"])


class TestWitnessVerifier(unittest.TestCase):
    def test_witnesses_verify(self):
        for name in ("correlated.json", "anti_correlated.json", "marginals_only.json",
                     "dup_key_join.json", "join_filtered.json", "all_null.json",
                     "exclusive_predicates.json"):
            spec = load_example(name)
            res = analyze(spec)
            self.assertEqual(res["status"], "exact", name)
            for key in ("min", "max"):
                report = verify(spec, res[key]["witness"], res[key]["value"],
                                res["spec_fingerprint"])
                self.assertTrue(report["valid"], f"{name}:{key} {report}")

    def test_tampered_witness_rejected(self):
        spec = load_example("correlated.json")
        res = analyze(spec)
        witness = copy.deepcopy(res["min"]["witness"])
        witness["R"][0]["count"] += 1  # break the row count
        report = verify(spec, witness, res["min"]["value"], res["spec_fingerprint"])
        self.assertFalse(report["valid"])

    def test_stale_certificate_detected(self):
        spec = load_example("correlated.json")
        res = analyze(spec)
        changed = copy.deepcopy(spec)
        changed["statistics"][0]["count"] = 5  # modify one statistic
        report = verify(changed, res["min"]["witness"], res["min"]["value"],
                        res["spec_fingerprint"])
        self.assertFalse(report["valid"])
        self.assertTrue(report.get("stale"))


class TestReferenceCrossCheck(unittest.TestCase):
    """Independent brute-force enumerator must agree with the production searcher."""

    def test_examples_match_reference(self):
        for name in ("correlated.json", "anti_correlated.json", "marginals_only.json",
                     "dup_key_join.json", "join_filtered.json", "all_null.json",
                     "exclusive_predicates.json"):
            spec = load_example(name)
            prod = analyze(spec)
            ref = enumerate_exact(spec)
            self.assertTrue(ref["consistent"], name)
            self.assertEqual(prod["bounds"]["lower"], ref["min"], name)
            self.assertEqual(prod["bounds"]["upper"], ref["max"], name)

    def test_big_spec_matches_reference(self):
        spec = big_spec()
        prod = analyze(spec)
        ref = enumerate_exact(spec)
        self.assertEqual(prod["bounds"]["lower"], ref["min"])
        self.assertEqual(prod["bounds"]["upper"], ref["max"])

    def test_reference_does_not_import_searcher(self):
        # the reference enumerator must be independent of the production
        # searcher: its source may not import the search/engine modules
        import cardest.reference as ref_mod
        src = open(ref_mod.__file__, encoding="utf-8").read()
        for forbidden in ("from .search", "from cardest.search",
                          "from .engine", "from cardest.engine",
                          "from .constraints", "from cardest.constraints"):
            self.assertNotIn(forbidden, src)
        # and none of the searcher's classes may leak into its globals
        for name in ("Searcher", "SelectObjective", "JoinObjective"):
            self.assertNotIn(name, vars(ref_mod))


class TestPointEstimates(unittest.TestCase):
    def test_estimate_is_labeled_not_a_bound(self):
        spec = load_example("correlated.json")
        est = point_estimate(spec)
        self.assertEqual(est["kind"], "heuristic_point_estimate")
        self.assertFalse(est["is_proven_bound"])
        self.assertIn("warning", est)
        # independence estimate (1.0) differs from the proven exact value (2):
        # demonstrates estimates must never be presented as bounds
        exact = analyze(spec)
        self.assertNotEqual(est["value"], exact["bounds"]["lower"])

    def test_join_estimate(self):
        est = point_estimate(load_example("dup_key_join.json"))
        self.assertEqual(est["value"], 12.0)  # histogram product is exact here
        self.assertFalse(est["is_proven_bound"])


class TestDomainLimit(unittest.TestCase):
    def test_domain_too_large(self):
        spec = big_spec()
        res = analyze(spec, max_cells=4)
        self.assertEqual(res["status"], "domain_too_large")
        self.assertNotIn("bounds", res)


if __name__ == "__main__":
    unittest.main()
