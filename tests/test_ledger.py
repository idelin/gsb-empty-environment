"""账本基本语义：预留 / 提交 / 取消、幂等、冲突、epsilon 求和组合。"""

import os
import random
import tempfile
import unittest

from dpledger import (
    BudgetLedger,
    BudgetExhaustedError,
    ConflictError,
    InvalidStateError,
    UnknownQueryError,
)

CONTENT_A = {"type": "count", "dataset": "synthetic_users", "sensitivity": 1.0}
CONTENT_B = {"type": "sum", "dataset": "synthetic_users", "sensitivity": 2.0}


class LedgerTestBase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.db = os.path.join(self.tmp.name, "ledger.db")
        self.ledger = BudgetLedger(self.db, total_epsilon=4.0,
                                   rng=random.Random(1234))

    def tearDown(self):
        self.ledger.close()
        self.tmp.cleanup()


class TestReserveCommitCancel(LedgerTestBase):
    def test_happy_path_reserve_commit(self):
        st = self.ledger.reserve("q1", CONTENT_A, 1.0)
        self.assertEqual(st["state"], "RESERVED")
        self.assertEqual(self.ledger.budget()["reserved"], 1.0)

        out = self.ledger.commit("q1", CONTENT_A, true_value=100.0)
        self.assertEqual(out["state"], "PUBLISHED")
        self.assertFalse(out["reused"])
        self.assertNotEqual(out["noisy_value"], 100.0)  # 已加噪

        b = self.ledger.budget()
        self.assertEqual(b["reserved"], 0.0)
        self.assertEqual(b["spent"], 1.0)
        self.assertAlmostEqual(b["available"], 3.0)

    def test_cancel_refunds_budget(self):
        self.ledger.reserve("q1", CONTENT_A, 1.5)
        self.ledger.cancel("q1", CONTENT_A)
        b = self.ledger.budget()
        self.assertEqual(b["reserved"], 0.0)
        self.assertEqual(b["spent"], 0.0)
        self.assertAlmostEqual(b["available"], 4.0)

    def test_cancel_published_is_rejected_and_no_refund(self):
        self.ledger.reserve("q1", CONTENT_A, 1.5)
        self.ledger.commit("q1", CONTENT_A, true_value=7.0)
        with self.assertRaises(InvalidStateError):
            self.ledger.cancel("q1", CONTENT_A)
        b = self.ledger.budget()
        self.assertEqual(b["spent"], 1.5)  # 预算不退还
        self.assertAlmostEqual(b["available"], 2.5)

    def test_commit_cancelled_is_rejected(self):
        self.ledger.reserve("q1", CONTENT_A, 1.0)
        self.ledger.cancel("q1", CONTENT_A)
        with self.assertRaises(InvalidStateError):
            self.ledger.commit("q1", CONTENT_A, true_value=1.0)

    def test_commit_unknown_query(self):
        with self.assertRaises(UnknownQueryError):
            self.ledger.commit("ghost", CONTENT_A, true_value=1.0)


class TestIdempotency(LedgerTestBase):
    def test_duplicate_reserve_is_idempotent(self):
        s1 = self.ledger.reserve("q1", CONTENT_A, 1.0)
        s2 = self.ledger.reserve("q1", CONTENT_A, 1.0)
        self.assertEqual(s1, s2)
        self.assertEqual(self.ledger.budget()["reserved"], 1.0)  # 只计一次

    def test_duplicate_commit_reuses_noise_and_budget(self):
        self.ledger.reserve("q1", CONTENT_A, 1.0)
        r1 = self.ledger.commit("q1", CONTENT_A, true_value=42.0)
        r2 = self.ledger.commit("q1", CONTENT_A, true_value=42.0)
        self.assertTrue(r2["reused"])
        self.assertEqual(r1["noisy_value"], r2["noisy_value"])  # 复用原噪声
        self.assertEqual(self.ledger.budget()["spent"], 1.0)     # 只扣一次
        publishes = [e for e in self.ledger.events() if e["type"] == "PUBLISH"]
        self.assertEqual(len(publishes), 1)  # 最多一次发布

    def test_duplicate_cancel_is_idempotent(self):
        self.ledger.reserve("q1", CONTENT_A, 1.0)
        self.ledger.cancel("q1", CONTENT_A)
        st = self.ledger.cancel("q1", CONTENT_A)
        self.assertEqual(st["state"], "CANCELLED")


class TestConflict(LedgerTestBase):
    def test_same_id_different_content_conflicts_on_reserve(self):
        self.ledger.reserve("q1", CONTENT_A, 1.0)
        with self.assertRaises(ConflictError):
            self.ledger.reserve("q1", CONTENT_B, 1.0)

    def test_same_id_different_content_conflicts_on_commit(self):
        self.ledger.reserve("q1", CONTENT_A, 1.0)
        with self.assertRaises(ConflictError):
            self.ledger.commit("q1", CONTENT_B, true_value=1.0)

    def test_same_id_different_content_conflicts_on_cancel(self):
        self.ledger.reserve("q1", CONTENT_A, 1.0)
        with self.assertRaises(ConflictError):
            self.ledger.cancel("q1", CONTENT_B)


class TestComposition(LedgerTestBase):
    def test_explicit_epsilon_sum_composition(self):
        # 组合规则：已发布 epsilon 明示求和，总额 4.0。
        epsilons = [1.0, 0.5, 2.0, 0.5]
        for i, eps in enumerate(epsilons):
            qid = f"q{i}"
            self.ledger.reserve(qid, CONTENT_A, eps)
            self.ledger.commit(qid, CONTENT_A, true_value=float(i))
        b = self.ledger.budget()
        self.assertAlmostEqual(b["spent"], sum(epsilons))
        self.assertAlmostEqual(b["available"], 0.0)
        with self.assertRaises(BudgetExhaustedError):
            self.ledger.reserve("q_over", CONTENT_A, 0.01)

    def test_reserve_cannot_exceed_total(self):
        self.ledger.reserve("q1", CONTENT_A, 3.0)
        with self.assertRaises(BudgetExhaustedError):
            self.ledger.reserve("q2", CONTENT_A, 1.5)
        self.ledger.reserve("q2", CONTENT_A, 1.0)  # 恰好等于总额可行


if __name__ == "__main__":
    unittest.main()
