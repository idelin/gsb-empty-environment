import copy
import unittest

from cardian import Analyzer, analyze, verify_witness

from helpers import CORR_QUERY, CORR_STATS


def _good_witness():
    res = analyze(CORR_STATS, CORR_QUERY)
    return res, res["max"]["witness"]


class TestWitnessVerifier(unittest.TestCase):
    def test_accepts_valid_witness(self):
        _, witness = _good_witness()
        verdict = verify_witness(CORR_STATS, CORR_QUERY, witness)
        self.assertTrue(verdict["ok"], verdict["errors"])
        self.assertEqual(verdict["query_count"], witness["claim"])

    def test_rejects_tampered_row(self):
        _, witness = _good_witness()
        bad = copy.deepcopy(witness)
        bad["tables"]["T"]["rows"][0][0] = 1  # breaks the a=0 histogram count
        verdict = verify_witness(CORR_STATS, CORR_QUERY, bad)
        self.assertFalse(verdict["ok"])
        self.assertTrue(verdict["errors"])

    def test_rejects_dropped_row(self):
        _, witness = _good_witness()
        bad = copy.deepcopy(witness)
        bad["tables"]["T"]["rows"].pop()
        verdict = verify_witness(CORR_STATS, CORR_QUERY, bad)
        self.assertFalse(verdict["ok"])

    def test_rejects_wrong_claim(self):
        _, witness = _good_witness()
        bad = copy.deepcopy(witness)
        bad["claim"] += 1
        verdict = verify_witness(CORR_STATS, CORR_QUERY, bad)
        self.assertFalse(verdict["ok"])

    def test_rejects_out_of_domain_value(self):
        _, witness = _good_witness()
        bad = copy.deepcopy(witness)
        bad["tables"]["T"]["rows"][0][1] = 7
        verdict = verify_witness(CORR_STATS, CORR_QUERY, bad)
        self.assertFalse(verdict["ok"])


class TestInvalidation(unittest.TestCase):
    def test_modified_statistics_force_revalidation(self):
        analyzer = Analyzer()
        first = analyzer.analyze(CORR_STATS, CORR_QUERY)
        self.assertEqual(first["cache"], "computed")
        self.assertEqual(first["max"]["upper"], 2)

        # identical input: cache hit is safe, the proof is still valid
        again = analyzer.analyze(CORR_STATS, CORR_QUERY)
        self.assertIn("hit", again["cache"])

        # modify ONE statistic: old proofs must not be reused
        modified = copy.deepcopy(CORR_STATS)
        modified["tables"]["T"]["columns"]["b"]["histogram"] = {"0": 1, "1": 3}
        second = analyzer.analyze(modified, CORR_QUERY)
        self.assertEqual(second["cache"], "computed")
        self.assertFalse(second["bounds_reused"])
        # new optimum: overlap of a=0 (2 rows) with b=0 (1 row) is at most 1
        self.assertEqual(second["max"]["upper"], 1)
        self.assertEqual(second["min"]["lower"], 0)
        # the old witnesses were re-validated against the new statistics and
        # must be reported invalid (their b-marginals no longer match)
        revalidated = second["previous_witnesses_revalidated"]
        self.assertTrue(revalidated)
        self.assertTrue(
            any(not r["still_valid_under_new_statistics"] for r in revalidated)
        )

    def test_still_valid_witness_is_kept_honest(self):
        analyzer = Analyzer()
        analyzer.analyze(CORR_STATS, CORR_QUERY)
        # change a statistic that does not affect the witness: bump nothing
        # about the marginals but add a compatible joint count
        modified = copy.deepcopy(CORR_STATS)
        modified["tables"]["T"]["joint_counts"] = [
            {"columns": ["a", "b"],
             "counts": {"0,0": 2, "1,1": 2}}
        ]
        res = analyzer.analyze(modified, CORR_QUERY)
        self.assertFalse(res["bounds_reused"])
        # old max-witness (0,0)x2,(1,1)x2 satisfies the new statistics
        self.assertTrue(
            any(r["still_valid_under_new_statistics"]
                for r in res["previous_witnesses_revalidated"])
        )
        # and the bounds are now tight at 2
        self.assertEqual(res["min"]["lower"], 2)
        self.assertEqual(res["max"]["upper"], 2)


if __name__ == "__main__":
    unittest.main()
