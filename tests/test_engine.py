import random
import unittest

from ci_state.engine import BuildEngine, NodeState
from ci_state.events import Event


def ev(event_id, kind, node, attempt=1, ts=1.0, **extra):
    data = {"id": event_id, "kind": kind, "node": node,
            "attempt": attempt, "ts": ts}
    data.update(extra)
    return Event.from_dict(data)


STARTED = "node.started"
WRITTEN = "artifact.written"
COMMITTED = "artifact.committed"
SUCCEEDED = "node.succeeded"
FAILED = "node.failed"


def complete_success(prefix, node, artifacts, attempt=1, base=100):
    out = [ev(f"{prefix}s", STARTED, node, attempt, base)]
    for i, art in enumerate(artifacts):
        out.append(ev(f"{prefix}w{i}", WRITTEN, node, attempt, base + 1 + i,
                      artifact=art))
    for i, art in enumerate(artifacts):
        out.append(ev(f"{prefix}c{i}", COMMITTED, node, attempt,
                      base + 10 + i, artifact=art))
    out.append(ev(f"{prefix}ok", SUCCEEDED, node, attempt, base + 20))
    return out


class EngineTest(unittest.TestCase):
    def setUp(self):
        self.engine = BuildEngine({
            "a": frozenset(["out/a.bin"]),
            "b": frozenset(["out/b1.bin", "out/b2.bin"]),
        })

    def test_full_evidence_succeeds(self):
        self.engine.apply_many(complete_success("a", "a", ["out/a.bin"]))
        report = self.engine.report_node("a")
        self.assertIs(report.state, NodeState.SUCCESS)
        self.assertEqual(report.missing, [])

    def test_success_without_artifacts_is_pending(self):
        # A success event alone must never be treated as success.
        self.engine.apply(ev("a1", STARTED, "a"))
        self.engine.apply(ev("a2", SUCCEEDED, "a"))
        report = self.engine.report_node("a")
        self.assertIs(report.state, NodeState.PENDING)
        self.assertTrue(any("artifact.written" in m for m in report.missing))
        self.assertTrue(any("artifact.committed" in m for m in report.missing))

    def test_written_but_not_committed_is_pending(self):
        events = [ev("s", STARTED, "a"),
                  ev("w", WRITTEN, "a", artifact="out/a.bin"),
                  ev("ok", SUCCEEDED, "a")]
        self.engine.apply_many(events)
        report = self.engine.report_node("a")
        self.assertIs(report.state, NodeState.PENDING)
        self.assertTrue(any("artifact.committed" in m for m in report.missing))

    def test_committed_without_written_is_pending(self):
        events = [ev("s", STARTED, "a"),
                  ev("c", COMMITTED, "a", artifact="out/a.bin"),
                  ev("ok", SUCCEEDED, "a")]
        self.engine.apply_many(events)
        report = self.engine.report_node("a")
        self.assertIs(report.state, NodeState.PENDING)
        self.assertTrue(any("artifact.written" in m for m in report.missing))

    def test_missing_succeeded_event_is_pending(self):
        events = [ev("s", STARTED, "a"),
                  ev("w", WRITTEN, "a", artifact="out/a.bin"),
                  ev("c", COMMITTED, "a", artifact="out/a.bin")]
        self.engine.apply_many(events)
        self.assertIs(self.engine.report_node("a").state, NodeState.PENDING)

    def test_failure_terminal(self):
        self.engine.apply_many([ev("s", STARTED, "a"),
                                ev("f", FAILED, "a", reason="boom")])
        report = self.engine.report_node("a")
        self.assertIs(report.state, NodeState.FAILED)
        self.assertEqual(report.fail_reason, "boom")

    def test_no_events_is_pending_not_guessed(self):
        report = self.engine.report_node("b")
        self.assertIs(report.state, NodeState.PENDING)
        self.assertEqual(report.observed_attempts, [])

    def test_all_required_artifacts_needed(self):
        events = complete_success("b", "b", ["out/b1.bin", "out/b2.bin"])
        # Drop the commit for b2: must stay pending.
        events = [e for e in events if not (
            e.kind == COMMITTED and "out/b2.bin" in e.artifacts)]
        self.engine.apply_many(events)
        report = self.engine.report_node("b")
        self.assertIs(report.state, NodeState.PENDING)
        self.assertTrue(any("out/b2.bin" in m and "committed" in m
                            for m in report.missing))

    def test_duplicate_events_are_idempotent(self):
        events = complete_success("a", "a", ["out/a.bin"])
        self.engine.apply_many(events)
        before = self.engine.report_node("a")
        # Re-apply every event; counts must not change.
        applied = self.engine.apply_many(events)
        self.assertEqual(applied, 0)
        after = self.engine.report_node("a")
        self.assertEqual(after.state, before.state)
        self.assertEqual(sorted(after.event_ids), sorted(before.event_ids))
        self.assertEqual(len(after.event_ids), len(set(after.event_ids)))

    def test_out_of_order_processing_gives_same_state(self):
        events = complete_success("b", "b", ["out/b1.bin", "out/b2.bin"])
        reference = BuildEngine({
            "a": frozenset(["out/a.bin"]),
            "b": frozenset(["out/b1.bin", "out/b2.bin"]),
        })
        reference.apply_many(events)

        shuffled = list(events)
        random.Random(42).shuffle(shuffled)
        self.engine.apply_many(shuffled)
        for node in ("a", "b"):
            self.assertEqual(
                self.engine.report_node(node).state,
                reference.report_node(node).state,
            )

    def test_old_attempt_failure_cannot_override_new_success(self):
        # New attempt succeeds fully first...
        self.engine.apply_many(complete_success("a", "a", ["out/a.bin"],
                                                attempt=2, base=200))
        self.assertIs(self.engine.report_node("a").state, NodeState.SUCCESS)
        # ...then a failure from attempt 1 arrives late (and out of order).
        self.engine.apply(ev("old-f", FAILED, "a", attempt=1, ts=999,
                             reason="stale replay"))
        report = self.engine.report_node("a")
        self.assertIs(report.state, NodeState.SUCCESS)
        self.assertEqual(report.attempt, 2)
        self.assertEqual(report.observed_attempts, [1, 2])

    def test_new_failure_after_old_success_is_failed(self):
        self.engine.apply_many(complete_success("a", "a", ["out/a.bin"],
                                                attempt=1, base=100))
        self.engine.apply(ev("f2", FAILED, "a", attempt=2, reason="regression"))
        report = self.engine.report_node("a")
        self.assertIs(report.state, NodeState.FAILED)
        self.assertEqual(report.attempt, 2)

    def test_new_attempt_in_progress_is_pending_even_if_old_failed(self):
        self.engine.apply(ev("f1", FAILED, "a", attempt=1, reason="old"))
        self.engine.apply(ev("s2", STARTED, "a", attempt=2))
        report = self.engine.report_node("a")
        self.assertIs(report.state, NodeState.PENDING)
        self.assertIn("missing node.succeeded event", report.missing)

    def test_contradictory_duplicate_id_rejected(self):
        self.engine.apply(ev("x", STARTED, "a", attempt=1, ts=1))
        with self.assertRaises(ValueError):
            self.engine.apply(ev("x", STARTED, "a", attempt=2, ts=1))

    def test_unknown_node_rejected(self):
        with self.assertRaises(ValueError):
            self.engine.apply(ev("x", STARTED, "ghost"))

    def test_undecided_nodes_lists_only_pending(self):
        self.engine.apply_many(complete_success("a", "a", ["out/a.bin"]))
        undecided = {r.node for r in self.engine.undecided_nodes()}
        self.assertEqual(undecided, {"b"})

    def test_multi_artifact_batch_commit(self):
        events = [
            ev("s", STARTED, "b"),
            ev("w1", WRITTEN, "b", artifact="out/b1.bin"),
            ev("w2", WRITTEN, "b", artifact="out/b2.bin"),
            Event.from_dict({"id": "c", "kind": COMMITTED, "node": "b",
                             "attempt": 1, "ts": 5,
                             "artifacts": ["out/b1.bin", "out/b2.bin"]}),
            ev("ok", SUCCEEDED, "b"),
        ]
        self.engine.apply_many(events)
        self.assertIs(self.engine.report_node("b").state, NodeState.SUCCESS)


class EventValidationTest(unittest.TestCase):
    def test_bad_event_payloads(self):
        bad = [
            {},
            {"id": "x", "kind": "weird", "node": "a", "attempt": 1, "ts": 1},
            {"id": "x", "kind": STARTED, "node": "a", "attempt": 0, "ts": 1},
            {"id": "x", "kind": STARTED, "node": "a", "attempt": True, "ts": 1},
            {"id": "x", "kind": WRITTEN, "node": "a", "attempt": 1, "ts": 1},
            {"id": "", "kind": STARTED, "node": "a", "attempt": 1, "ts": 1},
        ]
        for payload in bad:
            with self.subTest(payload=payload):
                with self.assertRaises(Exception):
                    Event.from_dict(payload)


if __name__ == "__main__":
    unittest.main()
