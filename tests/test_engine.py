"""Requirement-by-requirement tests for the depsys engine."""
from __future__ import annotations

import unittest

from depsys import (
    CycleError,
    DepKind,
    Engine,
    FactAdd,
    FactUpdate,
    RuleUpdate,
    StepCorrection,
)
from depsys.sample import (
    allowance_fn,
    build_engine,
    cyc1_fn,
    cyc2_fn,
    gross_fn,
    selfish_fn,
    tax_fn,
    tax_fn_v2,
)


class ImpactQueryTests(unittest.TestCase):
    def setUp(self):
        self.eng = build_engine()

    def test_direct_and_transitive_with_shortest_chain(self):
        impact = self.eng.query_impact("tax:2024", {"rate_g5"})
        self.assertEqual(set(impact.direct), {"st:tax"})
        self.assertEqual(set(impact.transitive), {"st:report", "st:audit"})
        # shortest chains
        self.assertEqual(impact.explanations["st:tax"].distance, 1)
        self.assertEqual(impact.explanations["st:report"].distance, 2)
        self.assertEqual(impact.explanations["st:audit"].distance, 3)

    def test_impact_lists_downstream_products(self):
        impact = self.eng.query_impact("tax:2024", {"rate_g5"})
        self.assertEqual(impact.products, {"pay:net", "rpt:e1", "audit:e1"})

    def test_unconsumed_field_change_affects_nobody(self):
        impact = self.eng.query_impact("emp:e1", {"name"})
        self.assertEqual(impact.explanations, {})
        self.assertEqual(impact.products, set())


class DependencyRecordTests(unittest.TestCase):
    def setUp(self):
        self.eng = build_engine()

    def edges_of(self, step_id):
        return [e for e in self.eng.edges if e.step_id == step_id]

    def test_read_branch_negative_kinds_distinguished(self):
        kinds = {(e.fact_id, e.field): e.kind for e in self.edges_of("st:gross")}
        self.assertEqual(kinds[("emp:e1", "base_salary")], DepKind.READ)
        self.assertEqual(kinds[("cfg:bonus", "enabled")], DepKind.BRANCH)
        self.assertEqual(kinds[("cfg:bonus", "pct")], DepKind.READ)
        neg = [e for e in self.edges_of("st:allowance") if e.kind is DepKind.NEGATIVE]
        self.assertEqual(len(neg), 1)
        self.assertEqual(neg[0].fact_id, "emp:e1.allowance")
        self.assertIsNone(neg[0].fact_version)

    def test_edges_carry_stable_ids_and_versions(self):
        edge = next(e for e in self.edges_of("st:tax") if e.field == "rate_g5")
        self.assertEqual(edge.step_id, "st:tax")
        self.assertEqual(edge.step_version, 1)
        self.assertEqual(edge.fact_id, "tax:2024")
        self.assertEqual(edge.fact_version, 1)

    def test_facts_rules_steps_have_stable_ids_and_versions(self):
        self.assertEqual(self.eng.facts["emp:e1"].version, 1)
        self.assertEqual(self.eng.rules["rule:payroll"], 1)
        step = self.eng.steps["st:gross"]
        self.assertEqual((step.step_id, step.version, step.rule_id, step.rule_version),
                         ("st:gross", 1, "rule:payroll", 1))


class FieldPrecisionTests(unittest.TestCase):
    def setUp(self):
        self.eng = build_engine()

    def test_old_version_consumer_not_auto_dependent_on_new_version(self):
        # emp:e1 is revised, but only `name` changed and nobody consumed it.
        report = self.eng.apply_batch([
            FactUpdate("emp:e1",
                       {"name": "Ada Lovelace", "grade": "G5", "base_salary": 10000},
                       frozenset({"name"}))
        ])
        self.assertEqual(self.eng.facts["emp:e1"].version, 2)
        self.assertEqual(report.explanations, {})
        self.assertEqual(report.revalidated, set())
        # st:tax still holds its v1 edge and was not invalidated.
        edge = next(e for e in self.eng.edges
                    if e.step_id == "st:tax" and e.fact_id == "emp:e1")
        self.assertEqual(edge.fact_version, 1)

    def test_step_not_consuming_changed_fields_is_unaffected(self):
        # rate_g6 changes; st:tax consumes only rate_g5.
        report = self.eng.apply_batch([
            FactUpdate("tax:2024", {"rate_g5": 0.20, "rate_g6": 0.26},
                       frozenset({"rate_g6"}))
        ])
        self.assertEqual(report.revalidated, set())
        self.assertEqual(report.explanations, {})


class NegativeDependencyTests(unittest.TestCase):
    def setUp(self):
        self.eng = build_engine()

    def test_adding_previously_missing_input_triggers_invalidation(self):
        self.assertEqual(self.eng.facts["pay:allowance"].fields["amount"], 0)
        report = self.eng.apply_batch([FactAdd("emp:e1.allowance", {"amount": 500})])
        self.assertIn("st:allowance", report.revalidated)
        self.assertEqual(self.eng.facts["pay:allowance"].fields["amount"], 500)
        expl = report.explanations["st:allowance"]
        self.assertEqual(expl.root.kind, "fact_added")
        self.assertEqual(expl.hops[0].kind, DepKind.NEGATIVE)
        self.assertTrue(expl.replay(self.eng))


class StepCorrectionTests(unittest.TestCase):
    def setUp(self):
        self.eng = build_engine()

    def test_correction_retracts_old_edges(self):
        self.eng.apply_batch([StepCorrection("st:tax", tax_fn_v2, new_version=2)])
        tax_edges = [e for e in self.eng.edges if e.step_id == "st:tax"]
        self.assertEqual({(e.fact_id, e.field) for e in tax_edges},
                         {("pay:gross", "amount")})
        # the retracted tax:2024 edge no longer invalidates st:tax
        impact = self.eng.query_impact("tax:2024", {"rate_g5"})
        self.assertNotIn("st:tax", impact.explanations)
        report = self.eng.apply_batch([
            FactUpdate("tax:2024", {"rate_g5": 0.21, "rate_g6": 0.25},
                       frozenset({"rate_g5"}))
        ])
        self.assertEqual(report.revalidated, set())

    def test_correction_propagates_to_downstream(self):
        report = self.eng.apply_batch([StepCorrection("st:tax", tax_fn_v2, new_version=2)])
        self.assertIn("st:tax", report.revalidated)
        self.assertIn("st:report", report.revalidated)  # net changed 8800 -> 9020.0
        self.assertEqual(self.eng.facts["pay:net"].fields["amount"], 9020.0)


class CycleTests(unittest.TestCase):
    def test_two_step_cycle_diagnosed(self):
        eng = Engine()
        eng.register_rule("rule:cyc", 1)
        eng.register_step("st:cyc1", cyc1_fn, "rule:cyc")
        eng.register_step("st:cyc2", cyc2_fn, "rule:cyc")
        with self.assertRaises(CycleError) as cm:
            eng.apply_batch([StepCorrection("st:cyc1", cyc1_fn, new_version=2)])
        chain = cm.exception.chain
        self.assertEqual(chain[0], chain[-1])
        self.assertIn("st:cyc1", chain)
        self.assertIn("st:cyc2", chain)

    def test_self_supporting_step_diagnosed(self):
        eng = Engine()
        eng.register_rule("rule:loop", 1)
        eng.register_step("st:loop", selfish_fn, "rule:loop")
        with self.assertRaises(CycleError) as cm:
            eng.apply_batch([StepCorrection("st:loop", selfish_fn, new_version=2)])
        self.assertEqual(cm.exception.chain, ["st:loop", "st:loop"])

    def test_cycle_rolls_back_edges(self):
        eng = Engine()
        eng.register_rule("rule:cyc", 1)
        eng.register_step("st:cyc1", cyc1_fn, "rule:cyc")
        eng.register_step("st:cyc2", cyc2_fn, "rule:cyc")
        edges_before = list(eng.edges)
        with self.assertRaises(CycleError):
            eng.apply_batch([StepCorrection("st:cyc1", cyc1_fn, new_version=2)])
        self.assertEqual(eng.edges, edges_before)

    def test_explanations_are_simple_paths(self):
        eng = build_engine()
        report = eng.apply_batch([
            FactUpdate("emp:e1",
                       {"name": "Ada", "grade": "G5", "base_salary": 10001},
                       frozenset({"base_salary"}))
        ])
        for expl in report.explanations.values():
            self.assertTrue(expl.is_simple)


class BatchTests(unittest.TestCase):
    def setUp(self):
        self.eng = build_engine()

    def apply_salary_batch(self):
        return self.eng.apply_batch([
            FactUpdate("emp:e1",
                       {"name": "Ada", "grade": "G5", "base_salary": 10001},
                       frozenset({"base_salary"})),
            FactUpdate("tax:2024",
                       {"rate_g5": 0.20, "rate_g6": 0.26},
                       frozenset({"rate_g6"})),
        ])

    def test_minimal_explanations_and_rescued_nodes(self):
        report = self.apply_salary_batch()
        self.assertEqual(report.revalidated, {"st:gross", "st:tax", "st:report"})
        self.assertEqual(report.rescued, {"st:audit"})
        # minimal (shortest) chains
        self.assertEqual(report.explanations["st:gross"].distance, 1)
        self.assertEqual(report.explanations["st:tax"].distance, 2)
        self.assertEqual(report.explanations["st:report"].distance, 3)
        # rescued node output really is unchanged
        self.assertEqual(self.eng.facts["audit:e1"].fields, {"bucket": 8})
        self.assertEqual(self.eng.facts["audit:e1"].version, 1)

    def test_evidence_is_replayable(self):
        report = self.apply_salary_batch()
        self.assertTrue(report.explanations)
        for expl in report.explanations.values():
            self.assertTrue(expl.replay(self.eng), expl.describe())

    def test_reference_check_no_missed_and_irrelevant_branch_count(self):
        self.apply_salary_batch()
        ref = self.eng.reference_check()
        self.assertTrue(ref.ok)
        self.assertEqual(ref.missed, frozenset())
        # st:tax's branch on emp:e1.grade: emp:e1 changed but grade did not.
        self.assertEqual(ref.irrelevant_branch_accesses, 1)

    def test_reference_check_after_correction_batch(self):
        self.eng.apply_batch([StepCorrection("st:tax", tax_fn_v2, new_version=2)])
        ref = self.eng.reference_check()
        self.assertTrue(ref.ok)


class RuleVersionTests(unittest.TestCase):
    def setUp(self):
        self.eng = build_engine()

    def test_rule_update_invalidates_non_compliant_steps(self):
        report = self.eng.apply_batch([RuleUpdate("rule:payroll", 2)])
        self.assertEqual(report.non_compliant, {"st:gross", "st:tax", "st:allowance"})
        for sid in ("st:gross", "st:tax", "st:allowance"):
            self.assertIn(sid, report.explanations)
            self.assertEqual(report.explanations[sid].root.kind, "rule_update")
            self.assertTrue(report.explanations[sid].replay(self.eng))

    def test_correction_to_new_rule_version_restores_compliance(self):
        self.eng.apply_batch([RuleUpdate("rule:payroll", 2)])
        report = self.eng.apply_batch([
            StepCorrection("st:gross", gross_fn, new_version=2, new_rule_version=2),
            StepCorrection("st:tax", tax_fn, new_version=2, new_rule_version=2),
            StepCorrection("st:allowance", allowance_fn, new_version=2,
                           new_rule_version=2),
        ])
        self.assertEqual(report.non_compliant, set())


class VersioningTests(unittest.TestCase):
    def test_versions_increment_on_change(self):
        eng = build_engine()
        eng.apply_batch([
            FactUpdate("emp:e1",
                       {"name": "Ada", "grade": "G5", "base_salary": 10001},
                       frozenset({"base_salary"}))
        ])
        self.assertEqual(eng.facts["emp:e1"].version, 2)
        self.assertEqual(eng.facts["pay:gross"].version, 2)
        # rescued downstream fact keeps its version
        self.assertEqual(eng.facts["audit:e1"].version, 1)
        eng.apply_batch([StepCorrection("st:tax", tax_fn_v2, new_version=2)])
        self.assertEqual(eng.steps["st:tax"].version, 2)


if __name__ == "__main__":
    unittest.main()
