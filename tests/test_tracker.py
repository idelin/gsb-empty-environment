import unittest

from build_state.models import Artifact, BuildInput, Event, NodeSpec
from build_state.tracker import BuildTracker, NodeState
from build_state.versions import parse_version


def spec(node="n", package="pkg", version="1.0.0", artifacts=("a.bin",),
         require_commit=True):
    return NodeSpec(
        node=node,
        package=package,
        version=parse_version(version),
        required_artifacts=tuple(artifacts),
        require_commit=require_commit,
    )


def make_build(nodes):
    return BuildInput(nodes={s.node: s for s in nodes})


def ev(event_id, seq, attempt, node="n", kind="submitted", *,
       artifacts=(), commit=None, reason=None, package=None, version=None):
    if isinstance(artifacts, Artifact):
        artifacts = (artifacts,)
    return Event(
        event_id=event_id,
        seq=seq,
        attempt=attempt,
        node=node,
        kind=kind,
        timestamp=f"2026-09-21T00:{seq:02d}:00Z",
        package=package,
        version=parse_version(version) if version else None,
        artifacts=tuple(artifacts),
        commit=commit,
        reason=reason,
    )


def art(name="a.bin", package="pkg", version="1.0.0", sha="sha-1"):
    return Artifact(name=name, package=package,
                    version=parse_version(version), sha256=sha)


def run(events, nodes=None):
    build = make_build(nodes or [spec()])
    tracker = BuildTracker(build)
    tracker.ingest(events)
    return tracker, tracker.reconstruct()


class SuccessEvidenceTests(unittest.TestCase):
    def test_complete_evidence_is_success(self):
        tracker, statuses = run([
            ev("s", 0, 1),
            ev("a1", 1, 1, kind="artifact_written", artifacts=(art(),)),
            ev("c1", 2, 1, kind="committed", commit="c-1"),
            ev("ok", 3, 1, kind="succeeded"),
        ])
        self.assertEqual(statuses["n"].state, NodeState.SUCCESS)
        self.assertEqual(tracker.as_report(statuses)["unresolved_nodes"], [])

    def test_success_without_artifact_is_pending(self):
        _, statuses = run([
            ev("s", 0, 1),
            ev("c1", 2, 1, kind="committed", commit="c-1"),
            ev("ok", 3, 1, kind="succeeded"),
        ])
        status = statuses["n"]
        self.assertEqual(status.state, NodeState.PENDING)
        self.assertEqual(status.missing_artifacts, ("a.bin",))
        self.assertFalse(status.missing_commit)

    def test_success_without_commit_is_pending(self):
        _, statuses = run([
            ev("s", 0, 1),
            ev("a1", 1, 1, kind="artifact_written", artifacts=(art(),)),
            ev("ok", 3, 1, kind="succeeded"),
        ])
        status = statuses["n"]
        self.assertEqual(status.state, NodeState.PENDING)
        self.assertEqual(status.missing_artifacts, ())
        self.assertTrue(status.missing_commit)

    def test_success_missing_one_of_two_artifacts_is_pending(self):
        node_spec = spec(artifacts=("a.bin", "b.bin"))
        tracker, statuses = run([
            ev("s", 0, 1),
            ev("a1", 1, 1, kind="artifact_written", artifacts=(art("a.bin"),)),
            ev("c1", 2, 1, kind="committed", commit="c-1"),
            ev("ok", 3, 1, kind="succeeded"),
        ], nodes=[node_spec])
        status = statuses["n"]
        self.assertEqual(status.state, NodeState.PENDING)
        self.assertEqual(status.missing_artifacts, ("b.bin",))

    def test_commit_optional_node_succeeds_without_commit(self):
        node_spec = spec(require_commit=False)
        tracker, statuses = run([
            ev("s", 0, 1),
            ev("a1", 1, 1, kind="artifact_written", artifacts=(art(),)),
            ev("ok", 3, 1, kind="succeeded"),
        ], nodes=[node_spec])
        self.assertEqual(statuses["n"].state, NodeState.SUCCESS)

    def test_no_events_is_pending_with_all_evidence_missing(self):
        _, statuses = run([])
        status = statuses["n"]
        self.assertEqual(status.state, NodeState.PENDING)
        self.assertIsNone(status.attempt)
        self.assertEqual(status.missing_artifacts, ("a.bin",))
        self.assertTrue(status.missing_commit)


class FailureTests(unittest.TestCase):
    def test_failure_is_terminal_without_artifacts(self):
        _, statuses = run([
            ev("s", 0, 1),
            ev("f", 1, 1, kind="failed", reason="boom"),
        ])
        status = statuses["n"]
        self.assertEqual(status.state, NodeState.FAILED)
        self.assertEqual(status.reason, "boom")
        self.assertEqual(status.missing_artifacts, ())

    def test_later_success_recovers_from_failure(self):
        _, statuses = run([
            ev("s", 0, 1),
            ev("f", 1, 1, kind="failed", reason="boom"),
            ev("a1", 2, 1, kind="artifact_written", artifacts=(art(),)),
            ev("c1", 3, 1, kind="committed", commit="c-1"),
            ev("ok", 4, 1, kind="succeeded"),
        ])
        self.assertEqual(statuses["n"].state, NodeState.SUCCESS)

    def test_later_failure_supersedes_success(self):
        _, statuses = run([
            ev("s", 0, 1),
            ev("a1", 1, 1, kind="artifact_written", artifacts=(art(),)),
            ev("c1", 2, 1, kind="committed", commit="c-1"),
            ev("ok", 3, 1, kind="succeeded"),
            ev("f", 4, 1, kind="failed", reason="rollback"),
        ])
        status = statuses["n"]
        self.assertEqual(status.state, NodeState.FAILED)
        self.assertEqual(status.reason, "rollback")


class AttemptTests(unittest.TestCase):
    def _old_success_events(self):
        return [
            ev("s1", 0, 1),
            ev("a1", 1, 1, kind="artifact_written", artifacts=(art(sha="old"))),
            ev("c1", 2, 1, kind="committed", commit="c-old"),
            ev("ok1", 3, 1, kind="succeeded"),
        ]

    def test_old_attempt_failure_cannot_override_new_attempt_success(self):
        # Attempt 1 failed; a full retry on attempt 2 succeeds. The attempt 1
        # failure (even at a high seq, arriving out of order) cannot override
        # the newer attempt's success.
        events = [
            ev("f1", 9, 1, kind="failed", reason="stale failure arriving late"),
            ev("s2", 0, 2),
            ev("a2", 1, 2, kind="artifact_written", artifacts=art(sha="new")),
            ev("c2", 2, 2, kind="committed", commit="c-new"),
            ev("ok2", 3, 2, kind="succeeded"),
            ev("s1", 0, 1),
        ]
        _, statuses = run(events)
        self.assertEqual(statuses["n"].state, NodeState.SUCCESS)
        self.assertEqual(statuses["n"].attempt, 2)

    def test_new_attempt_starts_without_old_evidence(self):
        events = self._old_success_events() + [
            ev("s2", 0, 2),
            ev("ok2", 3, 2, kind="succeeded"),
        ]
        _, statuses = run(events)
        status = statuses["n"]
        self.assertEqual(status.state, NodeState.PENDING)
        self.assertEqual(status.attempt, 2)
        self.assertEqual(status.missing_artifacts, ("a.bin",))
        self.assertTrue(status.missing_commit)
        self.assertEqual(status.observed_attempts, (1, 2))

    def test_new_attempt_failure_overrides_old_success(self):
        events = self._old_success_events() + [
            ev("s2", 0, 2),
            ev("f2", 1, 2, kind="failed", reason="new failure"),
        ]
        _, statuses = run(events)
        status = statuses["n"]
        self.assertEqual(status.state, NodeState.FAILED)
        self.assertEqual(status.attempt, 2)
        self.assertEqual(status.reason, "new failure")


class IdempotencyTests(unittest.TestCase):
    def test_duplicate_event_id_is_ignored(self):
        original = ev("a1", 1, 1, kind="artifact_written", artifacts=(art(sha="good")))
        duplicate = ev("a1", 1, 1, kind="artifact_written",
                       artifacts=(art(sha="CONFLICT")))
        tracker, statuses = run([
            ev("s", 0, 1),
            original,
            duplicate,
            ev("c1", 2, 1, kind="committed", commit="c-1"),
            ev("ok", 3, 1, kind="succeeded"),
        ])
        self.assertEqual(tracker.duplicate_event_ids, ["a1"])
        self.assertEqual(statuses["n"].state, NodeState.SUCCESS)
        self.assertFalse(any("conflicts" in a.message for a in tracker.anomalies))

    def test_repeated_submission_does_not_reset(self):
        tracker, statuses = run([
            ev("s", 0, 1),
            ev("a1", 1, 1, kind="artifact_written", artifacts=(art(),)),
            ev("s-dup", 0, 1),  # different event_id, same attempt
        ])
        # Artifact evidence must survive the duplicate submission.
        self.assertEqual(statuses["n"].state, NodeState.PENDING)  # no terminal event
        self.assertEqual(statuses["n"].missing_artifacts, ())
        self.assertTrue(any("duplicate submission" in a.message
                            for a in tracker.anomalies))

    def test_same_events_fed_twice_are_idempotent(self):
        events = [
            ev("s", 0, 1),
            ev("a1", 1, 1, kind="artifact_written", artifacts=(art(),)),
        ]
        tracker = BuildTracker(make_build([spec()]))
        tracker.ingest(events)
        tracker.ingest(events)
        self.assertEqual(tracker.ingested_count, 2)
        self.assertEqual(len(tracker.duplicate_event_ids), 2)
        statuses = tracker.reconstruct()
        self.assertEqual(statuses["n"].state, NodeState.PENDING)


class OrderingAndAnomalyTests(unittest.TestCase):
    def _complete_events(self):
        return [
            ev("ok", 3, 1, kind="succeeded"),
            ev("c1", 2, 1, kind="committed", commit="c-1"),
            ev("a1", 1, 1, kind="artifact_written", artifacts=(art(),)),
            ev("s", 0, 1),
        ]

    def test_result_is_order_independent(self):
        ordered = self._complete_events()
        reversed_events = list(reversed(ordered))
        shuffled = [ordered[2], ordered[0], ordered[3], ordered[1]]
        results = []
        for batch in (ordered, reversed_events, shuffled):
            _, statuses = run(batch)
            results.append(statuses["n"].state)
        self.assertEqual(results, [NodeState.SUCCESS] * 3)

    def test_orphan_event_is_ignored_and_flagged(self):
        tracker, statuses = run([
            ev("x1", 0, 1, node="ghost"),
            ev("s", 0, 1),
        ])
        self.assertNotIn("ghost", statuses)
        self.assertEqual([e.event_id for e in tracker.orphan_events], ["x1"])

    def test_wrong_package_or_version_artifact_rejected(self):
        wrong_package = art(package="other")
        wrong_version = art(version="2.0.0", sha="sha-2")
        tracker, statuses = run([
            ev("s", 0, 1),
            ev("bad-pkg", 1, 1, kind="artifact_written",
               artifacts=(wrong_package,)),
            ev("bad-ver", 2, 1, kind="artifact_written",
               artifacts=(wrong_version,)),
            ev("ok", 3, 1, kind="succeeded"),
        ])
        status = statuses["n"]
        self.assertEqual(status.state, NodeState.PENDING)
        self.assertEqual(status.missing_artifacts, ("a.bin",))
        self.assertEqual(len(tracker.anomalies), 2)

    def test_extra_artifact_is_anomaly_not_evidence(self):
        node_spec = spec(artifacts=("a.bin",))
        tracker, statuses = run([
            ev("s", 0, 1),
            ev("a1", 1, 1, kind="artifact_written", artifacts=(art(),)),
            ev("extra", 2, 1, kind="artifact_written",
                artifacts=(art("extra.bin", sha="x"),)),
            ev("c1", 3, 1, kind="committed", commit="c-1"),
            ev("ok", 4, 1, kind="succeeded"),
        ], nodes=[node_spec])
        self.assertEqual(statuses["n"].state, NodeState.SUCCESS)
        self.assertTrue(any("not in required artifacts" in a.message
                            for a in tracker.anomalies))

    def test_conflicting_sha_keeps_first(self):
        tracker, statuses = run([
            ev("s", 0, 1),
            ev("a1", 1, 1, kind="artifact_written", artifacts=(art(sha="first"))),
            ev("a2", 2, 1, kind="artifact_written", artifacts=(art(sha="second"))),
            ev("c1", 3, 1, kind="committed", commit="c-1"),
            ev("ok", 4, 1, kind="succeeded"),
        ])
        self.assertEqual(statuses["n"].state, NodeState.SUCCESS)
        self.assertTrue(any("conflicts" in a.message for a in tracker.anomalies))

    def test_conflicting_commits_flagged(self):
        tracker, statuses = run([
            ev("s", 0, 1),
            ev("a1", 1, 1, kind="artifact_written", artifacts=(art(),)),
            ev("c1", 2, 1, kind="committed", commit="c-1"),
            ev("c2", 3, 1, kind="committed", commit="c-OTHER"),
            ev("ok", 4, 1, kind="succeeded"),
        ])
        self.assertEqual(statuses["n"].state, NodeState.SUCCESS)
        self.assertTrue(any("conflicting commit" in a.message
                            for a in tracker.anomalies))

    def test_terminal_tie_is_ambiguous_pending(self):
        _, statuses = run([
            ev("s", 0, 1),
            ev("tie-ok", 5, 1, kind="succeeded"),
            ev("tie-fail", 5, 1, kind="failed", reason="tie"),
        ])
        self.assertEqual(statuses["n"].state, NodeState.PENDING)


if __name__ == "__main__":
    unittest.main()
