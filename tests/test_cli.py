import contextlib
import io
import json
import tempfile
import unittest
from pathlib import Path

from buildset.cli import main

PACKAGES = {
    "packages": [
        {"name": "app", "version": "1.0.0", "kind": "library", "dependencies": [
            {"name": "gen", "constraint": ">=3.0.0"},
            {"name": "winonly", "constraint": "", "when": {"os": ["windows"]}},
        ]},
        {"name": "gen", "version": "3.1.0", "kind": "tool", "dependencies": []},
        {"name": "winonly", "version": "1.0.0", "kind": "library", "dependencies": []},
    ]
}

REQUEST = {
    "host": {"os": "linux", "arch": "x86_64"},
    "target": {"os": "darwin", "arch": "arm64"},
    "stage": "build",
    "roots": [{"name": "app", "constraint": ">=1.0.0"}],
}


class CliTests(unittest.TestCase):
    def setUp(self):
        self.tmp = tempfile.TemporaryDirectory()
        self.addCleanup(self.tmp.cleanup)
        root = Path(self.tmp.name)
        self.packages = root / "packages.json"
        self.request = root / "request.json"
        self.packages.write_text(json.dumps(PACKAGES), encoding="utf-8")
        self.request.write_text(json.dumps(REQUEST), encoding="utf-8")

    def run_cli(self, *extra):
        out = io.StringIO()
        with contextlib.redirect_stdout(out):
            code = main(["--packages", str(self.packages),
                         "--request", str(self.request), *extra])
        return code, json.loads(out.getvalue())

    def test_resolve_reports_build_set_and_evidence(self):
        code, report = self.run_cli()
        self.assertEqual(code, 0)
        names = {n["name"] for n in report["build_set"]}
        self.assertEqual(names, {"app", "gen"})
        self.assertEqual(report["pruned"], ["app@1.0.0 -> winonly"])
        gen = next(n for n in report["build_set"] if n["name"] == "gen")
        self.assertEqual(gen["partition"], "host")
        self.assertEqual(gen["cache_key"], ["gen", "3.1.0", "linux", "x86_64"])
        evidence = report["evidence"]
        self.assertTrue(any(e["dependency"] == "winonly" and not e["taken"]
                            for e in evidence))

    def test_cache_dir_separates_partitions(self):
        cache_dir = str(Path(self.tmp.name) / "cache")
        code, report = self.run_cli("--cache-dir", cache_dir)
        self.assertEqual(code, 0)
        stored = report["cache"]["stored"]
        self.assertTrue(any("/host/" in p for p in stored))
        self.assertTrue(any("/target/" in p for p in stored))
        # Second run: everything is a cache hit.
        code, report = self.run_cli("--cache-dir", cache_dir)
        self.assertEqual(code, 0)
        self.assertEqual(report["cache"]["stored"], [])
        self.assertEqual(len(report["cache"]["hits"]), 2)

    def test_conflict_exit_code(self):
        data = json.loads(self.packages.read_text(encoding="utf-8"))
        data["packages"].append(
            {"name": "other", "version": "1.0.0", "kind": "library",
             "dependencies": [{"name": "gen", "constraint": "<3.0.0"}]}
        )
        self.packages.write_text(json.dumps(data), encoding="utf-8")
        req = json.loads(self.request.read_text(encoding="utf-8"))
        req["roots"].append({"name": "other", "constraint": ""})
        self.request.write_text(json.dumps(req), encoding="utf-8")
        code, report = self.run_cli()
        self.assertEqual(code, 1)
        self.assertEqual(report["conflict"], "gen")


if __name__ == "__main__":
    unittest.main()
