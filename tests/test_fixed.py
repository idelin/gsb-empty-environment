"""固定样例测试：环绕 / reset / 长缺测 / 迟到补样 / 撤回 / 同时间决胜。"""

from __future__ import annotations

import unittest

from wrapcounter import (
    CounterAnalyzer,
    DuplicatePolicy,
    Reason,
    RevisionOp,
    Sample,
    SourceConfig,
    Status,
    recompute_source,
)


def s(sid, value, t, seq=0, reset=False, ingest=0):
    return Sample(sample_id=sid, source="dev", value=value, event_time=t,
                  seq=seq, reset=reset, ingest_order=ingest)


CFG = SourceConfig(source="dev", width_bits=8, max_gap_seconds=30, config_version=1)


class WrapTests(unittest.TestCase):
    def test_250_to_3_is_definite_wrap_of_9(self):
        ordered, intervals, total = recompute_source(CFG, [s("a", 250, 0), s("b", 3, 10)])
        edge = intervals[1]
        self.assertEqual(edge.status, Status.DEFINITE)
        self.assertEqual(edge.reason, Reason.WRAP)
        self.assertEqual(edge.delta, 9)
        self.assertEqual(edge.wraps, 1)
        self.assertTrue(total.complete)
        self.assertEqual(total.definite_delta, 9)
        self.assertEqual((edge.prev_sample_id, edge.curr_sample_id), ("a", "b"))
        self.assertEqual(edge.config_version, 1)

    def test_250_to_3_long_gap_is_uncertain_not_9(self):
        # 同样的读数，间隔超过最大可判定间隔：不能再解释成增长 9。
        ordered, intervals, total = recompute_source(CFG, [s("a", 250, 0), s("b", 3, 40)])
        edge = intervals[1]
        self.assertEqual(edge.status, Status.UNCERTAIN)
        self.assertEqual(edge.reason, Reason.GAP_TOO_LARGE)
        self.assertIsNone(edge.delta)
        self.assertFalse(total.complete)
        self.assertEqual(total.definite_delta, 0)  # 不把不确定段计入总数
        self.assertEqual(total.uncertain_interval_ids, ("dev:a->b",))

    def test_wide_counter_uses_configured_modulus(self):
        cfg16 = SourceConfig(source="dev", width_bits=16, max_gap_seconds=30)
        _, intervals, total = recompute_source(cfg16, [s("a", 65530, 0), s("b", 5, 10)])
        self.assertEqual(intervals[1].reason, Reason.WRAP)
        self.assertEqual(intervals[1].delta, 11)
        self.assertEqual(total.definite_delta, 11)


class ResetTests(unittest.TestCase):
    def test_same_number_without_reset_is_zero_growth(self):
        _, intervals, total = recompute_source(CFG, [s("a", 42, 0), s("b", 42, 5)])
        edge = intervals[1]
        self.assertEqual(edge.status, Status.DEFINITE)
        self.assertEqual(edge.reason, Reason.ZERO_GROWTH)
        self.assertEqual(edge.delta, 0)
        self.assertTrue(total.complete)

    def test_same_number_with_reset_is_uncertain(self):
        # 同样的数字 42 -> 42，右端带 reset：0、一整圈 256、重置后恰好相等 不可区分。
        _, intervals, total = recompute_source(
            CFG, [s("a", 42, 0), s("b", 42, 5, reset=True)]
        )
        edge = intervals[1]
        self.assertEqual(edge.status, Status.UNCERTAIN)
        self.assertEqual(edge.reason, Reason.EQUAL_AFTER_RESET)
        self.assertIsNone(edge.delta)
        self.assertFalse(total.complete)

    def test_reset_other_value_uncertain_and_later_growth_recovers(self):
        samples = [s("a", 250, 0), s("b", 3, 5, reset=True), s("c", 20, 10)]
        _, intervals, total = recompute_source(CFG, samples)
        self.assertEqual(intervals[1].reason, Reason.RESET_CROSSING)
        self.assertIsNone(intervals[1].delta)
        self.assertEqual(intervals[2].status, Status.DEFINITE)
        self.assertEqual(intervals[2].delta, 17)  # reset 锚点之后可重新累计
        self.assertFalse(total.complete)
        self.assertEqual(total.definite_delta, 17)  # 只有 reset 之后的确定段


class BackfillTests(unittest.TestCase):
    def setUp(self):
        self.engine = CounterAnalyzer()
        self.engine.register_source(CFG)

    def test_late_insert_only_revises_adjacent_diffs_and_conserves(self):
        # 先到 250@0 与 9@30：dt 恰好 <= 30，确定环绕 delta=15。
        self.engine.upsert_sample(s("a", 250, 0, ingest=1))
        self.engine.upsert_sample(s("c", 9, 30, ingest=2))
        total0 = self.engine.source_total("dev")
        self.assertTrue(total0.complete)
        self.assertEqual(total0.definite_delta, 15)

        # 迟到补样 4@15：拆成 250->4 (=10 wrap) 与 4->9 (=5)，总和守恒。
        revs = self.engine.upsert_sample(s("b", 4, 15, ingest=3))
        ops = [(r.op, r.interval_id) for r in revs]
        self.assertIn((RevisionOp.REVOKE, "dev:a->c"), ops)
        self.assertIn((RevisionOp.UPSERT, "dev:a->b"), ops)
        self.assertIn((RevisionOp.UPSERT, "dev:b->c"), ops)
        # 远离插入点：无其它区间被修订
        self.assertEqual(len(revs), 3)
        split_revs = [r for r in revs if r.op is RevisionOp.UPSERT]
        self.assertEqual(sum(r.interval.delta for r in split_revs), 15)
        total1 = self.engine.source_total("dev")
        self.assertEqual(total1.definite_delta, 15)
        self.assertTrue(total1.complete)
        for r in split_revs:
            self.assertEqual(r.config_version, 1)
            self.assertIn(r.sample_id, ("b", "c"))  # 右新区间右端仍可追溯到 c
        upsert_ids = {r.interval_id for r in split_revs}
        self.assertEqual(upsert_ids, {"dev:a->b", "dev:b->c"})
        upsert_a_b = next(r for r in revs if r.interval_id == "dev:a->b")
        self.assertEqual(upsert_a_b.replaced_interval_id, "dev:a->c")

    def test_uncertain_coarse_cannot_be_forged_and_backfill_completes(self):
        # 长缺测：250@0 -> 3@100，粗区间不确定，definite=0 且 complete=False。
        self.engine.upsert_sample(s("a", 250, 0, ingest=1))
        self.engine.upsert_sample(s("c", 3, 100, ingest=2))
        total0 = self.engine.source_total("dev")
        self.assertFalse(total0.complete)
        self.assertEqual(total0.definite_delta, 0)

        # 中间补样：250@0 -> 4@20 (wrap=10)，4@20 -> 3@40 (wrap=255)，3@40 ->3@100 仍缺测。
        self.engine.upsert_sample(s("b1", 4, 20, ingest=3))
        total = self.engine.source_total("dev")
        self.assertFalse(total.complete)
        self.assertEqual(total.definite_delta, 10)

        self.engine.upsert_sample(s("b2", 3, 40, ingest=4))
        total = self.engine.source_total("dev")
        self.assertFalse(total.complete)
        self.assertEqual(total.definite_delta, 265)  # 10 + 255

        # 最后补上 3@100 之前的近邻 200@70：3@40->200@70=197；200@70->3@100=59(wrap)
        self.engine.upsert_sample(s("b3", 200, 70, ingest=5))
        total = self.engine.source_total("dev")
        self.assertTrue(total.complete)
        self.assertEqual(total.definite_delta, 265 + 197 + 59)

    def test_retract_revokes_adjacent_and_remerges(self):
        self.engine.upsert_sample(s("a", 10, 0))
        self.engine.upsert_sample(s("b", 20, 10))
        self.engine.upsert_sample(s("c", 25, 20))
        self.assertEqual(self.engine.source_total("dev").definite_delta, 15)

        revs = self.engine.retract_sample("b")
        revoked = [r.interval_id for r in revs if r.op is RevisionOp.REVOKE]
        self.assertEqual(set(revoked), {"dev:a->b", "dev:b->c"})
        merged = [r for r in revs if r.op is RevisionOp.UPSERT]
        self.assertEqual(len(merged), 1)
        self.assertEqual(merged[0].interval_id, "dev:a->c")
        self.assertEqual(merged[0].interval.delta, 15)  # 10->25 = 15，守恒
        self.assertEqual(self.engine.source_total("dev").definite_delta, 15)
        # 作废区间不再存在于当前视图
        cur = {iv.interval_id for iv in self.engine.current_intervals("dev")}
        self.assertNotIn("dev:a->b", cur)
        self.assertNotIn("dev:b->c", cur)
        self.assertIn("dev:a->c", cur)


class TieBreakTests(unittest.TestCase):
    def test_same_time_ordered_by_seq(self):
        # 同一毫秒：seq 小者在前。seq0=250 -> seq1=3 判定为 wrap=9；反序会是 247。
        samples = [s("hi", 3, 7.0, seq=1), s("lo", 250, 7.0, seq=0)]
        ordered, intervals, total = recompute_source(CFG, samples)
        self.assertEqual([x.sample_id for x in ordered], ["lo", "hi"])
        self.assertEqual(intervals[1].reason, Reason.WRAP)
        self.assertEqual(intervals[1].delta, 9)
        self.assertEqual(total.definite_delta, 9)

    def test_same_time_duplicate_policy(self):
        engine = CounterAnalyzer()
        engine.register_source(CFG)
        engine.upsert_sample(s("a", 1, 0))
        with self.assertRaises(ValueError):
            engine.upsert_sample(s("b", 2, 0))
        engine.upsert_sample(s("b", 2, 0), DuplicatePolicy.REPLACE)
        total = engine.source_total("dev")
        self.assertEqual(total.ordered_sample_ids, ("b",))


class ProvenanceTests(unittest.TestCase):
    def test_results_keep_sample_ids_and_config_versions(self):
        engine = CounterAnalyzer()
        engine.register_source(CFG)
        engine.upsert_sample(s("a", 250, 0))
        revs = engine.upsert_sample(s("b", 3, 10))
        r = revs[0]
        self.assertEqual(r.revision_id, 2)
        self.assertEqual(r.interval.prev_sample_id, "a")
        self.assertEqual(r.interval.curr_sample_id, "b")
        self.assertEqual(r.interval.config_version, 1)
        total = engine.source_total("dev")
        self.assertEqual(total.ordered_sample_ids, ("a", "b"))
        self.assertEqual(total.interval_ids, ("dev:a->b",))
        self.assertEqual(total.config_version, 1)

    def test_config_update_revokes_old_versions_and_rejudges(self):
        engine = CounterAnalyzer()
        engine.register_source(CFG)  # max_gap=30
        engine.upsert_sample(s("a", 250, 0))
        engine.upsert_sample(s("b", 3, 100))  # 旧配置下不确定
        self.assertFalse(engine.source_total("dev").complete)

        cfg2 = SourceConfig(source="dev", width_bits=8, max_gap_seconds=200,
                            config_version=2)
        revs = engine.update_config(cfg2)
        self.assertTrue(any(r.op is RevisionOp.REVOKE and r.config_version == 1
                            and "v1 判定作废" in r.reason for r in revs))
        edge = engine.get_interval("dev", "dev:a->b")
        self.assertEqual(edge.status, Status.DEFINITE)
        self.assertEqual(edge.delta, 9)
        self.assertEqual(edge.config_version, 2)
        self.assertEqual(engine.source_total("dev").config_version, 2)

    def test_value_out_of_range_rejected(self):
        with self.assertRaises(ValueError):
            recompute_source(CFG, [s("a", 256, 0)])


if __name__ == "__main__":
    unittest.main()
