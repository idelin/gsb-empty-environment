import inspect
import unittest

import cdc.oracle as oracle_module
from cdc import TABLES, compute_final_state, freeze_row
from cdc.events import Abort, Begin, Change, Commit, Crash, StreamValidationError


def hand_stream():
    row_a = freeze_row({"balance": 10})
    row_b = freeze_row({"balance": 20})
    return [
        Begin(0),
        Change(0, 0, "users", "u1", None, row_a),
        Change(0, 1, "users", "u1", row_a, row_b),   # 同事务同主键两次更新
        Commit(0, 0),
        Begin(1),
        Change(1, 0, "users", "u2", None, row_a),
        Abort(1),                                     # 中止事务不生效
        Begin(2),
        Change(2, 0, "users", "u1", row_b, None),     # 删除
        Commit(2, 1),
    ]


class OracleTest(unittest.TestCase):
    def test_final_state_full_recompute(self):
        state = compute_final_state(hand_stream(), TABLES)
        # u1 被 txn0 写入后又被 txn2 删除, u2 所在事务中止 -> 两表皆空
        self.assertEqual(state["users"], {})
        self.assertEqual(state["orders"], {})

    def test_aborted_txn_excluded(self):
        state = compute_final_state(hand_stream(), TABLES)
        self.assertNotIn("u2", state["users"])

    def test_duplicates_reorder_crash_are_transparent(self):
        row_a = freeze_row({"balance": 10})
        row_b = freeze_row({"balance": 20})
        stream = [
            Begin(0),
            Change(0, 1, "users", "u1", row_a, row_b),  # 乱序: seq1 先到
            Change(0, 0, "users", "u1", None, row_a),
            Change(0, 0, "users", "u1", None, row_a),   # 重复投递
            Crash(),                                    # 崩溃后整体重放
            Begin(0),
            Change(0, 0, "users", "u1", None, row_a),
            Change(0, 1, "users", "u1", row_a, row_b),
            Commit(0, 0),
            Commit(0, 0),                               # 重复 Commit
        ]
        state = compute_final_state(stream, TABLES)
        self.assertEqual(state["users"], {"u1": row_b})

    def test_invalid_stream_raises_validation_error(self):
        bad = [Begin(0), Change(0, 0, "nope", "x", None, None), Commit(0, 0)]
        with self.assertRaises(StreamValidationError):
            compute_final_state(bad, TABLES)

    def test_oracle_does_not_use_incremental_algorithm(self):
        src = inspect.getsource(oracle_module)
        self.assertNotIn("processor", src)
        self.assertNotIn("IncrementalProcessor", src)
        self.assertFalse(hasattr(oracle_module, "IncrementalProcessor"))


if __name__ == "__main__":
    unittest.main()
