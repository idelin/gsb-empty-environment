"""Unittest suite for contribution_bounds (synthetic data only)."""

import random
import unittest

from contribution_bounds import (
    BoundConfig,
    ContributionBounder,
    Record,
    build_shard_state,
    clamp,
    enumerate_subject_l1,
    merge_shard_states,
    select_records,
)

CFG = BoundConfig(max_records_per_subject=3, max_groups_per_subject=2,
                  value_lower=-2.0, value_upper=5.0)


def rec(rid, subj="s1", group="g1", value=1.0, shard=""):
    return Record(record_id=rid, subject_id=subj, group_id=group,
                  value=value, shard_id=shard)


class TestRecordCap(unittest.TestCase):
    def test_per_subject_record_cap(self):
        records = [rec(f"r{i}", value=1.0) for i in range(10)]
        res = select_records(records, CFG)
        self.assertEqual(len(res.kept), CFG.max_records_per_subject)
        # Stable selection: smallest record_ids survive.
        self.assertEqual(res.kept_ids, ("r0", "r1", "r2"))
        dropped = [e for e in res.evidence if e.decision == "dropped"]
        self.assertTrue(all(e.reason == "record_cap" for e in dropped))
        self.assertEqual(len(dropped), 7)

    def test_group_cap(self):
        records = [rec(f"r{i}", group=f"g{i}") for i in range(5)]
        res = select_records(records, CFG)
        groups = {r.group_id for r in res.kept}
        self.assertEqual(len(groups), CFG.max_groups_per_subject)
        reasons = {e.reason for e in res.evidence}
        self.assertIn("group_cap", reasons)


class TestValueClipping(unittest.TestCase):
    def test_negative_and_positive_clipping(self):
        records = [rec("a", value=-99.0), rec("b", value=99.0),
                   rec("c", value=1.5)]
        res = select_records(records, CFG)
        values = {r.record_id: r.value for r in res.kept}
        self.assertEqual(values["a"], CFG.value_lower)
        self.assertEqual(values["b"], CFG.value_upper)
        self.assertEqual(values["c"], 1.5)
        ev = {e.record_id: e for e in res.evidence}
        self.assertTrue(ev["a"].was_clipped)
        self.assertTrue(ev["b"].was_clipped)
        self.assertFalse(ev["c"].was_clipped)
        self.assertEqual(ev["a"].raw_value, -99.0)
        self.assertEqual(ev["a"].clipped_value, CFG.value_lower)

    def test_clamp_bounds(self):
        self.assertEqual(clamp(-10.0, -2.0, 5.0), -2.0)
        self.assertEqual(clamp(10.0, -2.0, 5.0), 5.0)
        self.assertEqual(clamp(0.5, -2.0, 5.0), 0.5)


class TestStability(unittest.TestCase):
    def test_record_order_invariance(self):
        base = [rec(f"r{i}", subj=f"s{i % 3}", group=f"g{i % 4}",
                    value=float(i) - 5.0) for i in range(30)]
        expected = select_records(base, CFG).kept_ids
        rng = random.Random(1234)
        for _ in range(20):
            shuffled = base[:]
            rng.shuffle(shuffled)
            self.assertEqual(select_records(shuffled, CFG).kept_ids, expected)


class TestShardMerge(unittest.TestCase):
    def _two_shard_data(self):
        # Subject s1 has 3 records in shard A and 3 in shard B (cap is 3).
        shard_a = [rec(f"a{i}", subj="s1", value=4.0) for i in range(3)]
        shard_b = [rec(f"b{i}", subj="s1", value=4.0) for i in range(3)]
        return shard_a, shard_b

    def test_global_cap_across_shards(self):
        shard_a, shard_b = self._two_shard_data()
        states = [build_shard_state(shard_a, "A"),
                  build_shard_state(shard_b, "B")]
        report = merge_shard_states(states, CFG)
        kept_s1 = [r for r in report.result.kept if r.subject_id == "s1"]
        self.assertEqual(len(kept_s1), CFG.max_records_per_subject)

    def test_merge_commutes_with_shard_order(self):
        shard_a, shard_b = self._two_shard_data()
        states = [build_shard_state(shard_a, "A"),
                  build_shard_state(shard_b, "B")]
        fwd = merge_shard_states(states, CFG).result.kept_ids
        rev = merge_shard_states(list(reversed(states)), CFG).result.kept_ids
        self.assertEqual(fwd, rev)

    def test_merged_equals_single_shard(self):
        shard_a, shard_b = self._two_shard_data()
        states = [build_shard_state(shard_a, "A"),
                  build_shard_state(shard_b, "B")]
        merged = merge_shard_states(states, CFG).result.kept_ids
        direct = select_records(shard_a + shard_b, CFG).kept_ids
        self.assertEqual(merged, direct)

    def test_merge_state_sizes_reported(self):
        shard_a, shard_b = self._two_shard_data()
        states = [build_shard_state(shard_a, "A"),
                  build_shard_state(shard_b, "B")]
        report = merge_shard_states(states, CFG)
        self.assertEqual(len(report.shard_state_sizes), 2)
        for shard_id, size, count in report.shard_state_sizes:
            self.assertGreater(size, 0)
            self.assertEqual(count, 3)
        self.assertGreater(report.merged_state_size, 0)
        self.assertEqual(report.merged_record_count, 6)


class TestDedup(unittest.TestCase):
    def test_duplicate_id_across_shards_counts_once(self):
        dup_a = rec("dup", value=3.0)
        dup_b = rec("dup", value=3.0)
        states = [build_shard_state([dup_a], "A"),
                  build_shard_state([dup_b], "B")]
        report = merge_shard_states(states, CFG)
        self.assertEqual(len(report.result.kept), 1)
        self.assertEqual(report.merged_record_count, 1)
        reasons = {e.reason for e in report.result.evidence}
        self.assertIn("duplicate_id", reasons)

    def test_dedup_not_bypassed_by_resharding(self):
        # Same record_id placed in different shard assignments must not
        # inflate the kept count beyond one copy.
        records = [rec("x", value=2.0), rec("y", value=2.0)]
        for assignment in (("A", "A"), ("A", "B"), ("B", "A")):
            states = {}
            for r, shard in zip(records + records, assignment * 2):
                states.setdefault(shard, []).append(r)
            built = [build_shard_state(v, k) for k, v in states.items()]
            report = merge_shard_states(built, CFG)
            self.assertEqual(len(report.result.kept), 2)


class TestWithdrawCorrect(unittest.TestCase):
    def test_withdrawal_backfills_candidates(self):
        cfg = BoundConfig(max_records_per_subject=2, max_groups_per_subject=2,
                          value_lower=-2.0, value_upper=5.0)
        bounder = ContributionBounder(cfg)
        bounder.add_records([rec(f"r{i}", value=1.0) for i in range(4)])
        self.assertEqual(bounder.select().kept_ids, ("r0", "r1"))
        # Withdrawal of another subject does not matter here; instead drop
        # r0's owner entirely and confirm pool backfills from r2/r3.
        bounder.withdraw_subject("s1")
        self.assertEqual(bounder.select().kept_ids, ())

    def test_correction_backfills_candidate(self):
        cfg = BoundConfig(max_records_per_subject=2, max_groups_per_subject=1,
                          value_lower=-2.0, value_upper=5.0)
        bounder = ContributionBounder(cfg)
        bounder.add_records([
            rec("r0", group="g1", value=1.0),
            rec("r1", group="g1", value=1.0),
            rec("r2", group="g1", value=1.0),  # dropped by record cap
        ])
        self.assertEqual(bounder.select().kept_ids, ("r0", "r1"))
        # Correct r1 into group g2: the group cap now drops r1, freeing a
        # record slot that is deterministically backfilled by r2.
        bounder.correct_record("r1", group_id="g2")
        self.assertEqual(bounder.select().kept_ids, ("r0", "r2"))

    def test_correction_updates_value(self):
        bounder = ContributionBounder(CFG)
        bounder.add_records([rec("r0", value=1.0)])
        bounder.correct_record("r0", value=99.0)
        kept = bounder.select().kept
        self.assertEqual(kept[0].value, CFG.value_upper)  # clipped after fix
        with self.assertRaises(KeyError):
            bounder.correct_record("missing", value=0.0)


class TestAudit(unittest.TestCase):
    def test_audit_before_after(self):
        records = [rec(f"r{i}", value=99.0) for i in range(6)]
        res = select_records(records, CFG)
        audit = res.audit[0]
        self.assertEqual(audit.candidate_count, 6)
        self.assertEqual(audit.kept_count, 3)
        self.assertAlmostEqual(audit.raw_sum_all, 6 * 99.0)
        self.assertAlmostEqual(audit.clipped_sum_kept, 3 * CFG.value_upper)
        self.assertAlmostEqual(audit.l1_contribution, 3 * CFG.value_upper)
        self.assertTrue(audit.within_bound)
        self.assertLessEqual(audit.l1_contribution,
                             CFG.l1_bound_add_remove + 1e-9)

    def test_every_record_has_evidence(self):
        records = [rec(f"r{i}", subj=f"s{i % 2}", value=float(i))
                   for i in range(12)]
        res = select_records(records, CFG)
        self.assertEqual(len(res.evidence), 12)
        self.assertEqual({e.record_id for e in res.evidence},
                         {r.record_id for r in records})


class TestEnumeration(unittest.TestCase):
    def test_small_domain_l1_within_bounds(self):
        cfg = BoundConfig(max_records_per_subject=2, max_groups_per_subject=2,
                          value_lower=-2.0, value_upper=3.0)
        out = enumerate_subject_l1(cfg, value_domain=[-5.0, -2.0, 0.0, 3.0, 7.0],
                                   group_domain=["g1", "g2", "g3"],
                                   shard_ids=["A", "B"])
        self.assertTrue(out["add_remove_ok"])
        self.assertTrue(out["replace_ok"])
        self.assertGreater(out["datasets_enumerated"], 0)
        # Observed bounds should be tight for this domain: cap=2 records,
        # clip range [-2, 3] -> add/remove bound 2*3=6, replace bound 2*5=10.
        self.assertAlmostEqual(out["observed_l1_add_remove"], 6.0)
        self.assertAlmostEqual(out["observed_l1_replace"], 10.0)

    def test_enumeration_respects_shards(self):
        cfg = BoundConfig(max_records_per_subject=2, max_groups_per_subject=3,
                          value_lower=-1.0, value_upper=1.0)
        out = enumerate_subject_l1(cfg, value_domain=[-9.0, 9.0],
                                   group_domain=["g1"], shard_ids=["A", "B"])
        # Even when the subject's records are spread over two shards, the
        # global cap keeps the observed L1 within the bound.
        self.assertTrue(out["add_remove_ok"])
        self.assertLessEqual(out["observed_l1_add_remove"], 2.0 + 1e-9)


if __name__ == "__main__":
    unittest.main(verbosity=2)
