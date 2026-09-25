"""Unit tests for proofpack. Run: python3 -m unittest discover -s tests -v"""
from __future__ import annotations

import copy
import json
import unittest

from proofpack import (
    Fact,
    KnowledgeBase,
    Rule,
    UnprovableError,
    attach_summary,
    build_export,
    check_necessity,
    export_bytes,
    search,
    step_id_for,
    verify_package,
)
from proofpack.package import BODY_KEYS
from proofpack.sample import GOALS, build_sample_kb


def body_of(package):
    return {key: package[key] for key in BODY_KEYS}


def resigned(package):
    return attach_summary(body_of(package))


class SampleExportTests(unittest.TestCase):
    def setUp(self):
        self.kb = build_sample_kb()
        self.export = build_export(self.kb, GOALS)
        self.package = self.export.package

    def test_search_is_exact_with_tight_bounds(self):
        result = self.export.result
        self.assertEqual(result.status, "exact")
        self.assertEqual(result.lower, result.cost)
        self.assertEqual(result.upper, result.cost)

    def test_least_public_facts_then_min_size(self):
        # Private means-test path: only f-identity is public (1), even though
        # the public path via f-income-redacted would use fewer items.
        self.assertEqual(self.export.result.cost.public_facts, 1)
        rule_ids = {r["id"] for r in self.package["rules"]}
        self.assertIn("r-means-private", rule_ids)
        self.assertNotIn("r-means-public", rule_ids)

    def test_shared_facts_counted_once(self):
        # f-means-tested and its whole subproof serve both root goals.
        fact_ids = [f["id"] for f in self.package["facts"]]
        self.assertEqual(len(fact_ids), len(set(fact_ids)))
        self.assertEqual(
            set(fact_ids),
            {"f-age", "f-income", "f-employment", "f-identity",
             "f-adult", "f-means-tested", "f-eligible"},
        )
        # size = facts + rules + steps = 7 + 3 + 3
        self.assertEqual(self.export.result.cost.size, 13)

    def test_irrelevant_evidence_excluded(self):
        ids = {f["id"] for f in self.package["facts"]}
        ids |= {r["id"] for r in self.package["rules"]}
        self.assertNotIn("f-lottery-win", ids)
        self.assertNotIn("f-vip", ids)
        self.assertNotIn("r-noise", ids)

    def test_export_verifies_offline(self):
        report = verify_package(self.export.to_bytes())
        self.assertTrue(report.ok, report.errors)
        self.assertEqual(report.derived_goals, frozenset(GOALS))

    def test_every_item_is_necessary(self):
        report = check_necessity(self.export.to_bytes())
        self.assertTrue(report.ok, report.violations)

    def test_stable_ids_and_versions(self):
        entries = [*self.package["facts"], *self.package["rules"], *self.package["steps"]]
        for entry in entries:
            self.assertIsInstance(entry["id"], str)
            self.assertIsInstance(entry["version"], int)
            self.assertGreaterEqual(entry["version"], 1)
        for step in self.package["steps"]:
            self.assertEqual(step["id"], step_id_for(step["rule"]))

    def test_deterministic_bytes(self):
        self.assertEqual(self.export.to_bytes(), export_bytes(self.kb, GOALS))

    def test_insertion_order_does_not_change_bytes(self):
        kb = build_sample_kb()
        shuffled = KnowledgeBase(
            list(reversed(list(kb.facts.values()))),
            list(reversed(list(kb.rules.values()))),
        )
        self.assertEqual(export_bytes(shuffled, GOALS), self.export.to_bytes())
        self.assertEqual(export_bytes(kb, list(reversed(GOALS))), self.export.to_bytes())


class VerifierTests(unittest.TestCase):
    def setUp(self):
        self.package = build_export(build_sample_kb(), GOALS).package

    def test_digest_tampering_detected(self):
        tampered = copy.deepcopy(self.package)
        tampered["facts"][0]["statement"] = "forged statement"
        report = verify_package(tampered)
        self.assertFalse(report.ok)
        self.assertTrue(any("summary" in e for e in report.errors))

    def test_dangling_reference_detected(self):
        tampered = resigned(copy.deepcopy(self.package))
        # Point a rule premise at a fact that is not in the package.
        tampered["rules"][0]["premises"] = ["f-ghost"]
        tampered["steps"][0]["inputs"] = ["f-ghost"]
        tampered = resigned(tampered)
        report = verify_package(tampered)
        self.assertFalse(report.ok)
        self.assertTrue(any("dangling" in e for e in report.errors))

    def test_proven_claims_are_not_trusted(self):
        # Add an underivable non-base goal and re-sign: a verifier trusting
        # the package's claims would accept; re-derivation must reject.
        tampered = copy.deepcopy(self.package)
        tampered["facts"].append(
            {"id": "f-claimed", "version": 9, "statement": "forged", "public": False, "base": False}
        )
        tampered["goals"].append("f-claimed")
        tampered = resigned(tampered)
        report = verify_package(tampered)
        self.assertFalse(report.ok)
        self.assertTrue(any("f-claimed" in e for e in report.errors))

    def test_removing_any_required_premise_fails(self):
        for fact in self.package["facts"]:
            mutated = copy.deepcopy(self.package)
            mutated["facts"] = [f for f in mutated["facts"] if f["id"] != fact["id"]]
            report = verify_package(resigned(mutated))
            self.assertFalse(report.ok, f"removal of {fact['id']} should fail")

    def test_step_rule_mismatch_detected(self):
        tampered = copy.deepcopy(self.package)
        tampered["steps"][0]["output"] = "f-income"
        tampered = resigned(tampered)
        report = verify_package(tampered)
        self.assertFalse(report.ok)

    def test_garbage_input_rejected(self):
        self.assertFalse(verify_package(b"not json").ok)
        self.assertFalse(verify_package({"format": "other"}).ok)
        self.assertFalse(verify_package([1, 2, 3]).ok)


class OptimizationTests(unittest.TestCase):
    def test_public_minimised_before_size(self):
        facts = [
            Fact("pub", 1, "public leaf", public=True),
            Fact("priv1", 1, "private leaf"),
            Fact("priv2", 1, "private leaf"),
            Fact("t", 1, "target", base=False),
        ]
        rules = [
            Rule("r-small-but-public", 1, ("pub",), "t"),
            Rule("r-large-but-private", 1, ("priv1", "priv2"), "t"),
        ]
        result = search(KnowledgeBase(facts, rules), ["t"])
        self.assertEqual(result.cost.public_facts, 0)
        self.assertEqual(result.cost.size, 3 + 2)  # 3 facts + rule + step

    def test_size_breaks_public_ties(self):
        facts = [
            Fact("x", 1, "leaf"),
            Fact("y", 1, "leaf"),
            Fact("z", 1, "leaf"),
            Fact("t", 1, "target", base=False),
        ]
        rules = [
            Rule("r-small", 1, ("x",), "t"),
            Rule("r-large", 1, ("y", "z"), "t"),
        ]
        result = search(KnowledgeBase(facts, rules), ["t"])
        self.assertEqual(result.cost.size, 2 + 2)  # 2 facts + rule + step
        self.assertIn(("rule", "r-small"), result.option)

    def test_alternative_proofs_all_considered(self):
        # Two genuinely different minimal proofs for the same goal.
        facts = [
            Fact("a", 1, "leaf"),
            Fact("b", 1, "leaf"),
            Fact("t", 1, "target", base=False),
        ]
        rules = [Rule("r1", 1, ("a",), "t"), Rule("r2", 1, ("b",), "t")]
        result = search(KnowledgeBase(facts, rules), ["t"])
        self.assertEqual(result.cost.size, 4)


class BoundsTests(unittest.TestCase):
    @staticmethod
    def wide_kb(n_goals=6, n_paths=3):
        facts, rules = [], []
        for g in range(n_goals):
            gid = f"g{g}"
            facts.append(Fact(gid, 1, f"goal {g}", base=False))
            for p in range(n_paths):
                lid = f"l{g}-{p}"
                facts.append(Fact(lid, 1, f"leaf {p} for goal {g}"))
                rules.append(Rule(f"r{g}-{p}", 1, (lid,), gid))
        return KnowledgeBase(facts, rules), [f"g{g}" for g in range(n_goals)]

    def test_undecided_bounds_returned(self):
        kb, goals = self.wide_kb()
        result = search(kb, goals, budget=50)
        self.assertEqual(result.status, "undecided")
        self.assertIsNotNone(result.lower)
        self.assertIsNotNone(result.upper)
        self.assertLessEqual(result.lower.as_tuple(), result.upper.as_tuple())
        # The exact optimum (large budget) lies within the reported bounds.
        exact = search(kb, goals)
        self.assertEqual(exact.status, "exact")
        self.assertLessEqual(result.lower.as_tuple(), exact.cost.as_tuple())
        self.assertLessEqual(exact.cost.as_tuple(), result.upper.as_tuple())

    def test_undecided_export_is_still_valid(self):
        kb, goals = self.wide_kb()
        export = build_export(kb, goals, budget=50)
        self.assertEqual(export.result.status, "undecided")
        report = verify_package(export.to_bytes())
        self.assertTrue(report.ok, report.errors)
        self.assertEqual(report.derived_goals, frozenset(goals))


class GraphEdgeCaseTests(unittest.TestCase):
    def test_cycles_do_not_hang_and_are_not_used(self):
        facts = [
            Fact("a", 1, "leaf"),
            Fact("b", 1, "derived", base=False),
            Fact("c", 1, "derived", base=False),
        ]
        rules = [
            Rule("r-ab", 1, ("a",), "b"),
            Rule("r-bc", 1, ("b",), "c"),
            Rule("r-cb", 1, ("c",), "b"),  # cycle b <-> c
        ]
        kb = KnowledgeBase(facts, rules)
        self.assertFalse(kb.acyclic)
        result = search(kb, ["c"])
        self.assertEqual(result.status, "exact")
        self.assertEqual(result.cost.size, 3 + 4)  # a,b,c + 2 rules + 2 steps

    def test_unprovable_goal_raises(self):
        kb = KnowledgeBase([Fact("d", 1, "derived only", base=False)], [])
        with self.assertRaises(UnprovableError):
            search(kb, ["d"])
        with self.assertRaises(UnprovableError):
            search(kb, ["unknown"])

    def test_duplicate_ids_rejected(self):
        with self.assertRaises(ValueError):
            KnowledgeBase([Fact("x", 1, "a"), Fact("x", 1, "b")], [])
        with self.assertRaises(ValueError):
            KnowledgeBase(
                [Fact("x", 1, "a"), Fact("y", 2, "b")],
                [Rule("r", 1, ("x",), "y"), Rule("r", 1, ("y",), "x")],
            )


if __name__ == "__main__":
    unittest.main()
