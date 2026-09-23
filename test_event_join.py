"""event_join 引擎的单元测试（标准库 unittest）。"""

from __future__ import annotations

import unittest

from event_join import (
    LATE_DELETE,
    LATE_INSERT,
    STALE_DELETE,
    UNKNOWN_TABLE,
    Change,
    Op,
    ToleranceJoin,
    Watermark,
)


def mk(table, pk, key, ts, seq, before=None, after=None):
    """构造变更；未显式给镜像时按 before/after 推断 INSERT/DELETE。"""

    if before is None and after is None:
        after = {"key": key, "ts": ts}
    return Change(seq, table, pk, before, after)


def ins(seq, side, pk, key, ts, table=None):
    return mk(table or side, pk, key, ts, seq)


def dele(seq, side, pk, key, ts, table=None):
    return mk(table or side, pk, key, ts, seq,
              before={"key": key, "ts": ts}, after=None)


def upd(seq, side, pk, key1, ts1, key2, ts2, table=None):
    return Change(
        seq,
        table or side,
        pk,
        {"key": key1, "ts": ts1},
        {"key": key2, "ts": ts2},
    )


class JoinTestBase(unittest.TestCase):
    def make_join(self, tolerance=5):
        return ToleranceJoin(
            left_table="L",
            right_table="R",
            key_col="key",
            ts_col="ts",
            tolerance=tolerance,
        )


class TestBasicJoin(JoinTestBase):
    def test_match_within_tolerance_and_boundary_inclusive(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 105))  # |dt|=5 恰边界
        self.assertEqual(j.live_pairs(), ["l1|r1"])

    def test_outside_tolerance_no_match(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 106))  # |dt|=6 超界
        self.assertEqual(j.live_pairs(), [])

    def test_different_keys_never_join(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k1", 100))
        j.ingest(ins(2, "R", "r1", "k2", 100))
        self.assertEqual(j.live_pairs(), [])

    def test_zero_tolerance_requires_equal_timestamp(self):
        j = self.make_join(0)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 100))
        j.ingest(ins(3, "R", "r2", "k", 101))
        self.assertEqual(j.live_pairs(), ["l1|r1"])

    def test_match_in_both_arrival_orders(self):
        # 右先到、左后到同样连接
        j = self.make_join(5)
        j.ingest(ins(1, "R", "r1", "k", 200))
        j.ingest(ins(2, "L", "l1", "k", 203))
        self.assertEqual(j.live_pairs(), ["l1|r1"])

    def test_negative_tolerance_rejected(self):
        with self.assertRaises(ValueError):
            self.make_join(-1)


class TestOneLeftManyRight(JoinTestBase):
    def test_one_left_matches_many_rights(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 100))
        j.ingest(ins(3, "R", "r2", "k", 105))
        j.ingest(ins(4, "R", "r3", "k", 94))   # |dt|=6 超界
        j.ingest(ins(5, "R", "r4", "k", 95))   # |dt|=5 恰下边界
        self.assertEqual(
            j.live_pairs(),
            ["l1|r1", "l1|r2", "l1|r4"],
        )

    def test_many_left_one_right_also_matches(self):
        j = self.make_join(5)
        j.ingest(ins(1, "R", "r1", "k", 100))
        j.ingest(ins(2, "L", "l1", "k", 100))
        j.ingest(ins(3, "L", "l2", "k", 103))
        self.assertEqual(
            j.live_pairs(), ["l1|r1", "l2|r1"]
        )


class TestDeduplication(JoinTestBase):
    def test_live_pairs_ids_unique(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 100))
        j.ingest(ins(3, "L", "l1", "k", 100))  # 重复投递
        self.assertEqual(j.live_pairs(), ["l1|r1"])
        # changelog 中只有一个 + 事件，无重复
        plus = [e for e in j.output() if e.sign == "+"]
        self.assertEqual(len(plus), 1)

    def test_changelog_toggle_dedup_with_update(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 102))
        j.ingest(upd(3, "L", "l1", "k", 100, "k", 101))
        signs = [(e.sign, e.pair_id) for e in j.output()]
        # 撤旧建新；最终仍是同一个配对 ID
        self.assertEqual(signs, [("+", "l1|r1"), ("-", "l1|r1"),
                                 ("+", "l1|r1")])
        self.assertEqual(j.live_pairs(), ["l1|r1"])

    def test_duplicate_commit_seq_is_idempotent(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 100))
        j.ingest(ins(1, "L", "OTHER", "k", 100))  # seq 重放，忽略
        j.ingest(Watermark(2, 999))               # seq 重放，忽略
        self.assertEqual(j.live_pairs(), ["l1|r1"])
        self.assertIsNone(j.watermark)

    def test_pair_not_recreated_after_finalization(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 100))
        j.ingest(Watermark(3, 105))
        self.assertEqual(j.finalized_pairs(), ["l1|r1"])
        # 同主键、同时间的"旧事件"重放：属迟到，不能重建配对
        j.ingest(ins(4, "L", "l1", "k", 100))
        plus = [e for e in j.output()
                if e.pair_id == "l1|r1" and e.sign == "+"]
        self.assertEqual(len(plus), 1)
        self.assertEqual(j.live_pairs(), ["l1|r1"])
        self.assertEqual(len(j.late), 1)
        self.assertEqual(j.late[0].reason, LATE_INSERT)


class TestRetraction(JoinTestBase):
    def test_delete_retracts_pending_pair(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 102))
        j.ingest(dele(3, "L", "l1", "k", 100))
        self.assertEqual(j.live_pairs(), [])
        self.assertNotIn("l1|r1", j.finalized_pairs())
        signs = [e.sign for e in j.output()]
        self.assertEqual(signs, ["+", "-"])

    def test_delete_one_right_leaves_other_pairs(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 100))
        j.ingest(ins(3, "R", "r2", "k", 103))
        j.ingest(dele(4, "R", "r1", "k", 100))
        self.assertEqual(j.live_pairs(), ["l1|r2"])

    def test_update_changes_key_moves_pair(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k1", 100))
        j.ingest(ins(2, "R", "ra", "k1", 100))
        j.ingest(ins(3, "R", "rb", "k2", 100))
        j.ingest(upd(4, "L", "l1", "k1", 100, "k2", 100))
        self.assertEqual(j.live_pairs(), ["l1|rb"])
        # 旧配对撤回、新配对建立
        self.assertNotIn("l1|ra", j.finalized_pairs())

    def test_update_same_key_same_ts_is_stable(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 100))
        j.ingest(upd(3, "L", "l1", "k", 100, "k", 100))
        # 显式更新保留修正轨迹 -/+，但存活配对仍唯一
        self.assertEqual(j.live_pairs(), ["l1|r1"])

    def test_delete_unknown_row_is_stale(self):
        j = self.make_join(5)
        j.ingest(dele(1, "L", "ghost", "k", 100))
        self.assertEqual(len(j.late), 1)
        self.assertEqual(j.late[0].reason, STALE_DELETE)

    def test_reinsert_after_pending_delete_relinks(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 101))
        j.ingest(dele(3, "L", "l1", "k", 100))
        j.ingest(ins(4, "L", "l1", "k", 100))
        self.assertEqual(j.live_pairs(), ["l1|r1"])
        signs = [e.sign for e in j.output()]
        self.assertEqual(signs, ["+", "-", "+"])


class TestWatermarkAndFinalization(JoinTestBase):
    def test_exact_boundary_finalizes_both_sides(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 105))
        j.ingest(Watermark(3, 109))  # max(ts)+5=110，差 1，尚未最终化
        self.assertEqual(j.finalized_pairs(), [])
        j.ingest(Watermark(4, 110))  # 恰边界最终化
        self.assertEqual(j.finalized_pairs(), ["l1|r1"])
        self.assertEqual(j.live_pairs(), ["l1|r1"])

    def test_pair_finalizes_when_second_side_closes(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 103))
        j.ingest(Watermark(3, 105))  # 左先封闭(100+5)，右需 108
        self.assertEqual(j.finalized_pairs(), [])
        j.ingest(Watermark(4, 107))  # 右仍差 1
        self.assertEqual(j.finalized_pairs(), [])
        j.ingest(Watermark(5, 108))  # 较晚一侧封闭，配对最终化
        self.assertEqual(j.finalized_pairs(), ["l1|r1"])

    def test_finalization_after_one_side_froze_first(self):
        # 两侧时间不同：左 100 先冻结时右 103 仍存活，
        # 对侧到期（wm=108）时应由兜底扫描完成最终化。
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 103))
        j.ingest(Watermark(3, 105))
        self.assertEqual(j.finalized_pairs(), [])
        j.ingest(Watermark(4, 108))
        self.assertEqual(j.finalized_pairs(), ["l1|r1"])

    def test_pending_retraction_between_watermarks(self):
        # 左已到清理边界但配对尚未最终化（对侧更晚），此时删除仍应撤回
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 103))
        j.ingest(Watermark(3, 105))  # 左已清理，配对挂起
        j.ingest(dele(4, "R", "r1", "k", 103))  # 右仍存活，撤回有效
        self.assertEqual(j.late, [])
        self.assertEqual(j.live_pairs(), [])
        self.assertEqual(j.finalized_pairs(), [])

    def test_unmatched_rows_are_cleaned_without_report(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r9", "other", 200))
        j.ingest(Watermark(3, 500))
        self.assertEqual(j.late, [])
        self.assertEqual(j.finalized_pairs(), [])
        self.assertEqual(j.live_pairs(), [])

    def test_non_monotonic_watermark_ignored(self):
        j = self.make_join(5)
        j.ingest(Watermark(1, 100))
        j.ingest(Watermark(2, 90))
        self.assertEqual(j.watermark, 100)

    def test_state_released_after_cleanup(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 100))
        j.ingest(Watermark(3, 105))
        self.assertNotIn("k", j._rows["L"])
        self.assertNotIn("k", j._rows["R"])


class TestLateAndOutOfBounds(JoinTestBase):
    def test_late_insert_is_reported_not_joined(self):
        j = self.make_join(5)
        j.ingest(Watermark(1, 100))
        j.ingest(ins(2, "R", "r1", "k", 100))  # 恰等于水位也迟到
        self.assertEqual(j.live_pairs(), [])
        self.assertEqual(len(j.late), 1)
        self.assertEqual(j.late[0].reason, LATE_INSERT)

    def test_late_delete_after_finalization_keeps_result(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 100))
        j.ingest(Watermark(3, 105))
        # 已最终化后的迟到删除
        j.ingest(dele(4, "R", "r1", "k", 100))
        self.assertEqual(j.finalized_pairs(), ["l1|r1"])
        self.assertEqual(j.live_pairs(), ["l1|r1"])
        self.assertEqual(j.late[0].reason, LATE_DELETE)
        # changelog 不出现第二次 -
        minus = [e for e in j.output() if e.sign == "-"]
        self.assertEqual(minus, [])

    def test_late_update_cannot_silently_rebuild(self):
        j = self.make_join(5)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 105))
        j.ingest(Watermark(3, 110))  # max(ts)+5=110，配对已最终化
        self.assertEqual(j.finalized_pairs(), ["l1|r1"])
        # 对已清理行的更新：前值删除报 LATE_DELETE，后值插入报 LATE_INSERT，
        # 最终化结果与 changelog 均不发生静默改变
        j.ingest(upd(4, "L", "l1", "k", 100, "k", 101))
        reasons = sorted(r.reason for r in j.late)
        self.assertEqual(reasons, [LATE_DELETE, LATE_INSERT])
        self.assertEqual(j.finalized_pairs(), ["l1|r1"])
        signs = [e.sign for e in j.output()]
        self.assertEqual(signs, ["+"])

    def test_unknown_table_reported(self):
        j = self.make_join(5)
        j.ingest(ins(1, "audit", "x1", "k", 100, table="audit"))
        self.assertEqual(j.live_pairs(), [])
        self.assertEqual(j.late[0].reason, UNKNOWN_TABLE)

    def test_on_time_delete_with_high_watermark_still_works(self):
        # 容差 > 0 时：左行尚未到 ts+tolerance，即使 wm >= ts 仍存活，
        # 此时删除应正常撤回（不能误判为迟到删除）。
        j = self.make_join(10)
        j.ingest(ins(1, "L", "l1", "k", 100))
        j.ingest(ins(2, "R", "r1", "k", 100))
        j.ingest(Watermark(3, 105))  # >= ts，但 < 110，左行仍存活
        j.ingest(dele(4, "L", "l1", "k", 100))
        self.assertEqual(j.late, [])
        self.assertEqual(j.live_pairs(), [])
        self.assertEqual([e.sign for e in j.output()], ["+", "-"])


class TestChangeAndFixtureContract(unittest.TestCase):
    def test_op_inferred_from_images(self):
        self.assertIs(
            Change(1, "t", "p", None, {"key": "k", "ts": 1}).op,
            Op.INSERT,
        )
        self.assertIs(
            Change(2, "t", "p", {"key": "k", "ts": 1}, None).op,
            Op.DELETE,
        )
        self.assertIs(
            Change(3, "t", "p",
                   {"key": "k", "ts": 1}, {"key": "k", "ts": 2}).op,
            Op.UPDATE,
        )

    def test_explicit_ops_validated(self):
        with self.assertRaises(ValueError):
            Change(1, "t", "p", {"key": "k", "ts": 1},
                   {"key": "k", "ts": 2}, op=Op.INSERT)
        with self.assertRaises(ValueError):
            Change(2, "t", "p", None, None)

    def test_update_event_ts_defaults_to_before_ts(self):
        c = Change(3, "t", "p",
                   {"key": "k", "ts": 7}, {"key": "k", "ts": 8})
        self.assertEqual(c.ts, 7)


class TestDemoFixtureEndToEnd(unittest.TestCase):
    """直接回放 demo/fixtures.py，校验整体语义与各场景一致性。"""

    def _run(self):
        from demo.fixtures import (
            EVENTS,
            KEY_COL,
            ORDERS,
            SHIPMENTS,
            TOLERANCE,
            TS_COL,
        )

        join = ToleranceJoin(
            ORDERS, SHIPMENTS,
            key_col=KEY_COL, ts_col=TS_COL, tolerance=TOLERANCE,
        )
        for item, _note in EVENTS:
            join.ingest(item)
        return join

    def test_end_to_end_outcomes(self):
        j = self._run()
        # 场景 1+3：两条边界内配对最终化；超界 S3 不连接
        self.assertEqual(
            j.finalized_pairs(), ["O1|S1", "O1|S2"]
        )
        # 场景 2 撤回的配对不复活；场景 5 更新后配对仍存活且唯一
        self.assertEqual(j.live_pairs(), ["O1|S1", "O1|S2", "O7|S7"])
        # 场景 4：四类旁路报告
        reasons = [(r.pk, r.reason) for r in j.late]
        self.assertEqual(reasons, [
            ("O_LATE", LATE_INSERT),
            ("S1", LATE_DELETE),
            ("S_GHOST", STALE_DELETE),
            ("X1", UNKNOWN_TABLE),
        ])
        # 迟到删除不产生任何 - 事件，最终化结果不被静默重建
        minus = [e for e in j.output() if e.sign == "-"]
        self.assertEqual({e.pair_id for e in minus}, {"O2|S4", "O7|S7"})
        self.assertEqual(j.watermark, 112)

    def test_pair_ids_are_unique_in_output_view(self):
        j = self._run()
        self.assertEqual(len(j.live_pairs()), len(set(j.live_pairs())))
        self.assertEqual(
            len(j.finalized_pairs()), len(set(j.finalized_pairs()))
        )


if __name__ == "__main__":
    unittest.main(verbosity=2)
