"""独立校验状态机：预算守恒、最多一次发布、秘密卫生。"""

import os
import random
import tempfile
import unittest

from dpledger import BudgetLedger, InvariantViolation, verify
from dpledger.checker import SECRET_PAYLOAD_KEYS

CONTENT = {"type": "count", "dataset": "synthetic", "sensitivity": 1.0}


def make_event(seq, etype, qid, **payload):
    return {"seq": seq, "type": etype, "query_id": qid,
            "payload": payload, "at": float(seq)}


class TestCheckerOnRealLog(unittest.TestCase):
    def test_real_ledger_log_passes(self):
        tmp = tempfile.TemporaryDirectory()
        db = os.path.join(tmp.name, "ledger.db")
        ledger = BudgetLedger(db, total_epsilon=3.0, rng=random.Random(5))
        ledger.reserve("q1", CONTENT, 1.0)
        ledger.reserve("q2", CONTENT, 1.0)
        ledger.commit("q1", CONTENT, true_value=10.0)
        ledger.cancel("q2", CONTENT)  # 取消返还预算
        ledger.reserve("q3", CONTENT, 2.0)
        ledger.commit("q3", CONTENT, true_value=20.0)

        report = verify(ledger.events(), total_epsilon=3.0)
        self.assertAlmostEqual(report["spent"], 3.0)
        self.assertAlmostEqual(report["reserved"], 0.0)
        self.assertEqual(report["published"], ["q1", "q3"])
        self.assertEqual(report["states"]["q2"], "CANCELLED")
        ledger.close()
        tmp.cleanup()


class TestCheckerRejectsTampering(unittest.TestCase):
    def test_double_publish_rejected(self):
        events = [
            make_event(1, "RESERVE", "q1", query_id="q1", content_hash="h",
                       epsilon=1.0),
            make_event(2, "PUBLISH", "q1", query_id="q1", content_hash="h",
                       epsilon=1.0, noisy_value=1.5),
            make_event(3, "PUBLISH", "q1", query_id="q1", content_hash="h",
                       epsilon=1.0, noisy_value=2.5),
        ]
        with self.assertRaises(InvariantViolation):
            verify(events, total_epsilon=4.0)

    def test_overspend_rejected(self):
        events = [
            make_event(1, "RESERVE", "q1", query_id="q1", content_hash="h",
                       epsilon=3.0),
            make_event(2, "RESERVE", "q2", query_id="q2", content_hash="h",
                       epsilon=3.0),
        ]
        with self.assertRaises(InvariantViolation):
            verify(events, total_epsilon=4.0)

    def test_cancel_after_publish_rejected(self):
        events = [
            make_event(1, "RESERVE", "q1", query_id="q1", content_hash="h",
                       epsilon=1.0),
            make_event(2, "PUBLISH", "q1", query_id="q1", content_hash="h",
                       epsilon=1.0, noisy_value=1.5),
            make_event(3, "CANCEL", "q1", query_id="q1", content_hash="h",
                       epsilon=1.0),
        ]
        with self.assertRaises(InvariantViolation):
            verify(events, total_epsilon=4.0)

    def test_publish_without_reserve_rejected(self):
        events = [
            make_event(1, "PUBLISH", "q1", query_id="q1", content_hash="h",
                       epsilon=1.0, noisy_value=1.5),
        ]
        with self.assertRaises(InvariantViolation):
            verify(events, total_epsilon=4.0)

    def test_secret_state_in_log_rejected(self):
        events = [
            make_event(1, "RESERVE", "q1", query_id="q1", content_hash="h",
                       epsilon=1.0, rng_state="31337"),
        ]
        with self.assertRaises(InvariantViolation):
            verify(events, total_epsilon=4.0)

    def test_true_value_not_allowed_in_public_log(self):
        events = [
            make_event(1, "RESERVE", "q1", query_id="q1", content_hash="h",
                       epsilon=1.0),
            make_event(2, "PUBLISH", "q1", query_id="q1", content_hash="h",
                       epsilon=1.0, noisy_value=1.5, true_value=1.0),
        ]
        with self.assertRaises(InvariantViolation):
            verify(events, total_epsilon=4.0)

    def test_real_log_contains_no_secret_fields(self):
        tmp = tempfile.TemporaryDirectory()
        db = os.path.join(tmp.name, "ledger.db")
        ledger = BudgetLedger(db, total_epsilon=2.0, rng=random.Random(1))
        ledger.reserve("q1", CONTENT, 1.0)
        ledger.commit("q1", CONTENT, true_value=123.456)
        for event in ledger.events():
            for key in event["payload"]:
                self.assertNotIn(key, SECRET_PAYLOAD_KEYS)
            self.assertNotIn("true_value", event["payload"])
        ledger.close()
        tmp.cleanup()


if __name__ == "__main__":
    unittest.main()
