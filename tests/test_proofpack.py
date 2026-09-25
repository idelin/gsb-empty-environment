import copy
import random
import unittest
from fractions import Fraction

from proofpack import (
    FilterRule, Record, aggregate, build_package, contribution_summary,
    retract_contribution, verify_package,
)
from proofpack.model import UNDEFINED


def make_records(count=32, seed=11):
    rng = random.Random(seed)
    keys = sorted(rng.randint(1, 100) for _ in range(count))
    return [
        Record(rid=f"F{i:04d}", fact_version=1, key=k,
               value=rng.randint(1, 50), weight=rng.randint(0, 5))
        for i, k in enumerate(keys)
    ]


def make_package():
    records = make_records()
    rule = FilterRule(rule_id="rule:key-range", version=1,
                      field="key", lo=20, hi=60)
    pkg = build_package(records, rule, package_id="pkg:test", version=1)
    return records, rule, pkg


class TestPackageVerification(unittest.TestCase):
    def test_fresh_package_verifies(self):
        records, rule, pkg = make_package()
        report = verify_package(pkg, expected_root=pkg["dataset"]["root"])
        self.assertTrue(report["ok"],
                        [c for c in report["checks"] if not c["ok"]])

    def test_arithmetic_matches_direct_recompute(self):
        records, rule, pkg = make_package()
        selected = [r for r in records if rule.matches(r)]
        self.assertEqual(pkg["aggregates"], aggregate(selected))
        self.assertGreater(pkg["aggregates"]["count"], 0)

    def test_weighted_average_is_exact_fraction(self):
        records = [
            Record("F1", 1, 1, 1, 1),
            Record("F2", 1, 2, 2, 1),
            Record("F3", 1, 3, 5, 2),
        ]
        rule = FilterRule("rule:key-range", 1, "key", 1, 3)
        pkg = build_package(records, rule, "pkg:exact")
        self.assertEqual(pkg["aggregates"]["sum"], 8)
        self.assertEqual(pkg["aggregates"]["count"], 3)
        self.assertEqual(pkg["aggregates"]["wavg_numerator"], 13)
        self.assertEqual(pkg["aggregates"]["wavg_denominator"], 4)
        self.assertEqual(pkg["aggregates"]["wavg"], str(Fraction(13, 4)))
        self.assertTrue(verify_package(pkg)["ok"])

    def test_mean_of_batch_means_rejected(self):
        records, rule, pkg = make_package()
        selected = [r for r in records if rule.matches(r)]
        chunks = [selected[i::4] for i in range(4)]
        means = [Fraction(sum(r.value * r.weight for r in c),
                          sum(r.weight for r in c))
                 for c in chunks if sum(r.weight for r in c)]
        wrong = str(sum(means, Fraction(0)) / len(means))
        self.assertNotEqual(wrong, pkg["aggregates"]["wavg"],
                            "sample data must expose the mean-of-means trap")
        tampered = copy.deepcopy(pkg)
        tampered["aggregates"]["wavg"] = wrong
        report = verify_package(tampered)
        self.assertFalse(report["ok"])
        self.assertFalse(self._check(report, "arithmetic"))

    def test_zero_denominator_marked_undefined(self):
        records = [
            Record("Z0", 1, 5, 10, 1),
            Record("Z1", 1, 10, 7, 0),
            Record("Z2", 1, 15, 9, 0),
            Record("Z3", 1, 20, 4, 0),
            Record("Z4", 1, 25, 6, 3),
        ]
        rule = FilterRule("rule:key-range", 1, "key", 10, 20)
        pkg = build_package(records, rule, "pkg:zero")
        self.assertEqual(pkg["aggregates"]["wavg_denominator"], 0)
        self.assertEqual(pkg["aggregates"]["wavg"], UNDEFINED)
        self.assertEqual(pkg["aggregates"]["count"], 3)
        self.assertTrue(verify_package(pkg)["ok"])

    def test_numeric_wavg_with_zero_denominator_rejected(self):
        records = [Record("Z1", 1, 10, 7, 0), Record("Z2", 1, 15, 9, 0)]
        rule = FilterRule("rule:key-range", 1, "key", 10, 20)
        pkg = build_package(records, rule, "pkg:zero2")
        tampered = copy.deepcopy(pkg)
        tampered["aggregates"]["wavg"] = "0"
        report = verify_package(tampered)
        self.assertFalse(self._check(report, "arithmetic"))

    def test_empty_selection_verifies(self):
        records = make_records()
        rule = FilterRule("rule:key-range", 1, "key", 200, 300)
        pkg = build_package(records, rule, "pkg:empty")
        self.assertEqual(pkg["aggregates"]["count"], 0)
        self.assertEqual(pkg["aggregates"]["wavg"], UNDEFINED)
        self.assertTrue(verify_package(pkg)["ok"])

    def test_omission_detected(self):
        _, _, pkg = make_package()
        tampered = copy.deepcopy(pkg)
        del tampered["evidence"]["contributions"][1]
        report = verify_package(tampered)
        self.assertFalse(report["ok"])

    def test_duplicate_detected(self):
        _, _, pkg = make_package()
        tampered = copy.deepcopy(pkg)
        dup = copy.deepcopy(tampered["evidence"]["contributions"][0])
        tampered["evidence"]["contributions"].insert(1, dup)
        report = verify_package(tampered)
        self.assertFalse(report["ok"])
        self.assertFalse(self._check(report, "no_duplicates"))

    def test_hidden_matching_record_detected(self):
        # Prover cheats: shrinks the selected range by one and presents the
        # dropped (matching) record as the right boundary.
        records, rule, pkg = make_package()
        cheat = copy.deepcopy(pkg)
        last = cheat["evidence"]["contributions"].pop()
        cheat["evidence"]["boundary_right"] = last
        report = verify_package(cheat)
        self.assertFalse(report["ok"])
        self.assertFalse(self._check(report, "ordering_and_rule"))

    def test_tampered_segment_digest_detected(self):
        _, _, pkg = make_package()
        if not pkg["evidence"]["segments"]:
            self.skipTest("no digest segments in this sample")
        tampered = copy.deepcopy(pkg)
        seg = tampered["evidence"]["segments"][0]
        seg["hash"] = ("0" if seg["hash"][0] != "0" else "1") + seg["hash"][1:]
        report = verify_package(tampered)
        self.assertFalse(report["ok"])
        self.assertFalse(self._check(report, "root_reconstruction"))

    def test_tampered_record_content_detected(self):
        _, _, pkg = make_package()
        tampered = copy.deepcopy(pkg)
        tampered["evidence"]["contributions"][0]["record"]["value"] += 1
        report = verify_package(tampered)
        self.assertFalse(report["ok"])
        self.assertFalse(self._check(report, "leaf_integrity"))

    def test_tampered_step_chain_detected(self):
        _, _, pkg = make_package()
        tampered = copy.deepcopy(pkg)
        tampered["steps"][1]["aggregates"]["sum"] += 1
        report = verify_package(tampered)
        self.assertFalse(report["ok"])
        self.assertFalse(self._check(report, "step_chain_replay"))

    @staticmethod
    def _check(report, name):
        return next(c["ok"] for c in report["checks"] if c["check"] == name)


class TestRetraction(unittest.TestCase):
    def test_retraction_updates_numerator_denominator_summary(self):
        _, _, pkg = make_package()
        victim = pkg["evidence"]["contributions"][2]["record"]
        pkg2 = retract_contribution(pkg, victim["rid"])

        self.assertEqual(pkg2["version"], pkg["version"] + 1)
        self.assertEqual(pkg2["aggregates"]["sum"],
                         pkg["aggregates"]["sum"] - victim["value"])
        self.assertEqual(pkg2["aggregates"]["count"],
                         pkg["aggregates"]["count"] - 1)
        self.assertEqual(pkg2["aggregates"]["wavg_numerator"],
                         pkg["aggregates"]["wavg_numerator"]
                         - victim["value"] * victim["weight"])
        self.assertEqual(pkg2["aggregates"]["wavg_denominator"],
                         pkg["aggregates"]["wavg_denominator"] - victim["weight"])
        self.assertNotEqual(pkg2["contribution_summary"]["digest"],
                            pkg["contribution_summary"]["digest"])
        self.assertNotEqual(pkg2["package_digest"], pkg["package_digest"])

        report = verify_package(pkg2, expected_root=pkg["dataset"]["root"],
                                previous_package=pkg)
        self.assertTrue(report["ok"],
                        [c for c in report["checks"] if not c["ok"]])

    def test_retraction_matches_direct_recompute(self):
        records, rule, pkg = make_package()
        victim_rid = pkg["evidence"]["contributions"][0]["record"]["rid"]
        pkg2 = retract_contribution(pkg, victim_rid)
        expected = [r for r in records if rule.matches(r) and r.rid != victim_rid]
        self.assertEqual(pkg2["aggregates"], aggregate(expected))

    def test_multiple_retractions(self):
        _, _, pkg = make_package()
        rids = [c["record"]["rid"] for c in pkg["evidence"]["contributions"][:2]]
        pkg2 = retract_contribution(pkg, rids[0])
        pkg3 = retract_contribution(pkg2, rids[1])
        report = verify_package(pkg3, previous_package=pkg)
        self.assertTrue(report["ok"],
                        [c for c in report["checks"] if not c["ok"]])
        self.assertEqual(pkg3["aggregates"]["count"],
                         pkg["aggregates"]["count"] - 2)

    def test_retract_unknown_rid_raises(self):
        _, _, pkg = make_package()
        with self.assertRaises(ValueError):
            retract_contribution(pkg, "NOPE")

    def test_double_retraction_raises(self):
        _, _, pkg = make_package()
        rid = pkg["evidence"]["contributions"][0]["record"]["rid"]
        pkg2 = retract_contribution(pkg, rid)
        with self.assertRaises(ValueError):
            retract_contribution(pkg2, rid)

    def test_stale_summary_after_retraction_detected(self):
        _, _, pkg = make_package()
        rid = pkg["evidence"]["contributions"][1]["record"]["rid"]
        pkg2 = retract_contribution(pkg, rid)
        tampered = copy.deepcopy(pkg2)
        tampered["contribution_summary"] = pkg["contribution_summary"]
        report = verify_package(tampered)
        self.assertFalse(report["ok"])
        self.assertFalse(
            next(c["ok"] for c in report["checks"]
                 if c["check"] == "contribution_summary"))

    def test_forged_retraction_delta_detected(self):
        _, _, pkg = make_package()
        rid = pkg["evidence"]["contributions"][1]["record"]["rid"]
        pkg2 = retract_contribution(pkg, rid)
        tampered = copy.deepcopy(pkg2)
        tampered["steps"][-1]["delta"]["wavg_numerator"] -= 1
        report = verify_package(tampered)
        self.assertFalse(report["ok"])

    def test_retraction_to_zero_denominator(self):
        records = [
            Record("A", 1, 10, 5, 3),
            Record("B", 1, 20, 8, 0),
        ]
        rule = FilterRule("rule:key-range", 1, "key", 1, 100)
        pkg = build_package(records, rule, "pkg:retract-zero")
        self.assertEqual(pkg["aggregates"]["wavg"], str(Fraction(5, 1)))
        pkg2 = retract_contribution(pkg, "A")
        self.assertEqual(pkg2["aggregates"]["wavg_denominator"], 0)
        self.assertEqual(pkg2["aggregates"]["wavg"], UNDEFINED)
        report = verify_package(pkg2, previous_package=pkg)
        self.assertTrue(report["ok"],
                        [c for c in report["checks"] if not c["ok"]])


if __name__ == "__main__":
    unittest.main()
