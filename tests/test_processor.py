import unittest

from cdc import (
    TABLES,
    IncrementalProcessor,
    ProcessorError,
    compute_final_state,
    freeze_row,
    generate,
    run_case,
)
from cdc.events import Abort, Begin, Change, Commit, Crash

ROW_A = freeze_row({"balance": 10})
ROW_B = freeze_row({"balance": 20})


class ProcessorAgainstOracleTest(unittest.TestCase):
    def test_matches_oracle_across_seeds(self):
        for seed in range(100):
            g = generate(seed=seed)
            result = run_case(g.stream, TABLES)
            self.assertEqual(
                "ok", result.status, f"seed={seed} diffs={result.diffs}"
            )

    def test_crash_recovery_with_replay(self):
        stream = [
            Begin(0),
            Change(0, 0, "users", "u1", None, ROW_A),
            Crash(),                                   # 易失状态丢失
            Begin(0),                                  # 重放整个事务
            Change(0, 0, "users", "u1", None, ROW_A),
            Change(0, 1, "users", "u1", ROW_A, ROW_B),
            Commit(0, 0),
        ]
        proc = IncrementalProcessor(TABLES)
        proc.run(stream)
        self.assertEqual(proc.state(), compute_final_state(stream, TABLES))
        self.assertEqual(proc.state()["users"], {"u1": ROW_B})

    def test_out_of_order_within_txn(self):
        stream = [
            Begin(0),
            Change(0, 1, "users", "u1", ROW_A, ROW_B),  # seq1 先到, 应缓冲
            Change(0, 0, "users", "u1", None, ROW_A),
            Commit(0, 0),
        ]
        proc = IncrementalProcessor(TABLES)
        proc.run(stream)
        self.assertEqual(proc.state()["users"], {"u1": ROW_B})


class ProcessorErrorTest(unittest.TestCase):
    """流有效但语义违规 -> ProcessorError(与 StreamValidationError 分开)。"""

    def test_commit_unknown_txn(self):
        proc = IncrementalProcessor(TABLES)
        with self.assertRaises(ProcessorError):
            proc.run([Begin(0), Commit(1, 0)])

    def test_conflicting_same_seq(self):
        proc = IncrementalProcessor(TABLES)
        with self.assertRaises(ProcessorError):
            proc.run([
                Begin(0),
                Change(0, 0, "users", "u1", None, ROW_A),
                Change(0, 0, "users", "u1", None, ROW_B),  # 同 seq 不同内容
            ])

    def test_commit_seq_reuse(self):
        proc = IncrementalProcessor(TABLES)
        with self.assertRaises(ProcessorError):
            proc.run([
                Begin(0), Commit(0, 0),
                Begin(1), Commit(1, 0),                   # commit_seq 重复
            ])

    def test_new_change_after_commit(self):
        proc = IncrementalProcessor(TABLES)
        with self.assertRaises(ProcessorError):
            proc.run([
                Begin(0),
                Change(0, 0, "users", "u1", None, ROW_A),
                Commit(0, 0),
                Change(0, 1, "users", "u1", ROW_A, ROW_B),  # 已终结事务的新 Change
            ])

    def test_duplicate_events_are_idempotent(self):
        proc = IncrementalProcessor(TABLES)
        proc.run([
            Begin(0), Begin(0),
            Change(0, 0, "users", "u1", None, ROW_A),
            Change(0, 0, "users", "u1", None, ROW_A),
            Commit(0, 0), Commit(0, 0),
            Change(0, 0, "users", "u1", None, ROW_A),       # 终结后的重复重放
        ])
        self.assertEqual(proc.state()["users"], {"u1": ROW_A})

    def test_abort_discards_changes(self):
        proc = IncrementalProcessor(TABLES)
        proc.run([
            Begin(0),
            Change(0, 0, "users", "u1", None, ROW_A),
            Abort(0),
        ])
        self.assertEqual(proc.state()["users"], {})


if __name__ == "__main__":
    unittest.main()
