import os
import tempfile
import unittest

from txbuffer import Abort, Applier, Change, Commit, ConflictError


def chg(txid, seqno, table="t", key=1, before=None, after=None):
    return Change(txid, seqno, table, key, before, after)


class BufferingTests(unittest.TestCase):
    def test_uncommitted_changes_are_invisible(self):
        app = Applier()
        app.handle_change(chg("T1", 1, after={"v": 1}))
        self.assertIsNone(app.get("t", 1))
        app.handle_commit(Commit("T1", 1, 2))  # 清单不完整
        self.assertIsNone(app.get("t", 1))
        self.assertEqual(app.last_commit_seq, 0)

    def test_commit_applies_all_changes_atomically(self):
        app = Applier()
        app.handle_change(chg("T1", 1, table="a", key=1, after={"x": 1}))
        app.handle_change(chg("T1", 2, table="b", key="k", after={"y": 2}))
        result = app.handle_commit(Commit("T1", 1, 2))
        self.assertEqual(result, "committed")
        self.assertEqual(app.get("a", 1), {"x": 1})
        self.assertEqual(app.get("b", "k"), {"y": 2})
        self.assertEqual(app.last_commit_seq, 1)

    def test_delete_then_insert_same_key_in_one_txn(self):
        app = Applier()
        app.handle_change(chg("T0", 1, key=1, after={"v": "old"}))
        app.handle_commit(Commit("T0", 1, 1))

        app.handle_change(chg("T1", 1, key=1, before={"v": "old"}, after=None))
        app.handle_change(chg("T1", 2, key=1, before=None, after={"v": "new"}))
        app.handle_commit(Commit("T1", 2, 2))
        self.assertEqual(app.get("t", 1), {"v": "new"})

    def test_early_commit_marker_waits_for_full_list(self):
        app = Applier()
        self.assertEqual(app.handle_commit(Commit("T1", 1, 2)), "waiting")
        self.assertEqual(app.handle_change(chg("T1", 1, after={"v": 1})), "waiting")
        self.assertIsNone(app.get("t", 1))
        self.assertEqual(
            app.handle_change(chg("T1", 2, key=2, after={"v": 2})), "committed"
        )
        self.assertEqual(app.get("t", 2), {"v": 2})

    def test_intra_txn_seq_gap_keeps_txn_waiting(self):
        app = Applier()
        app.handle_change(chg("T1", 1, after={"v": 1}))
        app.handle_change(chg("T1", 3, key=3, after={"v": 3}))  # 缺序号 2
        self.assertEqual(app.handle_commit(Commit("T1", 1, 3)), "waiting")
        self.assertIsNone(app.get("t", 1))
        self.assertEqual(app.handle_change(chg("T1", 2, key=2, after={"v": 2})), "committed")
        self.assertEqual(app.table_rows("t"), {1: {"v": 1}, 2: {"v": 2}, 3: {"v": 3}})


class AbortTests(unittest.TestCase):
    def test_abort_discards_buffered_txn(self):
        app = Applier()
        app.handle_change(chg("T1", 1, after={"v": 1}))
        self.assertEqual(app.handle_abort(Abort("T1")), "aborted")
        app.handle_commit(Commit("T1", 1, 1))  # 旧事务已丢弃；这是新缓冲
        self.assertIsNone(app.get("t", 1))

    def test_abort_unknown_and_committed_txns(self):
        app = Applier()
        self.assertEqual(app.handle_abort(Abort("NX")), "unknown")
        app.handle_change(chg("T1", 1, after={"v": 1}))
        app.handle_commit(Commit("T1", 1, 1))
        self.assertEqual(app.handle_abort(Abort("T1")), "duplicate")
        self.assertEqual(app.get("t", 1), {"v": 1})

    def test_abort_txn_paused_behind_gap(self):
        app = Applier()
        app.handle_change(chg("T2", 1, after={"v": 2}))
        app.handle_commit(Commit("T2", 2, 1))  # 缺 seq=1，暂停在 ready 中
        self.assertTrue(app.paused)
        self.assertEqual(app.handle_abort(Abort("T2")), "aborted")
        self.assertFalse(app.paused)


class OrderingTests(unittest.TestCase):
    def test_commit_seq_gap_pauses_then_resumes(self):
        app = Applier()
        app.handle_change(chg("T3", 1, after={"v": 3}))
        status = app.handle_commit(Commit("T3", 3, 1))
        self.assertEqual(status, "paused")
        self.assertTrue(app.paused)
        self.assertIsNone(app.get("t", 1))

        app.handle_change(chg("T1", 1, key=10, after={"v": 1}))
        self.assertEqual(app.handle_commit(Commit("T1", 1, 1)), "committed")
        # 仍缺 2，3 号继续等待
        self.assertTrue(app.paused)
        self.assertIsNone(app.get("t", 1))

        app.handle_change(chg("T2", 1, key=20, after={"v": 2}))
        self.assertEqual(app.handle_commit(Commit("T2", 2, 1)), "committed")
        # 缺口补齐，3 号自动续上
        self.assertFalse(app.paused)
        self.assertEqual(app.last_commit_seq, 3)
        self.assertEqual(app.get("t", 1), {"v": 3})

    def test_out_of_order_commit_markers_apply_in_seq_order(self):
        app = Applier()
        for txid, cs, val in [("T2", 2, 2), ("T3", 3, 3), ("T1", 1, 1)]:
            app.handle_change(chg(txid, 1, key=cs, after={"v": val}))
        self.assertEqual(app.handle_commit(Commit("T2", 2, 1)), "paused")
        self.assertEqual(app.handle_commit(Commit("T3", 3, 1)), "paused")
        self.assertEqual(app.handle_commit(Commit("T1", 1, 1)), "committed")
        self.assertEqual(app.last_commit_seq, 3)
        self.assertEqual(app.get("t", 2), {"v": 2})
        self.assertEqual(app.get("t", 3), {"v": 3})


class DuplicateTests(unittest.TestCase):
    def test_committed_txn_events_are_idempotent(self):
        app = Applier()
        app.handle_change(chg("T1", 1, after={"v": 1}))
        app.handle_commit(Commit("T1", 1, 1))
        self.assertEqual(app.handle_change(chg("T1", 1, after={"v": 1})), "duplicate")
        self.assertEqual(app.handle_commit(Commit("T1", 1, 1)), "duplicate")
        self.assertEqual(app.last_commit_seq, 1)
        self.assertEqual(app.table_rows("t"), {1: {"v": 1}})

    def test_replayed_change_with_different_payload_rejected(self):
        app = Applier()
        app.handle_change(chg("T1", 1, after={"v": 1}))
        with self.assertRaises(ValueError):
            app.handle_change(chg("T1", 1, after={"v": 999}))

    def test_commit_marker_conflict_rejected(self):
        app = Applier()
        app.handle_change(chg("T1", 1, after={"v": 1}))
        app.handle_commit(Commit("T1", 1, 2))
        with self.assertRaises(ValueError):
            app.handle_commit(Commit("T1", 1, 1))


class AtomicityTests(unittest.TestCase):
    def test_before_value_mismatch_rolls_back_whole_txn(self):
        app = Applier()
        app.handle_change(chg("T0", 1, key=1, after={"v": "base"}))
        app.handle_commit(Commit("T0", 1, 1))

        app.handle_change(chg("T1", 1, key=1, before={"v": "base"}, after={"v": "a"}))
        # 第 2 条前值错误
        app.handle_change(chg("T1", 2, key=2, before={"v": "x"}, after={"v": "b"}))
        with self.assertRaises(ConflictError):
            app.handle_commit(Commit("T1", 2, 2))

        # 全有或全无：第 1 条也不可见，水位不前进
        self.assertEqual(app.get("t", 1), {"v": "base"})
        self.assertIsNone(app.get("t", 2))
        self.assertEqual(app.last_commit_seq, 1)

    def test_later_commit_unblocked_after_conflict_txn_aborted(self):
        app = Applier()
        app.handle_change(chg("T1", 1, key=1, before={"v": "x"}, after={"v": 1}))
        with self.assertRaises(ConflictError):
            app.handle_commit(Commit("T1", 1, 1))
        # 冲突事务仍在队头；回滚后队列可继续
        app.handle_abort(Abort("T1"))
        app.handle_change(chg("T2", 1, key=2, after={"v": 2}))
        self.assertEqual(app.handle_commit(Commit("T2", 2, 1)), "paused")


class RecoveryTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.path = os.path.join(self.tmp.name, "state.json")

    def tearDown(self):
        self.tmp.cleanup()

    def test_only_committed_data_survives_restart(self):
        app = Applier(self.path)
        app.handle_change(chg("T1", 1, key=1, after={"v": 1}))
        app.handle_commit(Commit("T1", 1, 1))
        # 半事务与因缺口暂停的事务
        app.handle_change(chg("T2", 1, key=2, after={"v": 2}))
        app.handle_commit(Commit("T2", 2, 1))
        app.handle_change(chg("T4", 1, key=4, after={"v": 4}))
        app.handle_commit(Commit("T4", 4, 1))
        self.assertEqual(app.last_commit_seq, 2)

        reopened = Applier(self.path)
        self.assertEqual(reopened.last_commit_seq, 2)
        self.assertEqual(reopened.get("t", 1), {"v": 1})
        self.assertIsNone(reopened.get("t", 4))  # 半事务恢复后不可见
        self.assertEqual(reopened.handle_commit(Commit("T1", 1, 1)), "duplicate")

    def test_integer_key_type_preserved_across_restart(self):
        app = Applier(self.path)
        app.handle_change(chg("T1", 1, key=42, after={"v": 1}))
        app.handle_commit(Commit("T1", 1, 1))
        reopened = Applier(self.path)
        self.assertEqual(reopened.get("t", 42), {"v": 1})
        self.assertIsNone(reopened.get("t", "42"))

    def test_resume_from_replayed_events_after_restart(self):
        app = Applier(self.path)
        app.handle_change(chg("T1", 1, after={"v": 1}))
        app.handle_commit(Commit("T1", 1, 1))
        reopened = Applier(self.path)
        reopened.handle_change(chg("T3", 1, key=3, after={"v": 3}))
        self.assertEqual(reopened.handle_commit(Commit("T3", 3, 1)), "paused")
        reopened.handle_change(chg("T2", 1, key=2, after={"v": 2}))
        self.assertEqual(reopened.handle_commit(Commit("T2", 2, 1)), "committed")
        self.assertEqual(reopened.last_commit_seq, 3)


if __name__ == "__main__":
    unittest.main(verbosity=2)
