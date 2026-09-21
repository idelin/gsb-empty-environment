import json
import random
import tempfile
import unittest
from pathlib import Path

from buildstate import (
    BuildState,
    Event,
    EventError,
    FAILED,
    PENDING,
    SUCCEEDED,
    Version,
    VersionError,
    load_events,
    load_packages,
)
from buildstate.loader import LoadError


def ev(event_id, etype, node, attempt, **payload):
    return Event.from_dict(
        {
            "event_id": event_id,
            "type": etype,
            "node": node,
            "attempt": attempt,
            "payload": payload,
        }
    )


def make_spec(package="pkg", version="1.0.0", artifacts=("a.tar.gz", "a.whl")):
    from buildstate import NodeSpec

    return NodeSpec(
        package=package,
        version=Version.parse(version),
        required_artifacts=tuple(artifacts),
    )


def full_success_events(node, attempt, artifacts=("a.tar.gz", "a.whl"), prefix="e"):
    events = [
        ev(f"{prefix}-start", "node_started", node, attempt),
        ev(f"{prefix}-ok", "build_succeeded", node, attempt),
        ev(f"{prefix}-commit", "committed", node, attempt, commit="abc123"),
    ]
    for i, artifact in enumerate(artifacts):
        events.append(
            ev(f"{prefix}-art{i}", "artifact_written", node, attempt, artifact=artifact)
        )
    return events


class VersionTests(unittest.TestCase):
    def test_plain_version(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertIsNone(v.prerelease)
        self.assertEqual(str(v), "1.2.3")

    def test_prerelease_version(self):
        v = Version.parse("0.9.0-rc.1")
        self.assertEqual(v.prerelease, "rc.1")
        self.assertEqual(str(v), "0.9.0-rc.1")

    def test_invalid_versions_rejected(self):
        for bad in ["1.2", "1.2.3.4", "v1.2.3", "1.02.3", "1.2.3-", "1.2.3+build", "", 123]:
            with self.assertRaises(VersionError, msg=repr(bad)):
                Version.parse(bad)


class EventValidationTests(unittest.TestCase):
    def test_unknown_type_rejected(self):
        with self.assertRaises(EventError):
            ev("x", "exploded", "pkg@1.0.0", 1)

    def test_attempt_must_be_positive_int(self):
        for bad in [0, -1, "1", 1.5, True]:
            with self.assertRaises(EventError, msg=repr(bad)):
                ev("x", "node_started", "pkg@1.0.0", bad)

    def test_artifact_event_requires_artifact(self):
        with self.assertRaises(EventError):
            ev("x", "artifact_written", "pkg@1.0.0", 1)

    def test_commit_event_requires_commit(self):
        with self.assertRaises(EventError):
            ev("x", "committed", "pkg@1.0.0", 1)


class ReducerTests(unittest.TestCase):
    def setUp(self):
        self.spec = make_spec()
        self.node = self.spec.node_id

    def build(self, events, specs=None):
        state = BuildState(specs or [self.spec])
        state.apply_all(events)
        return state

    def test_success_with_complete_evidence(self):
        state = self.build(full_success_events(self.node, 1))
        status = state.node_status(self.node)
        self.assertEqual(status.status, SUCCEEDED)
        self.assertEqual(status.attempt, 1)
        self.assertEqual(status.missing, ())
        self.assertEqual(status.evidence["commits"], ["abc123"])
        self.assertEqual(sorted(status.evidence["artifacts"]), ["a.tar.gz", "a.whl"])

    def test_success_missing_one_artifact_is_pending(self):
        events = [
            ev("e1", "node_started", self.node, 1),
            ev("e2", "build_succeeded", self.node, 1),
            ev("e3", "artifact_written", self.node, 1, artifact="a.tar.gz"),
            ev("e4", "committed", self.node, 1, commit="abc123"),
        ]
        status = self.build(events).node_status(self.node)
        self.assertEqual(status.status, PENDING)
        self.assertEqual(status.missing, ("a.whl",))
        self.assertIn("evidence incomplete", status.reason)

    def test_success_missing_commit_is_pending(self):
        events = [
            ev("e1", "build_succeeded", self.node, 1),
            ev("e2", "artifact_written", self.node, 1, artifact="a.tar.gz"),
            ev("e3", "artifact_written", self.node, 1, artifact="a.whl"),
        ]
        status = self.build(events).node_status(self.node)
        self.assertEqual(status.status, PENDING)
        self.assertIn("<commit>", status.missing)

    def test_old_attempt_failure_does_not_override_new_success(self):
        events = full_success_events(self.node, 2, prefix="n")
        events.append(ev("o1", "build_failed", self.node, 1, reason="compiler crash"))
        status = self.build(events).node_status(self.node)
        self.assertEqual(status.status, SUCCEEDED)
        self.assertEqual(status.attempt, 2)

    def test_latest_attempt_failure_is_terminal(self):
        events = full_success_events(self.node, 1, prefix="o")
        events += [
            ev("n1", "node_started", self.node, 2),
            ev("n2", "build_failed", self.node, 2, reason="unit tests failed"),
        ]
        status = self.build(events).node_status(self.node)
        self.assertEqual(status.status, FAILED)
        self.assertEqual(status.attempt, 2)
        self.assertIn("unit tests failed", status.reason)

    def test_new_attempt_supersedes_old_success(self):
        events = full_success_events(self.node, 1, prefix="o")
        events.append(ev("n1", "node_started", self.node, 2))
        status = self.build(events).node_status(self.node)
        self.assertEqual(status.status, PENDING)
        self.assertEqual(status.attempt, 2)

    def test_contradictory_same_attempt_is_failed(self):
        events = full_success_events(self.node, 1)
        events.append(ev("x1", "build_failed", self.node, 1, reason="flaky reporter"))
        status = self.build(events).node_status(self.node)
        self.assertEqual(status.status, FAILED)
        self.assertIn("contradictory", status.reason)

    def test_duplicate_event_ids_are_idempotent(self):
        events = full_success_events(self.node, 1)
        doubled = events + list(events)  # same event_ids submitted twice
        state = self.build(doubled)
        status = state.node_status(self.node)
        self.assertEqual(status.status, SUCCEEDED)
        self.assertEqual(len(status.evidence["commits"]), 1)
        self.assertEqual(len(status.evidence["artifacts"]), 2)
        self.assertEqual(len(state.duplicate_event_ids), len(events))

    def test_out_of_order_events_same_result(self):
        events = full_success_events(self.node, 1)
        reference = self.build(events).node_status(self.node)
        for seed in range(20):
            shuffled = list(events)
            random.Random(seed).shuffle(shuffled)
            self.assertEqual(self.build(shuffled).node_status(self.node), reference)

    def test_no_events_is_pending(self):
        status = self.build([]).node_status(self.node)
        self.assertEqual(status.status, PENDING)
        self.assertIsNone(status.attempt)

    def test_started_only_is_pending(self):
        status = self.build([ev("e1", "node_started", self.node, 1)]).node_status(self.node)
        self.assertEqual(status.status, PENDING)

    def test_unknown_node_events_ignored_with_warning(self):
        events = [ev("e1", "node_started", "ghost@9.9.9", 1)]
        state = self.build(events)
        self.assertEqual(len(state.warnings), 1)
        self.assertEqual(state.node_status(self.node).status, PENDING)

    def test_report_lists_pending_nodes(self):
        other = make_spec(package="lazy", version="2.0.0", artifacts=("l.tar.gz",))
        state = self.build(full_success_events(self.node, 1), specs=[self.spec, other])
        report = state.report()
        self.assertEqual(report["pending"], ["lazy@2.0.0"])
        self.assertEqual(report["summary"]["succeeded"], 1)
        self.assertEqual(report["summary"]["pending"], 1)


class LoaderTests(unittest.TestCase):
    def write(self, directory, name, data):
        path = Path(directory) / name
        path.write_text(json.dumps(data), encoding="utf-8")
        return path

    def test_load_packages_and_events_roundtrip(self):
        with tempfile.TemporaryDirectory() as tmp:
            pkg_path = self.write(
                tmp,
                "packages.json",
                {
                    "nodes": [
                        {
                            "package": "alpha",
                            "version": "1.2.0-rc.1",
                            "required_artifacts": ["a.tar.gz"],
                        }
                    ]
                },
            )
            evt_path = self.write(
                tmp,
                "events.json",
                {
                    "events": [
                        {
                            "event_id": "e1",
                            "type": "node_started",
                            "node": "alpha@1.2.0-rc.1",
                            "attempt": 1,
                        }
                    ]
                },
            )
            specs = load_packages(pkg_path)
            events = load_events(evt_path)
        self.assertEqual(specs[0].node_id, "alpha@1.2.0-rc.1")
        self.assertEqual(events[0].node, "alpha@1.2.0-rc.1")

    def test_load_packages_rejects_bad_version(self):
        with tempfile.TemporaryDirectory() as tmp:
            pkg_path = self.write(
                tmp,
                "packages.json",
                {"nodes": [{"package": "a", "version": "1.2", "required_artifacts": ["x"]}]},
            )
            with self.assertRaises(LoadError):
                load_packages(pkg_path)

    def test_load_packages_rejects_duplicate_nodes(self):
        with tempfile.TemporaryDirectory() as tmp:
            node = {"package": "a", "version": "1.0.0", "required_artifacts": ["x"]}
            pkg_path = self.write(tmp, "packages.json", {"nodes": [node, node]})
            with self.assertRaises(LoadError):
                load_packages(pkg_path)

    def test_load_events_rejects_malformed_event(self):
        with tempfile.TemporaryDirectory() as tmp:
            evt_path = self.write(tmp, "events.json", {"events": [{"type": "node_started"}]})
            with self.assertRaises(LoadError):
                load_events(evt_path)


if __name__ == "__main__":
    unittest.main()
