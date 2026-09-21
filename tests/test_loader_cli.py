import io
import json
import tempfile
import unittest
from contextlib import redirect_stdout
from pathlib import Path

from ci_state.loader import (
    InputError,
    PackageCatalog,
    load_build_input,
    load_events,
)
from ci_state.main import main, build

ROOT = Path(__file__).resolve().parent.parent
SAMPLE = ROOT / "sample_data"


class CatalogTest(unittest.TestCase):
    def setUp(self):
        self.catalog = PackageCatalog({
            "lib": ["1.2.0", "1.3.0-rc.1", "1.3.0", "2.0.0-alpha"],
        })

    def test_exact_prerelease_pinned(self):
        self.assertEqual(str(self.catalog.resolve("lib", "1.3.0-rc.1")),
                         "1.3.0-rc.1")

    def test_latest_prefers_release(self):
        self.assertEqual(str(self.catalog.resolve("lib", "latest")), "1.3.0")

    def test_range_excludes_prereleases_unless_explicit(self):
        self.assertEqual(str(self.catalog.resolve("lib", ">=1.2.0 <2.0.0")),
                         "1.3.0")
        self.assertEqual(
            str(self.catalog.resolve("lib", ">=1.3.0-rc.1 <2.0.0")),
            "1.3.0",
        )
        self.assertEqual(
            str(self.catalog.resolve("lib", ">=2.0.0-alpha <2.0.0")),
            "2.0.0-alpha",
        )

    def test_caret_and_tilde(self):
        self.assertEqual(str(self.catalog.resolve("lib", "^1.2.0")), "1.3.0")
        # tilde permits the final release within the same minor
        self.assertEqual(str(self.catalog.resolve("lib", "~1.3.0-rc.1")),
                         "1.3.0")

    def test_missing_package_and_version(self):
        with self.assertRaises(InputError):
            self.catalog.resolve("nope", "1.0.0")
        with self.assertRaises(InputError):
            self.catalog.resolve("lib", "9.9.9")

    def test_invalid_version_in_catalog(self):
        with self.assertRaises(InputError):
            PackageCatalog({"x": ["not-a-version"]})


class BuildInputTest(unittest.TestCase):
    def test_sample_loads_and_resolves(self):
        catalog = PackageCatalog.from_data(
            json.loads((SAMPLE / "packages.json").read_text()))
        nodes, resolved = load_build_input(SAMPLE / "build_input.json", catalog)
        self.assertEqual(
            set(nodes),
            {"lib", "api", "worker", "gateway", "scheduler", "docs", "nightly"},
        )
        picked = {r["name"]: r["packages"] for r in resolved}
        self.assertEqual(picked["api"]["libcore"], "1.3.0-rc.1")
        self.assertEqual(picked["gateway"]["libcore"], "1.3.0")

    def test_unknown_dependency_rejected(self):
        catalog = PackageCatalog({"lib": ["1.0.0"]})
        data = {"nodes": [
            {"name": "a", "packages": {}, "required_artifacts": [],
             "depends_on": ["missing"]}
        ]}
        with self.assertRaises(InputError):
            load_build_input(data, catalog)


class EventLogTest(unittest.TestCase):
    def test_sample_event_log_parses(self):
        events = load_events(SAMPLE / "events.log.json")
        self.assertEqual(len(events), 29)
        self.assertEqual(len({e.id for e in events}), 27)


class CliIntegrationTest(unittest.TestCase):
    ARGS = [
        "--packages", str(SAMPLE / "packages.json"),
        "--input", str(SAMPLE / "build_input.json"),
        "--events", str(SAMPLE / "events.log.json"),
    ]

    def test_sample_states(self):
        engine, _resolved = build(
            str(SAMPLE / "packages.json"),
            str(SAMPLE / "build_input.json"),
            str(SAMPLE / "events.log.json"),
        )
        states = {r.node: r.state.value for r in engine.report()}
        self.assertEqual(states, {
            "api": "SUCCESS",        # attempt 2 wins; late attempt-1 failure ignored
            "gateway": "SUCCESS",    # events processed out of order
            "lib": "SUCCESS",        # duplicates ignored
            "worker": "PENDING",     # written but never committed
            "scheduler": "PENDING",  # attempt 2 only started
            "docs": "PENDING",       # no events at all
            "nightly": "FAILED",     # explicit failure terminal
        })
        self.assertEqual(len(engine.duplicate_events), 2)

    def test_cli_json_exit_code_pending(self):
        buf = io.StringIO()
        with redirect_stdout(buf):
            code = main(self.ARGS + ["--json"])
        self.assertEqual(code, 2)
        payload = json.loads(buf.getvalue())
        undecided = {n["node"] for n in payload["nodes"]
                     if n["state"] == "PENDING"}
        self.assertEqual(undecided, {"worker", "scheduler", "docs"})
        api = next(n for n in payload["nodes"] if n["node"] == "api")
        self.assertEqual(api["attempt"], 2)
        self.assertEqual(api["missing"], [])

    def test_all_successful_log_exits_zero(self):
        catalog = PackageCatalog.from_data(
            json.loads((SAMPLE / "packages.json").read_text()))
        nodes, _ = load_build_input(SAMPLE / "build_input.json", catalog)
        log = []
        seq = 0
        for name, artifacts in nodes.items():
            log.append({"id": f"{name}-s", "kind": "node.started",
                        "node": name, "attempt": 1, "ts": seq})
            seq += 1
            for art in artifacts:
                log.append({"id": f"{name}-w-{art}", "kind": "artifact.written",
                            "node": name, "attempt": 1, "ts": seq,
                            "artifact": art})
                seq += 1
            for art in artifacts:
                log.append({"id": f"{name}-c-{art}", "kind": "artifact.committed",
                            "node": name, "attempt": 1, "ts": seq,
                            "artifact": art})
                seq += 1
            log.append({"id": f"{name}-ok", "kind": "node.succeeded",
                        "node": name, "attempt": 1, "ts": seq})
            seq += 1
        with tempfile.TemporaryDirectory() as tmp:
            path = Path(tmp) / "all-ok.json"
            path.write_text(json.dumps(log))
            buf = io.StringIO()
            with redirect_stdout(buf):
                code = main([
                    "--packages", str(SAMPLE / "packages.json"),
                    "--input", str(SAMPLE / "build_input.json"),
                    "--events", str(path),
                ])
            self.assertEqual(code, 0, buf.getvalue())


if __name__ == "__main__":
    unittest.main()
