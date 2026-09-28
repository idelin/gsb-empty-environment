"""Unittests for the bounded replay-dedup inbox (all data is synthetic)."""

import os
import tempfile
import unittest

from inbox import APPLIED, CACHED, IGNORED, REJECTED, Inbox, SimulatedCrash


class InboxTestBase(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.db = os.path.join(self.tmp.name, "inbox.db")
        self.inbox = Inbox(self.db, window=3)

    def tearDown(self):
        self.inbox.close()
        self.tmp.cleanup()

    def reopen(self):
        """Simulate process restart: new Inbox over the same DB file."""
        self.inbox.close()
        self.inbox = Inbox(self.db, window=3)


class TestAcceptanceBoundary(InboxTestBase):
    def test_w3_committed0_boundary(self):
        # W=3, committed=0: seq 3 is inside the window -> cached.
        status, applied = self.inbox.receive("alice", 3, 30)
        self.assertEqual(status, CACHED)
        self.assertEqual(applied, [])
        self.assertEqual(self.inbox.state(), ({}, 0))

        # seq 4 is beyond cursor(0) + W(3) -> explicitly rejected,
        # and the rejection must not advance the cursor.
        status, _ = self.inbox.receive("alice", 4, 40)
        self.assertEqual(status, REJECTED)
        self.assertEqual(self.inbox.state(), ({}, 0))

        # seq 1 and 2 arrive: the contiguous cursor advances to 3 and
        # the cached seq 3 drains in the same transactions.
        status, applied = self.inbox.receive("alice", 1, 10)
        self.assertEqual((status, applied), (APPLIED, [1]))
        status, applied = self.inbox.receive("alice", 2, 20)
        self.assertEqual((status, applied), (APPLIED, [2, 3]))
        self.assertEqual(self.inbox.state(), ({"alice": 3}, 60))

        # Duplicate of seq 1 (<= committed cursor): idempotent ignore,
        # value is NOT added again.
        status, applied = self.inbox.receive("alice", 1, 10)
        self.assertEqual((status, applied), (IGNORED, []))
        self.assertEqual(self.inbox.state(), ({"alice": 3}, 60))


class TestWindowSemantics(InboxTestBase):
    def test_reject_does_not_cache_or_advance(self):
        self.inbox.receive("a", 4, 400)  # rejected
        self.inbox.receive("a", 1, 10)
        self.inbox.receive("a", 2, 20)
        self.inbox.receive("a", 3, 30)
        # If 4 had been cached it would drain here; balance proves it was not.
        self.assertEqual(self.inbox.state(), ({"a": 3}, 60))

    def test_window_slides_with_cursor(self):
        self.inbox.receive("a", 1, 1)
        self.inbox.receive("a", 2, 2)
        self.inbox.receive("a", 3, 3)  # cursor = 3
        status, _ = self.inbox.receive("a", 7, 7)  # 7 > 3 + 3 -> rejected
        self.assertEqual(status, REJECTED)
        status, _ = self.inbox.receive("a", 6, 6)  # 6 <= 3 + 3 -> cached
        self.assertEqual(status, CACHED)

    def test_duplicate_of_cached_message_applies_once(self):
        self.inbox.receive("a", 2, 20)
        status, _ = self.inbox.receive("a", 2, 20)  # still cached, no effect
        self.assertEqual(status, CACHED)
        self.inbox.receive("a", 1, 10)
        self.assertEqual(self.inbox.state(), ({"a": 2}, 30))

    def test_senders_have_independent_cursors(self):
        self.inbox.receive("a", 1, 1)
        self.inbox.receive("a", 2, 2)
        status, _ = self.inbox.receive("b", 2, 20)  # b's cursor is still 0
        self.assertEqual(status, CACHED)
        self.inbox.receive("b", 1, 10)
        cursors, balance = self.inbox.state()
        self.assertEqual(cursors, {"a": 2, "b": 2})
        self.assertEqual(balance, 33)


class TestCrashRecovery(InboxTestBase):
    def test_crash_before_commit_rolls_back_then_retry_applies_once(self):
        self.inbox.crash_point = "before"
        with self.assertRaises(SimulatedCrash):
            self.inbox.receive("a", 1, 10)
        self.reopen()
        # Rolled back: nothing applied, nothing cached.
        self.assertEqual(self.inbox.state(), ({}, 0))
        status, applied = self.inbox.receive("a", 1, 10)
        self.assertEqual((status, applied), (APPLIED, [1]))
        self.assertEqual(self.inbox.state(), ({"a": 1}, 10))

    def test_crash_after_commit_persists_exactly_once(self):
        self.inbox.crash_point = "after"
        with self.assertRaises(SimulatedCrash):
            self.inbox.receive("a", 1, 10)
        self.reopen()
        # Committed before the "process died": effect visible after recovery.
        self.assertEqual(self.inbox.state(), ({"a": 1}, 10))
        # Client retries the same message: idempotent ignore, no double add.
        status, _ = self.inbox.receive("a", 1, 10)
        self.assertEqual(status, IGNORED)
        self.assertEqual(self.inbox.state(), ({"a": 1}, 10))

    def test_pending_cache_survives_crash(self):
        self.inbox.receive("a", 3, 30)  # cached
        self.inbox.crash_point = "after"
        with self.assertRaises(SimulatedCrash):
            self.inbox.receive("a", 2, 20)  # cached, committed, then "died"
        self.reopen()
        self.assertEqual(self.inbox.state(), ({}, 0))
        # Gap fills after recovery: 1, 2, 3 each applied exactly once.
        status, applied = self.inbox.receive("a", 1, 10)
        self.assertEqual((status, applied), (APPLIED, [1, 2, 3]))
        self.assertEqual(self.inbox.state(), ({"a": 3}, 60))

    def test_crash_before_commit_of_cached_message_keeps_nothing(self):
        self.inbox.crash_point = "before"
        with self.assertRaises(SimulatedCrash):
            self.inbox.receive("a", 2, 20)
        self.reopen()
        # The cached insert rolled back too; redelivery works cleanly.
        self.assertEqual(self.inbox.state(), ({}, 0))
        self.inbox.receive("a", 1, 10)
        self.inbox.receive("a", 2, 20)
        self.assertEqual(self.inbox.state(), ({"a": 2}, 30))


if __name__ == "__main__":
    unittest.main()
