"""causal 包的 unittest 测试：分类、最小性、版本识别、重放、无关输入排除。"""

import unittest

from causal.diff import (
    ADDED,
    REMOVED,
    SUPPORT_CHANGED,
    CAUSE_EVIDENCE_LOST,
    CAUSE_EVIDENCE_UPDATED,
    CAUSE_NEW_FACT,
    CAUSE_RULE_CHANGE,
    FACT_ADDED,
    FACT_REMOVED,
    FACT_UPDATED,
    RULE_UPDATED,
    apply_diffs,
    compare,
    root_diffs,
)
from sample_data import build_snapshots


class EngineTest(unittest.TestCase):
    def setUp(self):
        self.t1, self.t2 = build_snapshots()

    def test_derives_expected_conclusions_t1(self):
        derived = self.t1.derive().derived_ids()
        self.assertEqual(
            derived,
            {"eligible", "fee-waiver", "audit-flag", "account-ok", "newsletter"},
        )

    def test_derives_expected_conclusions_t2(self):
        derived = self.t2.derive().derived_ids()
        self.assertEqual(
            derived,
            {"priority-support", "account-ok", "newsletter"},
        )

    def test_step_ids_stable_across_runs(self):
        first = self.t1.derive()
        second = self.t1.derive()
        for fid in first.derived_ids():
            self.assertEqual(first.step_ids_for(fid), second.step_ids_for(fid))

    def test_cone_excludes_unrelated_roots(self):
        cone_roots, cone_rules = self.t1.derive().cone("fee-waiver")
        self.assertEqual(cone_roots, {"kyc-complete", "device-trusted", "txn-volume"})
        self.assertEqual({r for r, _ in cone_rules}, {"R1", "R2"})


class CompareTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.t1, cls.t2 = build_snapshots()
        cls.report = compare(cls.t1, cls.t2)
        cls.by_id = {c.conclusion_id: c for c in cls.report.changes}

    def test_new_fact_causes_new_conclusion(self):
        change = self.by_id["priority-support"]
        self.assertEqual(change.change, ADDED)
        self.assertEqual(change.causes, (CAUSE_NEW_FACT,))
        self.assertEqual(
            [(d.kind, d.target_id) for d in change.slice],
            [(FACT_ADDED, "vip-status")],
        )

    def test_evidence_retraction_causes_loss(self):
        for cid in ("eligible", "fee-waiver"):
            change = self.by_id[cid]
            self.assertEqual(change.change, REMOVED)
            self.assertEqual(change.causes, (CAUSE_EVIDENCE_LOST,))
            self.assertEqual(
                [(d.kind, d.target_id) for d in change.slice],
                [(FACT_REMOVED, "device-trusted")],
            )

    def test_rule_change_identified_by_version(self):
        change = self.by_id["audit-flag"]
        self.assertEqual(change.change, REMOVED)
        self.assertEqual(change.causes, (CAUSE_RULE_CHANGE,))
        (d,) = change.slice
        self.assertEqual((d.kind, d.target_id, d.old_version, d.new_version),
                         (RULE_UPDATED, "R6", 1, 2))

    def test_same_result_support_changed_listed_separately(self):
        change = self.by_id["account-ok"]
        self.assertEqual(change.change, SUPPORT_CHANGED)
        self.assertEqual(change.causes, (CAUSE_EVIDENCE_UPDATED,))
        self.assertEqual(
            [(d.kind, d.target_id) for d in change.slice],
            [(FACT_UPDATED, "kyc-complete")],
        )
        self.assertNotEqual(change.steps_t1, change.steps_t2)

    def test_minimality_excludes_non_causal_diff(self):
        # kyc-complete 的更新在 fee-waiver 的溯源锥内，但并非其失效原因
        change = self.by_id["fee-waiver"]
        self.assertNotIn("kyc-complete", [d.target_id for d in change.slice])

    def test_irrelevant_inputs_in_no_explanation(self):
        explained = {d.target_id for c in self.report.changes for d in c.slice}
        self.assertNotIn("promo-code", explained)
        self.assertNotIn("R10", explained)
        irrelevant = {d.target_id for d in self.report.irrelevant_diffs}
        self.assertEqual(irrelevant, {"promo-code", "R10"})

    def test_unaffected_conclusion_not_reported(self):
        self.assertNotIn("newsletter", self.by_id)

    def test_all_slices_replay(self):
        for change in self.report.changes:
            self.assertTrue(change.replay_ok, change.conclusion_id)

    def test_replay_reproduces_each_change(self):
        for change in self.report.changes:
            replayed = apply_diffs(self.t1, self.t2, change.slice).derive()
            if change.change == ADDED:
                self.assertIn(change.conclusion_id, replayed.derived_ids())
            elif change.change == REMOVED:
                self.assertNotIn(change.conclusion_id, replayed.derived_ids())
            else:
                self.assertIn(change.conclusion_id, replayed.derived_ids())
                self.assertNotEqual(
                    replayed.step_ids_for(change.conclusion_id),
                    self.t1.derive().step_ids_for(change.conclusion_id),
                )


class RootDiffTest(unittest.TestCase):
    def test_root_diffs_cover_all_input_changes(self):
        t1, t2 = build_snapshots()
        kinds = {(d.kind, d.target_id) for d in root_diffs(t1, t2)}
        self.assertEqual(kinds, {
            (FACT_REMOVED, "device-trusted"),
            (FACT_UPDATED, "kyc-complete"),
            (FACT_ADDED, "vip-status"),
            (FACT_ADDED, "promo-code"),
            (RULE_UPDATED, "R6"),
            ("rule_added", "R10"),
        })


if __name__ == "__main__":
    unittest.main()
