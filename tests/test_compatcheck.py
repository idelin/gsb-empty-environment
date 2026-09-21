import json
import subprocess
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from compatcheck.core import (
    Edge,
    Param,
    Symbol,
    TypeEnv,
    analyze,
    diff_symbols,
    propagate,
    required_bump,
)
from compatcheck.semver import Version, VersionError, bump_level

REPO_ROOT = Path(__file__).resolve().parent.parent
HIERARCHY = {"int": ["num"], "num": ["object"], "str": ["object"], "bytes": ["object"]}


def sym(name, params=(), returns=None, behaviors=(), visibility="public"):
    return Symbol(
        name=name,
        visibility=visibility,
        params=tuple(Param(n, t) for n, t in params),
        returns=returns,
        behaviors=frozenset(behaviors),
    )


class TestVersion(unittest.TestCase):
    def test_parse_plain(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertFalse(v.is_prerelease)

    def test_parse_prerelease(self):
        v = Version.parse("2.0.0-rc.1")
        self.assertEqual(v.prerelease, ("rc", "1"))
        self.assertTrue(v.is_prerelease)
        self.assertEqual(str(v), "2.0.0-rc.1")

    def test_parse_invalid(self):
        for bad in ["1.2", "1.2.3.4", "v1.2.3", "1.02.3", ""]:
            with self.assertRaises(VersionError, msg=bad):
                Version.parse(bad)

    def test_prerelease_ordering(self):
        ordered = [
            "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
            "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
        ]
        versions = [Version.parse(s) for s in ordered]
        for earlier, later in zip(versions, versions[1:]):
            self.assertLess(earlier, later)

    def test_bump_level(self):
        base = Version.parse("1.2.3")
        self.assertEqual(bump_level(base, Version.parse("2.0.0")), "major")
        self.assertEqual(bump_level(base, Version.parse("1.3.0")), "minor")
        self.assertEqual(bump_level(base, Version.parse("1.2.4")), "patch")
        self.assertEqual(bump_level(base, Version.parse("1.2.3-rc.1")), "prerelease")
        self.assertEqual(bump_level(base, Version.parse("1.2.3")), "none")


class TestTypeEnv(unittest.TestCase):
    def test_relations(self):
        tenv = TypeEnv(HIERARCHY)
        self.assertEqual(tenv.relation("int", "int"), "same")
        self.assertEqual(tenv.relation("int", "object"), "widened")   # transitive
        self.assertEqual(tenv.relation("object", "int"), "narrowed")
        self.assertEqual(tenv.relation("str", "bytes"), "unrelated")


class TestDiffSymbols(unittest.TestCase):
    def setUp(self):
        self.tenv = TypeEnv(HIERARCHY)

    def diff_one(self, old, new):
        public, private = diff_symbols({old.name: old}, {new.name: new}, self.tenv)
        self.assertEqual(private, [])
        return public

    def test_param_widening_is_compatible(self):
        changes = self.diff_one(sym("f", [("x", "str")]), sym("f", [("x", "object")]))
        self.assertEqual(len(changes), 1)
        self.assertFalse(changes[0].breaking)
        self.assertEqual(changes[0].position, "input")

    def test_param_narrowing_is_breaking(self):
        changes = self.diff_one(sym("f", [("x", "object")]), sym("f", [("x", "str")]))
        self.assertTrue(changes[0].breaking)

    def test_param_unrelated_is_breaking(self):
        changes = self.diff_one(sym("f", [("x", "str")]), sym("f", [("x", "bytes")]))
        self.assertTrue(changes[0].breaking)

    def test_return_narrowing_is_compatible(self):
        changes = self.diff_one(sym("f", returns="object"), sym("f", returns="str"))
        self.assertEqual(len(changes), 1)
        self.assertFalse(changes[0].breaking)
        self.assertEqual(changes[0].position, "output")

    def test_return_widening_is_breaking(self):
        changes = self.diff_one(sym("f", returns="str"), sym("f", returns="object"))
        self.assertTrue(changes[0].breaking)

    def test_added_optional_param_compatible_required_breaking(self):
        old = sym("f", [("a", "str")])
        optional_new = Symbol("f", params=(Param("a", "str"), Param("b", "int", optional=True)))
        required_new = Symbol("f", params=(Param("a", "str"), Param("b", "int")))
        self.assertFalse(self.diff_one(old, optional_new)[0].breaking)
        self.assertTrue(self.diff_one(old, required_new)[0].breaking)

    def test_removed_param_is_breaking(self):
        changes = self.diff_one(sym("f", [("a", "str"), ("b", "int")]), sym("f", [("a", "str")]))
        self.assertTrue(any(c.kind == "param_removed" and c.breaking for c in changes))

    def test_behavior_removed_breaking_added_compatible(self):
        removed = self.diff_one(sym("f", behaviors=["pure", "thread-safe"]), sym("f", behaviors=["pure"]))
        self.assertEqual(len(removed), 1)
        self.assertTrue(removed[0].breaking)
        added = self.diff_one(sym("f", behaviors=["pure"]), sym("f", behaviors=["pure", "pure2"]))
        self.assertFalse(added[0].breaking)

    def test_removed_symbol_is_breaking_change(self):
        public, _ = diff_symbols({"f": sym("f")}, {}, self.tenv)
        self.assertEqual(public[0].kind, "removed")
        self.assertTrue(public[0].breaking)

    def test_private_changes_are_not_public(self):
        old = {"p": sym("p", [("x", "str")], visibility="private")}
        new = {"p": sym("p", [("x", "bytes")], visibility="private")}
        public, private = diff_symbols(old, new, self.tenv)
        self.assertEqual(public, [])
        self.assertEqual(len(private), 1)

    def test_made_private_counts_as_removal(self):
        public, _ = diff_symbols({"f": sym("f")}, {"f": sym("f", visibility="private")}, self.tenv)
        self.assertEqual(public[0].kind, "removed")
        self.assertTrue(public[0].breaking)


class TestPropagate(unittest.TestCase):
    def test_path_and_sites(self):
        edges = [
            Edge("root", "mid", "r.py:1"),
            Edge("mid", "leaf", "m.py:2"),
            Edge("other", "leaf"),  # no site, not a root chain
        ]
        paths = propagate({"leaf": ["c1"]}, edges, ["root"])
        self.assertEqual(len(paths), 1)
        self.assertEqual(paths[0].nodes, ["root", "mid", "leaf"])
        self.assertEqual(paths[0].sites, ["r.py:1", "m.py:2"])

    def test_uncalled_symbol_reaches_no_root(self):
        paths = propagate({"unused": ["c"]}, [Edge("root", "used")], ["root"])
        self.assertEqual(paths, [])

    def test_cycle_does_not_hang(self):
        edges = [Edge("a", "b"), Edge("b", "a"), Edge("root", "a")]
        paths = propagate({"b": ["c"]}, edges, ["root"])
        self.assertEqual([p.nodes for p in paths], [["root", "a", "b"]])


class TestAnalyze(unittest.TestCase):
    def load(self, name):
        with open(REPO_ROOT / "examples" / name, encoding="utf-8") as handle:
            return json.load(handle)

    def test_breaking_example(self):
        report = analyze(self.load("breaking.json"))
        self.assertEqual(report.package_impact, "breaking")
        self.assertEqual(report.root_impact, "breaking")
        self.assertEqual(report.affected_roots, ["app.main"])
        path_strings = [" -> ".join(p.nodes) for p in report.breaking_paths]
        self.assertIn("app.main -> app.service.handle -> textkit.parse", path_strings)
        self.assertIn("app.main -> app.worker.run -> textkit.tokenize", path_strings)
        # Uncalled removals / unreachable breaking changes do not affect the root.
        unreachable = {c.symbol for c in report.unreachable_breaking}
        self.assertEqual(unreachable, {"textkit.legacy_format", "textkit.trim"})
        # Private change ignored.
        self.assertEqual([c.symbol for c in report.private_changes], ["textkit._cache_clear"])
        self.assertTrue(report.version_check["ok"])
        self.assertEqual(report.version_check["required_bump"], "major")

    def test_compatible_example(self):
        report = analyze(self.load("compatible.json"))
        self.assertEqual(report.package_impact, "compatible")
        self.assertEqual(report.root_impact, "none")
        self.assertEqual(report.breaking_paths, [])
        self.assertTrue(report.version_check["ok"])

    def test_version_violation_when_breaking_without_major(self):
        payload = self.load("breaking.json")
        payload["new_version"] = "1.5.0"
        report = analyze(payload)
        self.assertFalse(report.version_check["ok"])

    def test_zero_major_accepts_minor_for_breaking(self):
        payload = self.load("breaking.json")
        payload["old_version"] = "0.9.0"
        payload["new_version"] = "0.10.0"
        report = analyze(payload)
        self.assertTrue(report.version_check["ok"])

    def test_required_bump_levels(self):
        self.assertEqual(required_bump([]), "patch")


class TestCli(unittest.TestCase):
    def run_cli(self, *args):
        return subprocess.run(
            [sys.executable, "-m", "compatcheck", *args],
            cwd=REPO_ROOT, capture_output=True, text=True,
        )

    def test_breaking_exit_code_and_output(self):
        result = self.run_cli("examples/breaking.json")
        self.assertEqual(result.returncode, 1)
        self.assertIn("Root impact: BREAKING", result.stdout)
        self.assertIn("app.main -> app.service.handle -> textkit.parse", result.stdout)

    def test_compatible_exit_code(self):
        result = self.run_cli("examples/compatible.json")
        self.assertEqual(result.returncode, 0)

    def test_json_output(self):
        result = self.run_cli("examples/breaking.json", "--json")
        data = json.loads(result.stdout)
        self.assertEqual(data["root_impact"], "breaking")
        self.assertEqual(data["affected_roots"], ["app.main"])

    def test_invalid_input_exit_code(self):
        result = self.run_cli("does-not-exist.json")
        self.assertEqual(result.returncode, 2)


if __name__ == "__main__":
    unittest.main()
