import json
import subprocess
import sys
import tempfile
import unittest
from pathlib import Path

from build_state.main import build_report, render
from build_state.models import load_build_input, load_events
from build_state.registry import PackageRegistry
from build_state.tracker import BuildTracker, NodeState

DATA = Path(__file__).resolve().parent.parent / "build_state" / "data"

PACKAGES = DATA / "packages.json"
BUILD_INPUT = DATA / "build_input.json"
EVENTS = DATA / "events.json"


class SampleFixtureTests(unittest.TestCase):
    def setUp(self):
        self.registry = PackageRegistry.from_file(PACKAGES)
        build_input = load_build_input(BUILD_INPUT, self.registry)
        events = load_events(EVENTS)
        tracker = BuildTracker(build_input)
        tracker.ingest(events)
        self.tracker = tracker
        self.statuses = tracker.reconstruct()
        self.report = tracker.as_report(self.statuses)

    def test_expected_states(self):
        states = {node: status.state for node, status in self.statuses.items()}
        self.assertEqual(states, {
            "node-a": NodeState.SUCCESS,
            "node-b": NodeState.PENDING,
            "node-c": NodeState.SUCCESS,
            "node-d": NodeState.FAILED,
            "node-e": NodeState.SUCCESS,
            "node-f": NodeState.SUCCESS,
            "node-g": NodeState.SUCCESS,
        })

    def test_unresolved_nodes_are_not_guessed(self):
        self.assertEqual(self.report["unresolved_nodes"], ["node-b"])
        node_b = self.statuses["node-b"]
        self.assertEqual(node_b.missing_artifacts, ("symbols.tar",))
        self.assertTrue(node_b.missing_commit)

    def test_old_attempt_failure_ignored_for_new_success(self):
        # node-d: attempt 1 succeeded (complete), attempt 2 failed.
        self.assertEqual(self.statuses["node-d"].state, NodeState.FAILED)
        self.assertEqual(self.statuses["node-d"].attempt, 2)

    def test_old_attempt_success_does_not_leak_into_new_success(self):
        # node-e: attempt 1 claimed success with wrong-version artifact and no
        # commit requirement; attempt 2 must stand on its own evidence.
        self.assertEqual(self.statuses["node-e"].state, NodeState.SUCCESS)
        self.assertEqual(self.statuses["node-e"].attempt, 2)
        self.assertEqual(self.statuses["node-e"].observed_attempts, (1, 2))

    def test_duplicate_and_orphan_accounting(self):
        self.assertEqual(self.report["duplicate_event_ids"], ["E023"])
        self.assertEqual(self.report["orphan_events"], ["E090"])

    def test_anomaly_messages(self):
        messages = [a["message"] for a in self.report["anomalies"]]
        self.assertTrue(any("undeclared node 'node-x'" in m for m in messages))
        self.assertTrue(any("'debug.map'" in m for m in messages))
        self.assertTrue(any("fff-OTHER" in m for m in messages))
        self.assertTrue(any("duplicate submission" in m for m in messages))

    def test_counts(self):
        self.assertEqual(self.report["counts"],
                         {"success": 5, "failed": 1, "pending": 1})

    def test_report_is_json_serializable(self):
        json.dumps(self.report)  # must not raise
        self.assertIn("node-a", render(self.report))

    def test_shuffled_order_gives_same_result(self):
        events = load_events(EVENTS)
        reversed_events = list(reversed(events))
        build_input = load_build_input(BUILD_INPUT, self.registry)
        tracker = BuildTracker(build_input)
        tracker.ingest(reversed_events)
        reversed_report = tracker.as_report(tracker.reconstruct())
        self.assertEqual(reversed_report["nodes"], self.report["nodes"])
        self.assertEqual(reversed_report["counts"], self.report["counts"])


class CliTests(unittest.TestCase):
    def test_cli_module_exit_codes(self):
        repo_root = Path(__file__).resolve().parent.parent
        proc = subprocess.run(
            [sys.executable, "-m", "build_state.main",
             "--registry", str(PACKAGES),
             "--build-input", str(BUILD_INPUT),
             "--events", str(EVENTS)],
            cwd=repo_root,
            capture_output=True,
            text=True,
        )
        self.assertEqual(proc.returncode, 0, proc.stderr)
        self.assertIn("node-b", proc.stdout)

        proc_strict = subprocess.run(
            [sys.executable, "-m", "build_state.main",
             "--registry", str(PACKAGES),
             "--build-input", str(BUILD_INPUT),
             "--events", str(EVENTS), "--strict"],
            cwd=repo_root,
            capture_output=True,
            text=True,
        )
        self.assertEqual(proc_strict.returncode, 1)


if __name__ == "__main__":
    unittest.main()
