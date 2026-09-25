import copy
import json
import unittest
from pathlib import Path

from rulecov import analyze, load_spec, verify_report

SAMPLE_PATH = Path(__file__).resolve().parent.parent / "examples" / "sample_spec.json"


def load_sample():
    with open(SAMPLE_PATH, encoding="utf-8") as fh:
        return load_spec(json.load(fh))


class TestVerifyReport(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.spec = load_sample()
        cls.report = analyze(cls.spec)

    def test_sample_report_replays_cleanly(self):
        self.assertEqual(verify_report(self.spec, self.report), [])

    def test_tampered_witness_is_detected(self):
        tampered = copy.deepcopy(self.report)
        conflict = next(f for f in tampered["findings"]
                        if f["kind"] == "same_priority_conflict")
        conflict["counterexample"]["input"]["order_total"] = 9999
        problems = verify_report(self.spec, tampered)
        self.assertTrue(problems)

    def test_tampered_step_log_is_detected(self):
        tampered = copy.deepcopy(self.report)
        tampered["steps"][0]["op"] = "intersect"
        problems = verify_report(self.spec, tampered)
        self.assertTrue(any("replay mismatch" in p for p in problems))

    def test_dropped_finding_is_detected(self):
        tampered = copy.deepcopy(self.report)
        tampered["findings"] = tampered["findings"][1:]
        problems = verify_report(self.spec, tampered)
        self.assertTrue(problems)

    def test_exhausted_report_still_replays(self):
        report = analyze(self.spec, budget_limit=5)
        self.assertFalse(report["complete"])
        self.assertEqual(verify_report(self.spec, report), [])

    def test_exhausted_report_must_not_claim_completeness(self):
        report = analyze(self.spec, budget_limit=5)
        report["complete"] = True
        problems = verify_report(self.spec, report)
        self.assertTrue(problems)


if __name__ == "__main__":
    unittest.main()
