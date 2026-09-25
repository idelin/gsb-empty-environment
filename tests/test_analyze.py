import json
import unittest
from pathlib import Path

from rulecov import analyze, evaluate, load_spec

SAMPLE_PATH = Path(__file__).resolve().parent.parent / "examples" / "sample_spec.json"


def load_sample():
    with open(SAMPLE_PATH, encoding="utf-8") as fh:
        return load_spec(json.load(fh))


def findings_of_kind(report, kind):
    return [f for f in report["findings"] if f["kind"] == kind]


class TestSampleAnalysis(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.spec = load_sample()
        cls.report = analyze(cls.spec)

    def test_report_is_complete(self):
        self.assertTrue(self.report["complete"])
        self.assertFalse(self.report["budget"]["exhausted"])
        self.assertEqual(set(self.report["phases"].values()), {"complete"})

    def test_unreachable_rule_detected(self):
        unreachable = findings_of_kind(self.report, "unreachable_rule")
        self.assertEqual([f["rule"]["id"] for f in unreachable], ["R3"])
        covered_by = {r["id"] for r in unreachable[0]["covered_by"]}
        self.assertIn("R1", covered_by)
        self.assertEqual(unreachable[0]["rule"]["version"], 1)
        self.assertTrue(unreachable[0]["evidence"]["steps"])

    def test_unreachable_rule_has_no_overlap_findings(self):
        overlaps = findings_of_kind(self.report, "partial_overlap")
        self.assertNotIn("R3", {f["low_rule"]["id"] for f in overlaps})

    def test_partial_overlap_counterexamples(self):
        overlaps = findings_of_kind(self.report, "partial_overlap")
        pairs = {(f["low_rule"]["id"], f["high_rule"]["id"]) for f in overlaps}
        self.assertIn(("R5", "R1"), pairs)
        self.assertIn(("R5", "R2"), pairs)
        for finding in overlaps:
            witness = finding["counterexample"]["input"]
            matched = evaluate(self.spec, witness)
            matched_ids = {r.rule_id for r in matched}
            self.assertIn(finding["low_rule"]["id"], matched_ids)
            self.assertIn(finding["high_rule"]["id"], matched_ids)
            low = self.spec.rule_map()[finding["low_rule"]["id"]]
            self.assertGreater(matched[0].priority, low.priority)

    def test_same_priority_conflict_counterexample(self):
        conflicts = findings_of_kind(self.report, "same_priority_conflict")
        self.assertEqual(len(conflicts), 1)
        conflict = conflicts[0]
        self.assertEqual({r["id"] for r in conflict["rules"]}, {"R6", "R7"})
        self.assertEqual(conflict["priority"], 50)
        witness = conflict["counterexample"]["input"]
        matched = evaluate(self.spec, witness)
        top = [r for r in matched if r.priority == matched[0].priority]
        self.assertEqual({r.rule_id for r in top}, {"R6", "R7"})
        self.assertEqual(len({r.action for r in top}), 2)

    def test_step_ids_are_stable_and_sequential(self):
        ids = [s["step_id"] for s in self.report["steps"]]
        self.assertEqual(ids, [f"S{i:04d}" for i in range(1, len(ids) + 1)])

    def test_deterministic_across_runs(self):
        self.assertEqual(self.report, analyze(self.spec))


class TestNoFalsePositives(unittest.TestCase):
    def test_same_priority_same_action_is_not_a_conflict(self):
        doc = {
            "spec_id": "t", "version": 1,
            "facts": [{"id": "x", "version": 1, "kind": "int", "lo": 0, "hi": 10}],
            "rules": [
                {"id": "A", "version": 1, "priority": 5, "action": "go",
                 "conditions": {"x": {"int": [0, 6]}}},
                {"id": "B", "version": 1, "priority": 5, "action": "go",
                 "conditions": {"x": {"int": [4, 10]}}},
            ],
        }
        report = analyze(load_spec(doc))
        self.assertEqual(findings_of_kind(report, "same_priority_conflict"), [])
        self.assertTrue(report["complete"])

    def test_reachable_rule_is_not_marked_unreachable(self):
        doc = {
            "spec_id": "t", "version": 1,
            "facts": [{"id": "x", "version": 1, "kind": "int", "lo": 0, "hi": 10}],
            "rules": [
                {"id": "A", "version": 1, "priority": 9, "action": "hi",
                 "conditions": {"x": {"int": [0, 5]}}},
                {"id": "B", "version": 1, "priority": 1, "action": "lo",
                 "conditions": {"x": {"int": [3, 10]}}},
            ],
        }
        report = analyze(load_spec(doc))
        self.assertEqual(findings_of_kind(report, "unreachable_rule"), [])
        overlaps = findings_of_kind(report, "partial_overlap")
        self.assertEqual(len(overlaps), 1)
        self.assertEqual(overlaps[0]["overlap_region"], {"x": {"int": [3, 5]}})
        self.assertEqual(overlaps[0]["counterexample"]["input"], {"x": 3})


class TestBudgetExhaustion(unittest.TestCase):
    def test_tiny_budget_marks_phases_unknown(self):
        spec = load_sample()
        report = analyze(spec, budget_limit=5)
        self.assertFalse(report["complete"])
        self.assertTrue(report["budget"]["exhausted"])
        self.assertEqual(report["phases"]["conflict"], "unknown")

    def test_budget_exhaustion_keeps_partial_findings_honest(self):
        spec = load_sample()
        report = analyze(spec, budget_limit=5)
        self.assertFalse(report["complete"])
        self.assertLessEqual(report["budget"]["used"], 5)

    def test_zero_budget(self):
        spec = load_sample()
        report = analyze(spec, budget_limit=0)
        self.assertEqual(set(report["phases"].values()), {"unknown"})
        self.assertEqual(report["findings"], [])
        self.assertFalse(report["complete"])


class TestSpecValidation(unittest.TestCase):
    def test_enum_value_outside_domain_rejected(self):
        doc = {
            "spec_id": "t", "version": 1,
            "facts": [{"id": "t", "version": 1, "kind": "enum", "values": ["a"]}],
            "rules": [{"id": "R", "version": 1, "priority": 1, "action": "x",
                       "conditions": {"t": {"enum": ["b"]}}}],
        }
        with self.assertRaises(Exception):
            load_spec(doc)

    def test_interval_outside_domain_rejected(self):
        doc = {
            "spec_id": "t", "version": 1,
            "facts": [{"id": "x", "version": 1, "kind": "int", "lo": 0, "hi": 10}],
            "rules": [{"id": "R", "version": 1, "priority": 1, "action": "x",
                       "conditions": {"x": {"int": [5, 99]}}}],
        }
        with self.assertRaises(Exception):
            load_spec(doc)


if __name__ == "__main__":
    unittest.main()
