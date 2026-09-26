"""Unit tests for the budget ledger and the independent checker.

Synthetic data only; nothing here is a formal privacy proof.
"""

import json
import sqlite3
import threading
import unittest
from pathlib import Path
from tempfile import TemporaryDirectory

from budget_ledger import (
    FAULT_POINTS,
    BudgetExhaustedError,
    BudgetLedger,
    ConflictError,
    FaultInjectedError,
    InvalidStateError,
    UnknownQueryError,
)
from ledger_checker import FORBIDDEN_LOG_KEYS, LedgerChecker


class LedgerTestCase(unittest.TestCase):
    def setUp(self):
        self._tmp = TemporaryDirectory()
        self.addCleanup(self._tmp.cleanup)
        self.db = Path(self._tmp.name) / "ledger.db"
        self.ledger = BudgetLedger(self.db, total_epsilon=1.0, sensitivity=1.0)

    def check_ok(self):
        report = LedgerChecker().check(self.db)
        self.assertTrue(report.ok, report.violations)
        return report


class TestReserveCommitCancel(LedgerTestCase):
    def test_happy_path(self):
        self.ledger.reserve("q1", {"sql": "SELECT COUNT(*) FROM t"}, 0.4)
        pub = self.ledger.commit("q1", true_value=47.0)
        self.assertFalse(pub.noise_reused)
        snap = self.ledger.snapshot()
        self.assertAlmostEqual(snap.spent, 0.4)
        self.assertAlmostEqual(snap.reserved, 0.0)
        self.assertAlmostEqual(snap.available, 0.6)
        self.assertAlmostEqual(snap.total, snap.reserved + snap.spent + snap.available)
        self.check_ok()

    def test_reserve_idempotent_same_content(self):
        content = {"sql": "SELECT SUM(x) FROM t"}
        r1 = self.ledger.reserve("q1", content, 0.3)
        r2 = self.ledger.reserve("q1", content, 0.3)
        self.assertFalse(r1.reused)
        self.assertTrue(r2.reused)
        # Only one reservation holds budget.
        self.assertAlmostEqual(self.ledger.snapshot().reserved, 0.3)
        self.check_ok()

    def test_reserve_conflict_same_id_different_content(self):
        self.ledger.reserve("q1", {"sql": "A"}, 0.3)
        with self.assertRaises(ConflictError):
            self.ledger.reserve("q1", {"sql": "B"}, 0.3)

    def test_commit_with_different_content_conflicts(self):
        self.ledger.reserve("q1", {"sql": "A"}, 0.3)
        with self.assertRaises(ConflictError):
            self.ledger.commit("q1", content={"sql": "B"}, true_value=1.0)

    def test_commit_idempotent_reuses_noise(self):
        self.ledger.reserve("q1", {"sql": "A"}, 0.5)
        p1 = self.ledger.commit("q1", true_value=10.0)
        p2 = self.ledger.commit("q1", true_value=10.0)
        self.assertTrue(p2.noise_reused)
        self.assertEqual(p1.result, p2.result)
        self.assertEqual(p1.publish_seq, p2.publish_seq)
        # Budget deducted exactly once.
        self.assertAlmostEqual(self.ledger.snapshot().spent, 0.5)
        self.check_ok()

    def test_commit_unknown_query(self):
        with self.assertRaises(UnknownQueryError):
            self.ledger.commit("nope", true_value=1.0)

    def test_cancel_reserved_refunds_budget(self):
        self.ledger.reserve("q1", {"sql": "A"}, 0.7)
        self.assertAlmostEqual(self.ledger.snapshot().available, 0.3)
        c = self.ledger.cancel("q1")
        self.assertTrue(c.refunded)
        self.assertAlmostEqual(self.ledger.snapshot().available, 1.0)
        self.check_ok()

    def test_cancel_published_raises_and_keeps_budget(self):
        self.ledger.reserve("q1", {"sql": "A"}, 0.6)
        self.ledger.commit("q1", true_value=3.0)
        with self.assertRaises(InvalidStateError):
            self.ledger.cancel("q1")
        snap = self.ledger.snapshot()
        self.assertAlmostEqual(snap.spent, 0.6)
        self.assertAlmostEqual(snap.available, 0.4)
        self.check_ok()

    def test_cancel_idempotent_and_unknown(self):
        self.ledger.reserve("q1", {"sql": "A"}, 0.2)
        self.ledger.cancel("q1")
        c2 = self.ledger.cancel("q1")
        self.assertTrue(c2.reused)
        self.assertFalse(c2.refunded)
        with self.assertRaises(UnknownQueryError):
            self.ledger.cancel("ghost")
        self.check_ok()

    def test_cancelled_query_is_terminal(self):
        self.ledger.reserve("q1", {"sql": "A"}, 0.2)
        self.ledger.cancel("q1")
        with self.assertRaises(InvalidStateError):
            self.ledger.commit("q1", true_value=1.0)
        with self.assertRaises(InvalidStateError):
            self.ledger.reserve("q1", {"sql": "A"}, 0.2)
        self.check_ok()

    def test_budget_exhaustion(self):
        self.ledger.reserve("q1", {"sql": "A"}, 0.8)
        with self.assertRaises(BudgetExhaustedError):
            self.ledger.reserve("q2", {"sql": "B"}, 0.3)
        self.ledger.reserve("q2", {"sql": "B"}, 0.2)
        with self.assertRaises(BudgetExhaustedError):
            self.ledger.reserve("q3", {"sql": "C"}, 1e-6)
        self.check_ok()

    def test_composition_is_explicit_epsilon_sum(self):
        epsilons = [0.1, 0.2, 0.25]
        for i, eps in enumerate(epsilons):
            self.ledger.reserve(f"q{i}", {"i": i}, eps)
            self.ledger.commit(f"q{i}", true_value=float(i))
        self.assertAlmostEqual(self.ledger.composition_bound(), sum(epsilons))
        self.check_ok()


class TestCommitBoundaryFaults(LedgerTestCase):
    def test_every_fault_point_is_safe_to_retry(self):
        for fault in FAULT_POINTS:
            with self.subTest(fault=fault):
                tmp = TemporaryDirectory()
                self.addCleanup(tmp.cleanup)
                db = Path(tmp.name) / "l.db"
                ledger = BudgetLedger(db, total_epsilon=1.0)
                ledger.reserve("q", {"sql": "A"}, 0.5)
                with self.assertRaises(FaultInjectedError):
                    ledger.commit("q", true_value=5.0, fault_at=fault)
                # Retry after the simulated crash must succeed and be
                # idempotent on a second replay.
                pub = ledger.commit("q", true_value=5.0)
                again = ledger.commit("q", true_value=5.0)
                self.assertEqual(pub.result, again.result)
                self.assertAlmostEqual(ledger.snapshot().spent, 0.5)
                report = LedgerChecker().check(db)
                self.assertTrue(report.ok, report.violations)

    def test_crash_after_publish_reuses_noise_and_budget(self):
        self.ledger.reserve("q1", {"sql": "A"}, 0.5)
        with self.assertRaises(FaultInjectedError):
            self.ledger.commit("q1", true_value=8.0, fault_at="after_tx_commit")
        # Simulate process restart: brand new ledger object on the same file.
        recovered = BudgetLedger(self.db, total_epsilon=1.0)
        pub = recovered.commit("q1", true_value=8.0)
        self.assertTrue(pub.noise_reused)
        # The durable record holds exactly one noise draw and one deduction.
        row = sqlite3.connect(str(self.db)).execute(
            "SELECT noise, result FROM queries WHERE query_id = 'q1'"
        ).fetchone()
        self.assertAlmostEqual(pub.result, row[1])
        self.assertAlmostEqual(row[1], 8.0 + row[0])
        self.assertAlmostEqual(recovered.snapshot().spent, 0.5)
        self.check_ok()

    def test_in_transaction_fault_rolls_back_fully(self):
        self.ledger.reserve("q1", {"sql": "A"}, 0.5)
        with self.assertRaises(FaultInjectedError):
            self.ledger.commit("q1", true_value=8.0, fault_at="after_publish_recorded")
        # Nothing was published: still reserved, no spend, retry allowed.
        snap = self.ledger.snapshot()
        self.assertAlmostEqual(snap.reserved, 0.5)
        self.assertAlmostEqual(snap.spent, 0.0)
        pub = self.ledger.commit("q1", true_value=8.0)
        self.assertFalse(pub.noise_reused)
        self.check_ok()


class TestConcurrency(LedgerTestCase):
    def test_concurrent_reservations_never_exceed_total(self):
        barrier = threading.Barrier(9)
        outcomes = {"ok": 0, "full": 0}
        lock = threading.Lock()

        def worker(i):
            barrier.wait()
            try:
                self.ledger.reserve(f"q{i}", {"i": i}, 0.3)
                with lock:
                    outcomes["ok"] += 1
            except BudgetExhaustedError:
                with lock:
                    outcomes["full"] += 1

        threads = [threading.Thread(target=worker, args=(i,)) for i in range(8)]
        for t in threads:
            t.start()
        barrier.wait()
        for t in threads:
            t.join()
        # 0.3 each against a total of 1.0: at most 3 may succeed.
        self.assertLessEqual(outcomes["ok"], 3)
        self.assertEqual(outcomes["ok"] + outcomes["full"], 8)
        self.assertLessEqual(self.ledger.snapshot().reserved, 1.0 + 1e-9)
        self.check_ok()

    def test_concurrent_same_id_different_content_conflict(self):
        barrier = threading.Barrier(11)
        results = []
        conflicts = []
        lock = threading.Lock()

        def worker(i):
            content = {"sql": "A"} if i % 2 == 0 else {"sql": "B"}
            barrier.wait()
            try:
                self.ledger.reserve("shared", content, 0.4)
                with lock:
                    results.append(content["sql"])
            except ConflictError:
                with lock:
                    conflicts.append(content["sql"])

        threads = [threading.Thread(target=worker, args=(i,)) for i in range(10)]
        for t in threads:
            t.start()
        barrier.wait()
        for t in threads:
            t.join()
        # Exactly one content wins; every loser conflicts.
        self.assertEqual(len(set(results)), 1)
        winner = results[0]
        self.assertTrue(all(c != winner for c in conflicts))
        self.assertGreaterEqual(len(conflicts), 1)
        self.assertEqual(len(results) + len(conflicts), 10)
        self.check_ok()


class TestIndependentChecker(LedgerTestCase):
    def _populate(self):
        self.ledger.reserve("q1", {"sql": "A"}, 0.4)
        self.ledger.commit("q1", true_value=1.0)
        self.ledger.reserve("q2", {"sql": "B"}, 0.3)
        self.ledger.reserve("q3", {"sql": "C"}, 0.2)
        self.ledger.cancel("q3")

    def test_checker_passes_on_healthy_ledger(self):
        self._populate()
        report = self.check_ok()
        self.assertAlmostEqual(report.spent, 0.4)
        self.assertAlmostEqual(report.reserved, 0.3)
        self.assertAlmostEqual(report.available, 0.3)

    def test_checker_detects_double_publish(self):
        self._populate()
        conn = sqlite3.connect(str(self.db))
        conn.execute(
            "INSERT INTO events(ts, query_id, event, epsilon, details)"
            " VALUES (0, 'q1', 'PUBLISH', 0.4, '{}')"
        )
        conn.commit()
        conn.close()
        report = LedgerChecker().check(self.db)
        self.assertFalse(report.ok)
        self.assertTrue(any("at-most-once" in v for v in report.violations))

    def test_checker_detects_conservation_break(self):
        self._populate()
        conn = sqlite3.connect(str(self.db))
        conn.execute("UPDATE queries SET epsilon = 0.9 WHERE query_id = 'q1'")
        conn.commit()
        conn.close()
        report = LedgerChecker().check(self.db)
        self.assertFalse(report.ok)
        self.assertTrue(any("spend" in v or "conservation" in v
                            for v in report.violations))

    def test_checker_detects_cancel_after_publish(self):
        self._populate()
        conn = sqlite3.connect(str(self.db))
        conn.execute(
            "INSERT INTO events(ts, query_id, event, epsilon, details)"
            " VALUES (0, 'q1', 'CANCEL', 0.4, '{}')"
        )
        conn.commit()
        conn.close()
        report = LedgerChecker().check(self.db)
        self.assertFalse(report.ok)
        self.assertTrue(any("refund" in v or "illegal transition" in v
                            for v in report.violations))

    def test_public_log_has_no_secret_state(self):
        self._populate()
        conn = sqlite3.connect(str(self.db))
        events = conn.execute("SELECT * FROM events").fetchall()
        noise_values = {
            row[0] for row in conn.execute(
                "SELECT noise FROM queries WHERE noise IS NOT NULL"
            )
        }
        conn.close()
        self.assertTrue(events)
        for _, _, _, _, _, details_json in events:
            details = json.loads(details_json)
            self.assertFalse(FORBIDDEN_LOG_KEYS & set(details))
            blob = json.dumps(details)
            for noise in noise_values:
                self.assertNotIn(repr(noise), blob)


if __name__ == "__main__":
    unittest.main()
