"""Unit tests for proofpack (synthetic data only)."""

from __future__ import annotations

import copy
import json
import unittest

from proofpack.export import canonical_bytes, export_package, resign
from proofpack.model import Fact, KnowledgeBase, Rule
from proofpack.sample import GOALS, build_kb
from proofpack.search import solve
from proofpack.verify import (
    VerificationError,
    minimality_failures,
    verify_package,
)


class SearchTests(unittest.TestCase):
    def test_leaks_minimised_before_size(self):
        # The guarantor proof is smaller (11 items) but leaks one public
        # fact; the all-private proof (14 items) must win.
        result = solve(build_kb(), GOALS)
        self.assertEqual(result.status, "OPTIMAL")
        self.assertEqual(result.lower, result.upper)
        self.assertEqual(result.lower, (0, 14))
        self.assertEqual(
            set(result.plan.base_facts), {"F_BANK_ATTEST", "F_PAYSLIP"}
        )
        self.assertEqual(
            set(result.plan.rules),
            {"R_CREDIT_ATTEST", "R_INCOME_PAYSLIP", "R_ELIG_STD", "R_PREMIUM"},
        )

    def test_size_tiebreak_when_leaks_equal(self):
        # With a private guarantor, the guarantor proof is smaller and
        # must be chosen instead.
        kb = build_kb()
        facts = [
            Fact(f.fact_id, f.version, f.payload, public=False, evidence=f.evidence)
            for f in kb.facts.values()
        ]
        result = solve(KnowledgeBase(facts, kb.rules.values()), GOALS)
        self.assertEqual(result.lower, (0, 11))
        self.assertEqual(
            set(result.plan.base_facts), {"F_GUARANTOR", "F_BANK_ATTEST"}
        )

    def test_shared_fact_counted_once(self):
        # F_CREDIT_OK feeds both goals; it must be derived exactly once.
        result = export_package(build_kb(), GOALS)
        pkg = result.package
        self.assertEqual(pkg["summary"]["base_facts"], 2)
        self.assertEqual(pkg["summary"]["rules"], 4)
        self.assertEqual(pkg["summary"]["steps"], 4)
        self.assertEqual(pkg["summary"]["package_items"], 14)
        outputs = [s["output"] for s in pkg["steps"]]
        self.assertEqual(outputs.count("F_CREDIT_OK"), 1)

    def test_infeasible_goal(self):
        result = solve(build_kb(), ["F_UNKNOWN"])
        self.assertEqual(result.status, "INFEASIBLE")
        kb = KnowledgeBase([Fact("F_X", 1, "x", evidence=False)], [])
        self.assertEqual(solve(kb, ["F_X"]).status, "INFEASIBLE")

    def test_cyclic_rules_terminate(self):
        kb = KnowledgeBase(
            [Fact("F_A", 1, "a"), Fact("F_B", 1, "b", evidence=False)],
            [
                Rule("R_CY1", 1, ("F_B",), "F_A"),
                Rule("R_CY2", 1, ("F_A",), "F_B"),
            ],
        )
        result = solve(kb, ["F_A"])
        self.assertEqual(result.status, "OPTIMAL")
        self.assertEqual(set(result.plan.base_facts), {"F_A"})
        self.assertEqual(result.plan.rules, frozenset())

    def test_budget_exhaustion_returns_bounds(self):
        result = solve(build_kb(), GOALS, budget=0)
        self.assertEqual(result.status, "BUDGET_EXCEEDED")
        self.assertIsNone(result.plan)
        self.assertEqual(result.lower, (0, 2))
        self.assertEqual(result.upper, (1, 11))
        self.assertLessEqual(result.lower, result.upper)


class ExportTests(unittest.TestCase):
    def test_stable_bytes(self):
        first = export_package(build_kb(), GOALS)
        second = export_package(build_kb(), GOALS)
        self.assertEqual(first.data, second.data)

    def test_bytes_independent_of_kb_order(self):
        self.assertEqual(
            export_package(build_kb(), GOALS).data,
            export_package(build_kb(reverse=True), GOALS).data,
        )

    def test_canonical_bytes_sorted_and_tight(self):
        self.assertEqual(
            canonical_bytes({"b": 1, "a": {"y": 2, "x": 1}}),
            b'{"a":{"x":1,"y":2},"b":1}',
        )

    def test_ids_and_versions_present(self):
        pkg = export_package(build_kb(), GOALS).package
        for fact in pkg["facts"]:
            self.assertIsInstance(fact["id"], str)
            self.assertIsInstance(fact["version"], int)
        for rule in pkg["rules"]:
            self.assertIsInstance(rule["id"], str)
            self.assertIsInstance(rule["version"], int)
        for step in pkg["steps"]:
            self.assertRegex(step["id"], r"^S\d{3}$")
            self.assertEqual(step["version"], 1)
        attest = next(r for r in pkg["rules"] if r["id"] == "R_CREDIT_ATTEST")
        self.assertEqual(attest["version"], 2)


class VerifyTests(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.export = export_package(build_kb(), GOALS)
        cls.pkg = cls.export.package

    def test_roundtrip_from_bytes(self):
        report = verify_package(json.loads(self.export.data))
        self.assertTrue(report["ok"])
        self.assertEqual(report["goals"], sorted(GOALS))
        self.assertEqual(report["package_items"], 14)

    def test_minimality_every_item_is_necessary(self):
        self.assertEqual(minimality_failures(self.pkg), [])

    def test_tampered_payload_detected(self):
        mutant = copy.deepcopy(self.pkg)
        mutant["facts"][0]["payload"] = "forged"
        with self.assertRaises(VerificationError):
            verify_package(mutant)

    def test_tampered_rule_version_detected(self):
        mutant = resign({**self.pkg, "rules": [
            {**r, "version": r["version"] + 1} if r["id"] == "R_PREMIUM" else r
            for r in self.pkg["rules"]
        ]})
        with self.assertRaises(VerificationError):
            verify_package(mutant)

    def test_dangling_premise_detected(self):
        mutant = copy.deepcopy(self.pkg)
        mutant["rules"][0]["premises"] = ["F_GHOST"]
        with self.assertRaises(VerificationError):
            verify_package(resign(mutant))

    def test_proven_markers_are_not_trusted(self):
        # A package that *claims* its goals are proven but cannot be
        # replayed must be rejected.
        mutant = copy.deepcopy(self.pkg)
        mutant["proven"] = True
        mutant["steps"] = [
            {
                "id": "S001",
                "version": 1,
                "rule": "R_ELIG_STD",
                "rule_version": 1,
                "inputs": [],
                "output": "F_ELIGIBLE",
            }
        ]
        with self.assertRaises(VerificationError):
            verify_package(resign(mutant))

    def test_irrelevant_evidence_rejected(self):
        mutant = copy.deepcopy(self.pkg)
        mutant["facts"].append(
            {
                "id": "F_KYC",
                "version": 1,
                "public": False,
                "role": "base",
                "payload": "unrelated evidence",
            }
        )
        with self.assertRaises(VerificationError):
            verify_package(resign(mutant))

    def test_wrong_format_rejected(self):
        mutant = copy.deepcopy(self.pkg)
        mutant["format"] = "proofpack/0"
        with self.assertRaises(VerificationError):
            verify_package(mutant)


if __name__ == "__main__":
    unittest.main()
