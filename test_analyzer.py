import unittest
from dataclasses import replace

from model import EnumSet, FactSchema, IntRange, Rule, decide
from analyzer import ANALYZER_VERSION, analyze, replay


def make_schema() -> FactSchema:
    return FactSchema(
        fact_id="synthetic-fact",
        version=1,
        domains={
            "color": EnumSet(["red", "green", "blue"]),
            "n": IntRange(0, 100),
        },
    )


class ConditionTests(unittest.TestCase):
    def test_enum_intersection(self):
        a = EnumSet(["red", "green"])
        b = EnumSet(["green", "blue"])
        self.assertEqual(a.intersect(b), EnumSet(["green"]))
        self.assertIsNone(a.intersect(EnumSet(["blue"])))
        self.assertTrue(EnumSet(["red", "green", "blue"]).covers(a))
        self.assertFalse(a.covers(b))
        self.assertEqual(a.pick(), "green")

    def test_int_range_boundaries(self):
        touching = IntRange(0, 10).intersect(IntRange(10, 20))
        self.assertEqual(touching, IntRange(10, 10))
        self.assertEqual(touching.pick(), 10)
        self.assertIsNone(IntRange(0, 9).intersect(IntRange(10, 20)))
        self.assertTrue(IntRange(0, 10).covers(IntRange(2, 9)))
        self.assertFalse(IntRange(0, 10).covers(IntRange(2, 11)))
        self.assertEqual(IntRange(2, 5).size(), 4)
        self.assertFalse(IntRange(0, 5).contains(True))

    def test_empty_enum_rejected(self):
        with self.assertRaises(ValueError):
            EnumSet([])


class AnalyzerTests(unittest.TestCase):
    def setUp(self):
        self.schema = make_schema()

    def test_same_priority_conflict_counterexample(self):
        rules = [
            Rule("A", 1, 5, "x", {"color": EnumSet(["red", "green"])}),
            Rule("B", 1, 5, "y", {"color": EnumSet(["green", "blue"])}),
        ]
        report = analyze(rules, self.schema)
        conflicts = report.of_kind("conflict")
        self.assertEqual(len(conflicts), 1)
        finding = conflicts[0]
        self.assertEqual(finding.rule_refs, ("A@v1", "B@v1"))
        self.assertEqual(finding.version, ANALYZER_VERSION)
        cx = dict(finding.counterexample)
        # Counterexample is a full, in-domain fact input.
        self.assertEqual(set(cx), {"color", "n"})
        # It must lie inside the overlap and trigger both rules.
        self.assertEqual(cx["color"], "green")
        decision = decide(rules, cx)
        self.assertTrue(decision.conflict)
        self.assertTrue(replay(finding, rules, self.schema))

    def test_same_priority_same_effect_is_not_conflict(self):
        rules = [
            Rule("A", 1, 5, "x", {"color": EnumSet(["red", "green"])}),
            Rule("B", 1, 5, "x", {"color": EnumSet(["green", "blue"])}),
        ]
        self.assertEqual(analyze(rules, self.schema).of_kind("conflict"), [])

    def test_disjoint_rules_have_no_findings(self):
        rules = [
            Rule("A", 1, 5, "x", {"color": EnumSet(["red"])}),
            Rule("B", 1, 5, "y", {"color": EnumSet(["blue"])}),
        ]
        report = analyze(rules, self.schema)
        self.assertEqual(report.findings, [])
        self.assertTrue(report.complete)

    def test_fully_covered_rule_is_unreachable(self):
        rules = [
            Rule("H", 1, 9, "a", {"n": IntRange(0, 10)}),
            Rule("L", 1, 1, "b", {"n": IntRange(3, 5)}),
        ]
        report = analyze(rules, self.schema)
        unreachable = report.of_kind("unreachable")
        self.assertEqual(len(unreachable), 1)
        self.assertEqual(unreachable[0].rule_refs[0], "L@v1")
        self.assertIn(("H@v1", "L@v1"), report.coverage)
        self.assertEqual(report.of_kind("partial_shadow"), [])
        self.assertTrue(replay(unreachable[0], rules, self.schema))

    def test_partial_overlap_emits_only_overlap_counterexample(self):
        rules = [
            Rule("H", 1, 9, "a", {"n": IntRange(0, 5)}),
            Rule("L", 1, 1, "b", {"n": IntRange(3, 10)}),
        ]
        report = analyze(rules, self.schema)
        self.assertEqual(report.of_kind("unreachable"), [])
        shadows = report.of_kind("partial_shadow")
        self.assertEqual(len(shadows), 1)
        finding = shadows[0]
        self.assertEqual(finding.rule_refs, ("H@v1", "L@v1"))
        cx = dict(finding.counterexample)
        # The counterexample lies strictly in the overlap [3, 5].
        self.assertLessEqual(3, cx["n"])
        self.assertLessEqual(cx["n"], 5)
        self.assertTrue(replay(finding, rules, self.schema))

    def test_union_coverage_proves_unreachable(self):
        rules = [
            Rule("H-R", 1, 9, "r", {"color": EnumSet(["red"])}),
            Rule("H-G", 1, 9, "g", {"color": EnumSet(["green"])}),
            Rule("H-B", 1, 9, "b", {"color": EnumSet(["blue"])}),
            Rule("L", 1, 1, "z", {}),
        ]
        report = analyze(rules, self.schema)
        unreachable = report.of_kind("unreachable")
        self.assertEqual([f.rule_refs[0] for f in unreachable], ["L@v1"])
        self.assertTrue(replay(unreachable[0], rules, self.schema))

    def test_budget_exhausted_during_pair_checks_does_not_clear_conflicts(self):
        rules = [
            Rule("A", 1, 5, "x", {"n": IntRange(0, 100)}),
            Rule("B", 1, 5, "y", {"n": IntRange(0, 100)}),
            Rule("C", 1, 5, "z", {"n": IntRange(0, 100)}),
        ]
        report = analyze(rules, self.schema, max_operations=2)
        self.assertFalse(report.complete)
        self.assertIn("NOT established", report.summary())

    def test_budget_exhausted_during_enumeration_leaves_verdict_open(self):
        rules = [
            Rule("H", 1, 9, "a", {"n": IntRange(0, 10)}),
            Rule("L", 1, 1, "b", {}),
        ]
        # One pair check + 101-point space would be needed.
        report = analyze(rules, self.schema, max_operations=5)
        self.assertFalse(report.complete)
        self.assertEqual(report.of_kind("unreachable"), [])

    def test_stable_ids_across_runs(self):
        rules = [
            Rule("A", 1, 5, "x", {"color": EnumSet(["red", "green"])}),
            Rule("B", 2, 5, "y", {"color": EnumSet(["green", "blue"])}),
        ]
        first = analyze(rules, self.schema)
        second = analyze(rules, self.schema)
        self.assertEqual(
            [f.finding_id for f in first.findings],
            [f.finding_id for f in second.findings],
        )
        self.assertEqual(
            [s.step_id for s in first.steps],
            [s.step_id for s in second.steps],
        )
        self.assertEqual(first.findings[0].version, ANALYZER_VERSION)

    def test_tampered_counterexample_fails_replay(self):
        rules = [
            Rule("A", 1, 5, "x", {"color": EnumSet(["red", "green"])}),
            Rule("B", 1, 5, "y", {"color": EnumSet(["green", "blue"])}),
        ]
        finding = analyze(rules, self.schema).of_kind("conflict")[0]
        tampered = tuple(
            (k, "red" if k == "color" else v) for k, v in finding.counterexample
        )
        self.assertFalse(
            replay(replace(finding, counterexample=tampered), rules, self.schema)
        )

    def test_unknown_field_and_out_of_domain_rejected(self):
        with self.assertRaises(ValueError):
            analyze(
                [Rule("A", 1, 5, "x", {"bogus": EnumSet(["z"])})],
                self.schema,
            )
        with self.assertRaises(ValueError):
            analyze(
                [Rule("A", 1, 5, "x", {"n": IntRange(0, 101)})],
                self.schema,
            )

    def test_partial_shadow_filtered_when_rule_proven_unreachable(self):
        rules = [
            Rule("H-R", 1, 9, "r", {"color": EnumSet(["red"])}),
            Rule("H-G", 1, 9, "g", {"color": EnumSet(["green"])}),
            Rule("L", 1, 1, "b", {"color": EnumSet(["red", "green"])}),
        ]
        report = analyze(rules, self.schema)
        self.assertEqual(len(report.of_kind("unreachable")), 1)
        self.assertEqual(report.of_kind("partial_shadow"), [])


if __name__ == "__main__":
    unittest.main(verbosity=2)
