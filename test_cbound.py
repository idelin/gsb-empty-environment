"""cbound 的 unittest 测试（仅合成数据）。"""

import random
import unittest

from cbound import (
    ContributionBounder,
    Params,
    Record,
    check_l1_bound,
    clip_value,
    select_retained,
)

PARAMS = Params(max_records_per_subject=2, max_groups_per_subject=2,
                lower=-5.0, upper=5.0)


def rec(rid, sid="s1", group="g1", value=1.0):
    return Record(record_id=rid, subject_id=sid, group=group, value=value)


class TestClipping(unittest.TestCase):
    def test_negative_and_positive_clipped_to_bounds(self):
        self.assertEqual(clip_value(-9.0, -5.0, 5.0), -5.0)
        self.assertEqual(clip_value(9.0, -5.0, 5.0), 5.0)
        self.assertEqual(clip_value(0.5, -5.0, 5.0), 0.5)

    def test_evidence_marks_clipped_negative(self):
        b = ContributionBounder(PARAMS)
        b.ingest_shard([rec("r1", value=-99.0)], shard="A")
        ev = b.clipping_evidence()[0]
        self.assertTrue(ev["was_clipped"])
        self.assertEqual(ev["raw_value"], -99.0)
        self.assertEqual(ev["clipped_value"], -5.0)


class TestCaps(unittest.TestCase):
    def test_record_cap_per_subject(self):
        b = ContributionBounder(PARAMS)
        b.ingest_shard([rec(f"r{i}") for i in range(5)], shard="A")
        self.assertEqual([r.record_id for r in b.retained()], ["r0", "r1"])

    def test_group_cap_per_subject(self):
        b = ContributionBounder(PARAMS)
        b.ingest_shard(
            [rec("r1", group="g1"), rec("r2", group="g2"), rec("r3", group="g3")],
            shard="A",
        )
        kept = {r.record_id for r in b.retained()}
        self.assertEqual(kept, {"r1", "r2"})
        reasons = {e["record_id"]: e["drop_reason"] for e in b.clipping_evidence()}
        self.assertEqual(reasons["r3"], "超出分组数上限")

    def test_cross_shard_subject_still_capped(self):
        """同主体分散在两个分片，合并后仍只保留全局上限条数。"""
        left = ContributionBounder(PARAMS)
        right = ContributionBounder(PARAMS)
        left.ingest_shard([rec("r1"), rec("r2")], shard="A")
        right.ingest_shard([rec("r3"), rec("r4")], shard="B")
        merged = left.merge(right)
        self.assertEqual([r.record_id for r in merged.retained()], ["r1", "r2"])
        self.assertLessEqual(len(merged.retained()),
                             PARAMS.max_records_per_subject)


class TestDeterminism(unittest.TestCase):
    def test_record_order_irrelevant(self):
        records = [rec(f"r{i}", value=float(i)) for i in range(6)]
        outs = set()
        for seed in range(10):
            shuffled = records[:]
            random.Random(seed).shuffle(shuffled)
            b = ContributionBounder(PARAMS)
            b.ingest_shard(shuffled, shard="A")
            outs.add(tuple(r.record_id for r in b.retained()))
        self.assertEqual(len(outs), 1)

    def test_shard_assignment_and_merge_order_irrelevant(self):
        records = [rec(f"r{i}", value=float(i)) for i in range(6)]
        results = set()
        for seed in range(10):
            rng = random.Random(seed)
            shuffled = records[:]
            rng.shuffle(shuffled)
            a, b = ContributionBounder(PARAMS), ContributionBounder(PARAMS)
            a.ingest_shard(shuffled[:3], shard="A")
            b.ingest_shard(shuffled[3:], shard="B")
            m1 = a.merge(b)
            m2 = b.merge(a)
            results.add(tuple(r.record_id for r in m1.retained()))
            self.assertEqual(
                [r.record_id for r in m1.retained()],
                [r.record_id for r in m2.retained()],
            )
        self.assertEqual(len(results), 1)

    def test_select_retained_is_pure_function_of_set(self):
        records = [rec(f"r{i}") for i in range(4)]
        forward = select_retained(records, PARAMS)
        backward = select_retained(list(reversed(records)), PARAMS)
        self.assertEqual([r.record_id for r in forward],
                         [r.record_id for r in backward])


class TestDedup(unittest.TestCase):
    def test_same_id_across_shards_deduplicated(self):
        """去重 ID 不能通过换分片绕过。"""
        b = ContributionBounder(PARAMS)
        b.ingest_shard([rec("r1"), rec("r2")], shard="A")
        ev = b.ingest_shard([rec("r2"), rec("r3")], shard="B")
        self.assertEqual(ev[0]["event"], "duplicate")
        self.assertEqual(len(b._records), 3)

    def test_merge_deduplicates_and_reports(self):
        a = ContributionBounder(PARAMS)
        b = ContributionBounder(PARAMS)
        a.ingest_shard([rec("r1"), rec("r2")], shard="A")
        b.ingest_shard([rec("r2"), rec("r3")], shard="B")
        merged = a.merge(b)
        self.assertEqual(len(merged._records), 3)
        self.assertEqual(merged.audit[-1]["duplicates_removed"], 1)


class TestRetractCorrect(unittest.TestCase):
    def test_retract_backfills_candidate(self):
        """撤回保留记录后，此前因上限被丢弃的候选补回。"""
        b = ContributionBounder(PARAMS)
        b.ingest_shard([rec("r1"), rec("r2"), rec("r3")], shard="A")
        self.assertEqual([r.record_id for r in b.retained()], ["r1", "r2"])
        b.retract("r1")
        self.assertEqual([r.record_id for r in b.retained()], ["r2", "r3"])

    def test_retract_missing_logged(self):
        b = ContributionBounder(PARAMS)
        entry = b.retract("nope")
        self.assertEqual(entry["event"], "retract_miss")

    def test_correct_updates_value_and_reselects(self):
        b = ContributionBounder(PARAMS)
        b.ingest_shard([rec("r1", value=1.0), rec("r2", value=2.0)], shard="A")
        b.correct(rec("r1", value=100.0))
        ev = {e["record_id"]: e for e in b.clipping_evidence()}
        self.assertEqual(ev["r1"]["raw_value"], 100.0)
        self.assertEqual(ev["r1"]["clipped_value"], 5.0)
        self.assertEqual(b.aggregate_sum(), 7.0)

    def test_correct_missing_logged(self):
        b = ContributionBounder(PARAMS)
        self.assertEqual(b.correct(rec("nope"))["event"], "correct_miss")


class TestStateSize(unittest.TestCase):
    def test_state_size_deterministic_and_positive(self):
        a = ContributionBounder(PARAMS)
        a.ingest_shard([rec("r1"), rec("r2")], shard="A")
        b = ContributionBounder(PARAMS)
        b.ingest_shard([rec("r3")], shard="B")
        merged = a.merge(b)
        self.assertGreater(merged.state_size_bytes(), 0)
        self.assertEqual(merged.state_size_bytes(), b.merge(a).state_size_bytes())


class TestL1Enumeration(unittest.TestCase):
    def test_small_domain_l1_within_bound(self):
        """小域枚举相邻主体数据，实际 L1 不超过确定性上界。"""
        domain = {
            "s1": [rec("a1", "s1", "g1", 7.0), rec("a2", "s1", "g1", -8.0),
                   rec("a3", "s1", "g2", 3.0)],
            "s2": [rec("b1", "s2", "g1", -4.0), rec("b2", "s2", "g2", 9.0)],
            "s3": [rec("c1", "s3", "g1", 2.0)],
        }
        result = check_l1_bound(domain, PARAMS)
        self.assertTrue(result["bound_holds"])
        self.assertLessEqual(result["actual_max_l1"],
                             PARAMS.l1_sum_bound + 1e-9)
        self.assertEqual(result["deterministic_bound"], 10.0)
        self.assertGreater(result["neighboring_pairs_checked"], 0)


if __name__ == "__main__":
    unittest.main()
