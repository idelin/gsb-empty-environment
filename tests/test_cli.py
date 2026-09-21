import hashlib
import json
import tempfile
import unittest
from pathlib import Path

from buildstate.cli import build_report, main

REPO_ROOT = Path(__file__).resolve().parents[1]
EXAMPLES = REPO_ROOT / "examples"


def digest(text):
    return hashlib.sha256(text.encode()).hexdigest()


class ExampleReportTests(unittest.TestCase):
    def test_example_report_states(self):
        report, code = build_report(
            str(EXAMPLES / "build.json"), str(EXAMPLES / "events.jsonl")
        )
        states = {
            line.split()[0]: line.split()[1]
            for line in report.splitlines()
            if line and not line.startswith(("NODE", " ", "SUMMARY", "UNRESOLVED",
                                            "WARNINGS"))
        }
        self.assertEqual(states["compile-core"], "SUCCESS")
        self.assertEqual(states["package-net"], "PENDING")
        self.assertEqual(states["test-flaky"], "SUCCESS")
        self.assertEqual(states["ship-bridge"], "PENDING")
        self.assertEqual(code, 2)
        self.assertIn("net.tar.gz.sha256", report)
        self.assertIn("ghost-node", report)

    def test_main_pending_exit_code(self):
        code = main([
            "--definition", str(EXAMPLES / "build.json"),
            "--events", str(EXAMPLES / "events.jsonl"),
        ])
        self.assertEqual(code, 2)

    def test_all_success_exit_zero(self):
        definition = {
            "packages": {"lib": {"version": "1.2.3"}},
            "nodes": {"build": {
                "inputs": [{"package": "lib", "version": "1.2.3"}],
                "requires_artifacts": ["a.bin"],
            }},
        }
        records = [
            {"event_id": "s", "timestamp": "t", "node": "build", "attempt": 1,
             "kind": "started"},
            {"event_id": "a", "timestamp": "t", "node": "build", "attempt": 1,
             "kind": "artifacts_written",
             "artifacts": [{"name": "a.bin", "sha256": digest("a")}]},
            {"event_id": "u", "timestamp": "t", "node": "build", "attempt": 1,
             "kind": "submitted"},
        ]
        with tempfile.TemporaryDirectory() as tmp:
            d = Path(tmp) / "build.json"
            e = Path(tmp) / "events.jsonl"
            d.write_text(json.dumps(definition), encoding="utf-8")
            e.write_text("\n".join(json.dumps(r) for r in records), encoding="utf-8")
            report, code = build_report(str(d), str(e))
        self.assertEqual(code, 0)
        self.assertIn("1 succeeded", report)

    def test_missing_definition_exit_usage(self):
        with tempfile.TemporaryDirectory() as tmp:
            code = main([
                "--definition", str(Path(tmp) / "nope.json"),
                "--events", str(EXAMPLES / "events.jsonl"),
            ])
        self.assertEqual(code, 64)

    def test_malformed_event_line_reports_location(self):
        definition = {
            "packages": {"lib": {"version": "1.2.3"}},
            "nodes": {"build": {
                "inputs": [{"package": "lib", "version": "1.2.3"}],
                "requires_artifacts": ["a.bin"],
            }},
        }
        with tempfile.TemporaryDirectory() as tmp:
            d = Path(tmp) / "build.json"
            e = Path(tmp) / "events.jsonl"
            d.write_text(json.dumps(definition), encoding="utf-8")
            e.write_text(
                '{"event_id": "s", "node": "build", "attempt": 1, '
                '"kind": "started", "timestamp": "t"}\n'
                'not json\n',
                encoding="utf-8",
            )
            with self.assertRaisesRegex(Exception, r":2:"):
                build_report(str(d), str(e))


if __name__ == "__main__":
    unittest.main()
