"""Unit tests for the bounded replay-deduplicating inbox.

Run with::

    python3.11 -m unittest discover -s tests -v
"""

from __future__ import annotations

import json
import os
import subprocess
import sys
import tempfile
import unittest

from dedup_inbox import BoundedInbox

REPO_ROOT = os.path.dirname(os.path.dirname(os.path.abspath(__file__)))


class InboxLogicTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.db = os.path.join(self.tmp.name, "inbox.db")

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def open(self, window: int = 3, crash_hook=None) -> BoundedInbox:
        return BoundedInbox(self.db, window=window, crash_hook=crash_hook)

    def test_acceptance_boundary(self) -> None:
        with self.open(window=3) as inbox:
            # W=3, cursor=0: seq=3 cacheable, seq=4 rejected.
            r3 = inbox.deliver("alice", 3, 30)
            self.assertEqual(r3.status, "cached")
            self.assertEqual(r3.cursor, 0)
            self.assertEqual(r3.total, 0)

            r4 = inbox.deliver("alice", 4, 40)
            self.assertEqual(r4.status, "rejected")
            self.assertEqual(r4.cursor, 0)

            state = inbox.state("alice")
            self.assertEqual(state["pending"], [{"seq": 3, "value": 30}])

            # 1 then 2: contiguous cursor reaches 3, value applied once.
            r1 = inbox.deliver("alice", 1, 10)
            self.assertEqual(r1.status, "committed")
            self.assertEqual(r1.committed_seqs, (1,))
            self.assertEqual(r1.cursor, 1)
            self.assertEqual(r1.total, 10)

            r2 = inbox.deliver("alice", 2, 20)
            self.assertEqual(r2.status, "committed")
            self.assertEqual(r2.committed_seqs, (2, 3))
            self.assertEqual(r2.cursor, 3)
            self.assertEqual(r2.total, 60)

            # Replay of 1 is idempotent: total stays 60.
            dup = inbox.deliver("alice", 1, 10)
            self.assertEqual(dup.status, "duplicate")
            self.assertEqual(dup.cursor, 3)
            self.assertEqual(dup.total, 60)

            # seq=4 is now within the shifted window and commits immediately.
            r4b = inbox.deliver("alice", 4, 40)
            self.assertEqual(r4b.status, "committed")
            self.assertEqual(r4b.committed_seqs, (4,))
            self.assertEqual(r4b.total, 100)

    def test_reject_does_not_advance_cursor_or_apply_value(self) -> None:
        with self.open(window=3) as inbox:
            inbox.deliver("bob", 1, 5)
            far = inbox.deliver("bob", 5, 500)
            self.assertEqual(far.status, "rejected")
            self.assertEqual(far.cursor, 1)
            self.assertEqual(far.total, 5)

            self.assertIsNone(
                inbox.conn.execute(
                    "SELECT 1 FROM ledger WHERE sender='bob' AND seq=5"
                ).fetchone()
            )
            self.assertIsNone(
                inbox.conn.execute(
                    "SELECT 1 FROM pending WHERE sender='bob' AND seq=5"
                ).fetchone()
            )

            # Fill the window: the earlier reject stored nothing, so a fresh
            # seq=5 is needed once the gap closes.
            self.assertEqual(inbox.deliver("bob", 4, 40).status, "cached")
            self.assertEqual(inbox.deliver("bob", 2, 20).status, "committed")
            self.assertEqual(inbox.deliver("bob", 3, 30).status, "committed")
            self.assertEqual(inbox.deliver("bob", 5, 500).status, "committed")
            self.assertEqual(inbox.state("bob")["total"], 595)

    def test_cached_duplicate_is_idempotent(self) -> None:
        with self.open(window=3) as inbox:
            first = inbox.deliver("carol", 2, 20)
            self.assertEqual(first.status, "cached")
            again = inbox.deliver("carol", 2, 999)
            self.assertEqual(again.status, "duplicate")
            self.assertEqual(again.cursor, 0)
            self.assertEqual(again.total, 0)

            committed = inbox.deliver("carol", 1, 10)
            self.assertEqual(committed.status, "committed")
            # Original value 20 is applied, never the replay value 999.
            self.assertEqual(committed.total, 30)

    def test_senders_are_independent(self) -> None:
        with self.open(window=3) as inbox:
            inbox.deliver("a", 1, 1)
            inbox.deliver("b", 3, 30)
            self.assertEqual(inbox.state("a")["cursor"], 1)
            self.assertEqual(inbox.state("a")["total"], 1)
            self.assertEqual(inbox.state("b")["cursor"], 0)
            self.assertEqual(inbox.state("b")["pending"], [{"seq": 3, "value": 30}])

    def test_window_edge_is_acceptable(self) -> None:
        with self.open(window=3) as inbox:
            self.assertEqual(inbox.deliver("d", 1, 1).status, "committed")
            self.assertEqual(inbox.deliver("d", 4, 4).status, "cached")
            self.assertEqual(inbox.deliver("d", 5, 5).status, "rejected")

    def test_window_must_match_on_reopen(self) -> None:
        with self.open(window=3):
            pass
        with self.assertRaises(ValueError):
            BoundedInbox(self.db, window=2)

    def test_invalid_seq(self) -> None:
        with self.open() as inbox:
            with self.assertRaises(ValueError):
                inbox.deliver("e", 0, 1)
            with self.assertRaises(ValueError):
                inbox.deliver("e", -1, 1)

    def test_persistence_across_reopen(self) -> None:
        with self.open(window=3) as inbox:
            inbox.deliver("f", 1, 10)
            inbox.deliver("f", 3, 30)
        with self.open(window=3) as inbox:
            state = inbox.state("f")
            self.assertEqual(state["cursor"], 1)
            self.assertEqual(state["total"], 10)
            self.assertEqual(state["pending"], [{"seq": 3, "value": 30}])
            self.assertEqual(inbox.deliver("f", 2, 20).total, 60)

    def test_crash_before_and_after_write_rolls_back_in_process(self) -> None:
        # Both points are inside the transaction: a crash must roll back the
        # whole write, leaving no pending row and no cursor movement.
        for point in ("before_write", "after_write"):
            with self.subTest(point=point):
                def hook(fired: str, point: str = point) -> None:
                    if fired == point:
                        raise RuntimeError(f"injected crash at {fired}")

                with self.assertRaises(RuntimeError):
                    with self.open(window=3, crash_hook=hook) as inbox:
                        inbox.deliver("g", 2, 20)

                with self.open(window=3) as check:
                    state = check.state("g")
                    self.assertEqual(state["cursor"], 0)
                    self.assertEqual(state["total"], 0)
                    self.assertEqual(state["pending"], [])

        with self.open(window=3) as inbox:
            self.assertEqual(inbox.deliver("g", 2, 20).status, "cached")

    def test_crash_after_commit_preserves_effect_in_process(self) -> None:
        def hook(point: str) -> None:
            if point == "after_commit":
                raise RuntimeError("reply lost after durable commit")

        with self.assertRaises(RuntimeError):
            with self.open(window=3, crash_hook=hook) as inbox:
                inbox.deliver("h", 1, 10)

        with self.open(window=3) as inbox:
            state = inbox.state("h")
            self.assertEqual(state["cursor"], 1)
            self.assertEqual(state["total"], 10)
            self.assertEqual(inbox.deliver("h", 1, 10).status, "duplicate")
            self.assertEqual(inbox.state("h")["total"], 10)


class CrashRecoveryTest(unittest.TestCase):
    """Drives the real CLI as a subprocess to simulate hard crashes."""

    def setUp(self) -> None:
        self.tmp = tempfile.TemporaryDirectory()
        self.db = os.path.join(self.tmp.name, "inbox.db")
        self.env = dict(os.environ, PYTHONPATH=REPO_ROOT)

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def _run(self, *args: str) -> subprocess.CompletedProcess:
        return subprocess.run(
            [sys.executable, "-m", "dedup_inbox", "--db", self.db, *args],
            capture_output=True,
            text=True,
            env=self.env,
            cwd=REPO_ROOT,
        )

    def _state(self, sender: str) -> dict:
        proc = self._run("--window", "3", "state", sender)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        return json.loads(proc.stdout)["states"][0]

    def test_crash_before_write_has_no_effect(self) -> None:
        proc = self._run(
            "--window", "3", "deliver",
            "--sender", "alice", "--seq", "1", "--value", "10",
            "--crash", "before_write",
        )
        self.assertEqual(proc.returncode, 99, proc.stderr)
        self.assertEqual(proc.stdout, "")

        state = self._state("alice")
        self.assertEqual(state["cursor"], 0)
        self.assertEqual(state["total"], 0)
        self.assertEqual(state["pending"], [])

        proc = self._run(
            "--window", "3", "deliver",
            "--sender", "alice", "--seq", "1", "--value", "10",
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(proc.stdout)
        self.assertEqual(result["status"], "committed")
        self.assertEqual(result["total"], 10)

    def test_crash_after_write_rolls_back_subprocess(self) -> None:
        for seq, value in ((1, 10), (2, 20)):
            proc = self._run(
                "--window", "3", "deliver",
                "--sender", "alice", "--seq", str(seq), "--value", str(value),
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)

        # Crash after the write but before COMMIT: SQLite rollback recovery
        # must leave seq=3 unapplied.
        proc = self._run(
            "--window", "3", "deliver",
            "--sender", "alice", "--seq", "3", "--value", "30",
            "--crash", "after_write",
        )
        self.assertEqual(proc.returncode, 99, proc.stderr)
        state = self._state("alice")
        self.assertEqual(state["cursor"], 2)
        self.assertEqual(state["total"], 30)
        self.assertEqual(state["pending"], [])

        # Retry succeeds and applies the value exactly once.
        proc = self._run(
            "--window", "3", "deliver",
            "--sender", "alice", "--seq", "3", "--value", "30",
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertEqual(json.loads(proc.stdout)["total"], 60)

    def test_crash_after_commit_preserves_effect_subprocess(self) -> None:
        for seq, value in ((1, 10), (2, 20)):
            proc = self._run(
                "--window", "3", "deliver",
                "--sender", "alice", "--seq", str(seq), "--value", str(value),
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)

        # Crash after the durable commit but before the client sees a reply.
        proc = self._run(
            "--window", "3", "deliver",
            "--sender", "alice", "--seq", "3", "--value", "30",
            "--crash", "after_commit",
        )
        self.assertEqual(proc.returncode, 99, proc.stderr)

        state = self._state("alice")
        self.assertEqual(state["cursor"], 3)
        self.assertEqual(state["total"], 60)

        # Replay is a no-op duplicate: total unchanged.
        proc = self._run(
            "--window", "3", "deliver",
            "--sender", "alice", "--seq", "3", "--value", "30",
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        result = json.loads(proc.stdout)
        self.assertEqual(result["status"], "duplicate")
        self.assertEqual(self._state("alice")["total"], 60)

    def test_crash_points_on_cached_message(self) -> None:
        # Pre-commit crash on a cached-only message leaves nothing behind.
        proc = self._run(
            "--window", "3", "deliver",
            "--sender", "bob", "--seq", "3", "--value", "300",
            "--crash", "after_write",
        )
        self.assertEqual(proc.returncode, 99, proc.stderr)
        state = self._state("bob")
        self.assertEqual(state["cursor"], 0)
        self.assertEqual(state["pending"], [])

        # Post-commit crash on the cached message keeps the cached row; a
        # replay with a different value is ignored and the original survives.
        proc = self._run(
            "--window", "3", "deliver",
            "--sender", "bob", "--seq", "3", "--value", "300",
            "--crash", "after_commit",
        )
        self.assertEqual(proc.returncode, 99, proc.stderr)
        state = self._state("bob")
        self.assertEqual(state["cursor"], 0)
        self.assertEqual(state["pending"], [{"seq": 3, "value": 300}])

        proc = self._run(
            "--window", "3", "deliver",
            "--sender", "bob", "--seq", "3", "--value", "999",
        )
        self.assertEqual(json.loads(proc.stdout)["status"], "duplicate")

        for seq, value in ((1, 100), (2, 200)):
            proc = self._run(
                "--window", "3", "deliver",
                "--sender", "bob", "--seq", str(seq), "--value", str(value),
            )
            self.assertEqual(proc.returncode, 0, proc.stderr)

        state = self._state("bob")
        self.assertEqual(state["cursor"], 3)
        self.assertEqual(state["total"], 600)
        self.assertEqual(state["pending"], [])

class CliBehaviorTest(unittest.TestCase):
    def setUp(self) -> None:
        self.tmp = temporary_directory = tempfile.TemporaryDirectory()
        self.db = os.path.join(temporary_directory.name, "inbox.db")
        self.env = dict(os.environ, PYTHONPATH=REPO_ROOT)

    def tearDown(self) -> None:
        self.tmp.cleanup()

    def _batch(self, lines: list[str]) -> subprocess.CompletedProcess:
        return subprocess.run(
            [sys.executable, "-m", "dedup_inbox",
             "--db", self.db, "--window", "3", "batch"],
            input="\n".join(lines) + "\n",
            capture_output=True,
            text=True,
            env=self.env,
            cwd=REPO_ROOT,
        )

    def test_acceptance_scenario_via_batch(self) -> None:
        proc = self._batch([
            json.dumps({"op": "deliver", "sender": "alice",
                        "seq": 3, "value": 30}),
            json.dumps({"op": "deliver", "sender": "alice",
                        "seq": 4, "value": 40}),
            json.dumps({"op": "deliver", "sender": "alice",
                        "seq": 1, "value": 10}),
            json.dumps({"op": "deliver", "sender": "alice",
                        "seq": 2, "value": 20}),
            json.dumps({"op": "deliver", "sender": "alice",
                        "seq": 1, "value": 10}),
            json.dumps({"op": "state", "senders": ["alice"]}),
        ])
        self.assertEqual(proc.returncode, 2, proc.stderr)
        results = [json.loads(line) for line in proc.stdout.splitlines()]
        self.assertEqual([r["status"] for r in results[:5]],
                         ["cached", "rejected", "committed",
                          "committed", "duplicate"])
        self.assertEqual(results[2]["committed_seqs"], [1])
        self.assertEqual(results[3]["committed_seqs"], [2, 3])
        self.assertEqual(results[4]["total"], 60)
        state = results[5]["states"][0]
        self.assertEqual(state["cursor"], 3)
        self.assertEqual(state["total"], 60)
        self.assertEqual(state["pending"], [])


if __name__ == "__main__":
    unittest.main()
