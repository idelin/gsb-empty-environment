"""并发语义：预留不超总额；同 ID 不同内容并发冲突。"""

import os
import random
import tempfile
import threading
import unittest

from dpledger import (
    BudgetLedger,
    BudgetExhaustedError,
    ConflictError,
    verify,
)

CONTENT = {"type": "count", "dataset": "synthetic", "sensitivity": 1.0}


class ConcurrencyTestBase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.db = os.path.join(self.tmp.name, "ledger.db")
        self.ledger = BudgetLedger(self.db, total_epsilon=4.0,
                                   rng=random.Random(7))

    def tearDown(self):
        self.ledger.close()
        self.tmp.cleanup()

    def run_threads(self, fn, n):
        barrier = threading.Barrier(n)
        results = [None] * n
        errors = [None] * n

        def worker(i):
            barrier.wait()  # 尽量同时进入临界区
            try:
                results[i] = fn(i)
            except Exception as exc:  # noqa: BLE001 - 测试需收集异常类型
                errors[i] = exc

        threads = [threading.Thread(target=worker, args=(i,)) for i in range(n)]
        for t in threads:
            t.start()
        for t in threads:
            t.join()
        return results, errors


class TestConcurrentReservationCap(ConcurrencyTestBase):
    def test_concurrent_reservations_never_exceed_total(self):
        n = 16
        results, errors = self.run_threads(
            lambda i: self.ledger.reserve(f"q{i}", CONTENT, 1.0), n)

        succeeded = [r for r in results if r is not None]
        exhausted = [e for e in errors
                     if isinstance(e, BudgetExhaustedError)]
        self.assertEqual(len(succeeded), 4)          # 总额 4.0 / 每个 1.0
        self.assertEqual(len(exhausted), n - 4)
        self.assertTrue(all(
            isinstance(e, BudgetExhaustedError) for e in errors if e))
        b = self.ledger.budget()
        self.assertLessEqual(b["reserved"] + b["spent"], b["total"])
        self.assertAlmostEqual(b["reserved"], 4.0)
        # 独立状态机核对预算守恒。
        report = verify(self.ledger.events(), total_epsilon=4.0)
        self.assertAlmostEqual(report["reserved"], 4.0)


class TestConcurrentSameIdConflict(ConcurrencyTestBase):
    def test_concurrent_same_id_different_content(self):
        contents = [
            {"type": "count", "dataset": "synthetic", "sensitivity": 1.0},
            {"type": "sum", "dataset": "synthetic", "sensitivity": 2.0},
        ]
        n = 8
        results, errors = self.run_threads(
            lambda i: self.ledger.reserve("shared-id", contents[i % 2], 1.0), n)

        succeeded = [r for r in results if r is not None]
        conflicts = [e for e in errors if isinstance(e, ConflictError)]
        # 恰好一个内容胜出：首个预留成功，同内容幂等成功，异内容冲突。
        self.assertEqual(len(succeeded) + len(conflicts), n)
        self.assertGreaterEqual(len(conflicts), 1)
        winner_hash = succeeded[0]["content_hash"]
        self.assertTrue(all(s["content_hash"] == winner_hash
                            for s in succeeded))
        self.assertEqual(self.ledger.budget()["reserved"], 1.0)  # 只计一次

    def test_concurrent_commit_publishes_at_most_once(self):
        self.ledger.reserve("q1", CONTENT, 1.0)
        n = 8
        results, errors = self.run_threads(
            lambda i: self.ledger.commit("q1", CONTENT, true_value=3.0), n)
        self.assertEqual(len([e for e in errors if e]), 0)
        values = {r["noisy_value"] for r in results}
        self.assertEqual(len(values), 1)  # 所有并发提交得到同一噪声值
        publishes = [e for e in self.ledger.events()
                     if e["type"] == "PUBLISH"]
        self.assertEqual(len(publishes), 1)
        self.assertEqual(self.ledger.budget()["spent"], 1.0)


if __name__ == "__main__":
    unittest.main()
