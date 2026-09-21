import hashlib
import unittest

from buildstate.events import Artifact
from buildstate.tracker import (
    FAILED_STATE,
    PENDING,
    SUCCESS,
    BuildTracker,
)

from ._helpers import event, make_definition


def digest(text):
    return hashlib.sha256(text.encode()).hexdigest()


def full_success_events(prefix="e", artifact_digest=digest("a"), attempt=1):
    return [
        event(f"{prefix}-start", attempt=attempt, kind="started"),
        event(
            f"{prefix}-art",
            attempt=attempt,
            kind="artifacts_written",
            artifacts=(Artifact("a.bin", artifact_digest),),
        ),
        event(f"{prefix}-submit", attempt=attempt, kind="submitted"),
    ]


class ReconstructionTests(unittest.TestCase):
    def setUp(self):
        self.definition = make_definition()

    def test_full_evidence_succeeds(self):
        tracker = BuildTracker(self.definition)
        tracker.ingest_many(full_success_events())
        snapshot = tracker.snapshot()
        self.assertEqual(snapshot.nodes[0].state, SUCCESS)
        self.assertTrue(snapshot.is_success)

    def test_out_of_order_events_succeed(self):
        tracker = BuildTracker(self.definition)
        ordered = full_success_events()
        tracker.ingest_many(reversed(ordered))
        self.assertEqual(tracker.snapshot().nodes[0].state, SUCCESS)

    def test_submitted_without_required_artifact_is_pending(self):
        tracker = BuildTracker(self.definition)
        tracker.ingest(event("s", kind="started"))
        tracker.ingest(event("sub", kind="submitted"))
        node = tracker.snapshot().nodes[0]
        self.assertEqual(node.state, PENDING)
        self.assertTrue(any(item.kind == "artifact" for item in node.missing))
        self.assertFalse(node.is_terminal)

    def test_submitted_without_started_is_pending(self):
        tracker = BuildTracker(self.definition)
        tracker.ingest(
            event("art", kind="artifacts_written",
                  artifacts=(Artifact("a.bin", digest("a")),))
        )
        tracker.ingest(event("sub", kind="submitted"))
        node = tracker.snapshot().nodes[0]
        self.assertEqual(node.state, PENDING)
        self.assertTrue(any(item.kind == "started" for item in node.missing))

    def test_no_events_is_pending_without_guessing(self):
        tracker = BuildTracker(self.definition)
        node = tracker.snapshot().nodes[0]
        self.assertEqual(node.state, PENDING)
        self.assertIsNone(node.selected_attempt)

    def test_failed_is_terminal_even_without_artifacts(self):
        tracker = BuildTracker(self.definition)
        tracker.ingest(event("s", kind="started"))
        tracker.ingest(event("f", kind="failed", reason="compile error"))
        node = tracker.snapshot().nodes[0]
        self.assertEqual(node.state, FAILED_STATE)
        self.assertTrue(node.is_terminal)
        self.assertEqual(node.attempts[0].failure_reason, "compile error")

    def test_old_failure_does_not_overwrite_new_success(self):
        tracker = BuildTracker(self.definition)
        tracker.ingest(event("f1-s", attempt=1, kind="started"))
        tracker.ingest(event("f1", attempt=1, kind="failed", reason="old"))
        tracker.ingest_many(full_success_events(prefix="a2", attempt=2))
        node = tracker.snapshot().nodes[0]
        self.assertEqual(node.state, SUCCESS)
        self.assertEqual(node.selected_attempt, 2)
        self.assertEqual(node.attempts[0].state, FAILED_STATE)

    def test_new_attempt_pending_ignores_old_success(self):
        tracker = BuildTracker(self.definition)
        tracker.ingest_many(full_success_events(prefix="a1", attempt=1))
        tracker.ingest(event("a2-s", attempt=2, kind="started"))
        node = tracker.snapshot().nodes[0]
        self.assertEqual(node.state, PENDING)
        self.assertEqual(node.selected_attempt, 2)

    def test_late_failure_for_old_attempt_keeps_new_success(self):
        tracker = BuildTracker(self.definition)
        tracker.ingest_many(full_success_events(prefix="a2", attempt=2))
        # Failure arrives much later for the older attempt 1, out of order.
        tracker.ingest(event("f1-s", attempt=1, kind="started"))
        tracker.ingest(event("f1", attempt=1, kind="failed", reason="late"))
        node = tracker.snapshot().nodes[0]
        self.assertEqual(node.state, SUCCESS)
        self.assertEqual(node.selected_attempt, 2)

    def test_duplicate_submission_is_idempotent(self):
        tracker = BuildTracker(self.definition)
        tracker.ingest_many(full_success_events())
        submit = event("e-submit", kind="submitted")
        self.assertFalse(tracker.ingest(submit))
        self.assertFalse(tracker.ingest(submit))
        snapshot = tracker.snapshot()
        self.assertEqual(snapshot.nodes[0].state, SUCCESS)
        self.assertEqual(
            sum(1 for e in tracker._events.values() if e.kind == "submitted"), 1
        )

    def test_conflicting_duplicate_event_id_warns_and_keeps_first(self):
        tracker = BuildTracker(self.definition)
        first = event("dup", kind="started", timestamp="t1")
        second = event("dup", kind="started", timestamp="t2")
        self.assertTrue(tracker.ingest(first))
        self.assertFalse(tracker.ingest(second))
        self.assertTrue(
            any("conflicting" in w for w in tracker.snapshot().warnings)
        )
        self.assertEqual(tracker._events[first.identity].timestamp, "t1")

    def test_replay_all_events_is_idempotent(self):
        tracker = BuildTracker(self.definition)
        events = full_success_events()
        tracker.ingest_many(events)
        added = tracker.ingest_many(events)
        self.assertEqual(added, 0)
        self.assertEqual(len(tracker._events), 3)
        self.assertEqual(tracker.snapshot().nodes[0].state, SUCCESS)

    def test_conflicting_artifact_hashes_keep_attempt_pending(self):
        tracker = BuildTracker(self.definition)
        tracker.ingest(event("s", kind="started"))
        tracker.ingest(
            event("art1", kind="artifacts_written",
                  artifacts=(Artifact("a.bin", digest("one")),))
        )
        tracker.ingest(
            event("art2", kind="artifacts_written",
                  artifacts=(Artifact("a.bin", digest("two")),))
        )
        tracker.ingest(event("sub", kind="submitted"))
        node = tracker.snapshot().nodes[0]
        self.assertEqual(node.state, PENDING)
        self.assertTrue(
            any(item.kind == "artifact_conflict" for item in node.missing)
        )

    def test_submitted_and_failed_same_attempt_is_pending_contradiction(self):
        tracker = BuildTracker(self.definition)
        tracker.ingest_many(full_success_events())
        tracker.ingest(event("e-fail", kind="failed", reason="but also failed"))
        node = tracker.snapshot().nodes[0]
        self.assertEqual(node.state, PENDING)
        self.assertEqual(node.missing[0].kind, "contradiction")

    def test_unknown_node_event_warns_and_ignored(self):
        tracker = BuildTracker(self.definition)
        self.assertFalse(
            tracker.ingest(event("x", node="ghost", kind="started"))
        )
        self.assertIn("unknown node", tracker.snapshot().warnings[0])

    def test_unrequired_artifact_warns_but_success_still_possible(self):
        tracker = BuildTracker(self.definition)
        tracker.ingest(event("s", kind="started"))
        tracker.ingest(
            event("art", kind="artifacts_written", artifacts=(
                Artifact("a.bin", digest("a")),
                Artifact("extra.log", digest("x")),
            ))
        )
        tracker.ingest(event("sub", kind="submitted"))
        snapshot = tracker.snapshot()
        self.assertEqual(snapshot.nodes[0].state, SUCCESS)
        self.assertTrue(any("not required" in w for w in snapshot.warnings))

    def test_snapshot_is_arrival_order_independent(self):
        definition = make_definition(nodes={"b": ("a.bin",)})
        events = [
            event("s", node="b", kind="started"),
            event("a", node="b", kind="artifacts_written",
                  artifacts=(Artifact("a.bin", digest("a")),)),
            event("u", node="b", kind="submitted"),
        ]
        first = BuildTracker(definition)
        first.ingest_many(events)
        second = BuildTracker(definition)
        second.ingest_many(list(reversed(events)))
        self.assertEqual(
            [(n.node, n.state) for n in first.snapshot().nodes],
            [(n.node, n.state) for n in second.snapshot().nodes],
        )


if __name__ == "__main__":
    unittest.main()
