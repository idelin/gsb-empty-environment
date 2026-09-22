import io
import json
import tempfile
import unittest
from contextlib import redirect_stderr, redirect_stdout
from pathlib import Path

import depres
from depres import Constraint, Index, InputError, Resolver, Version


def versions(items):
    return [Version.parse(item) for item in items]


def index_from(mapping):
    return Index(mapping)


ROOT_EXAMPLE = Path(__file__).parent / "examples"


class VersionTests(unittest.TestCase):
    def test_parse_and_core_order(self):
        self.assertEqual(str(Version.parse("1.2.3")), "1.2.3")
        ordered = versions(["1.0.0", "1.0.1", "1.1.0", "1.10.0", "2.0.0"])
        self.assertEqual(ordered, sorted(ordered))

    def test_semver_prerelease_ordering(self):
        ordered = versions([
            "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta",
            "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11",
            "1.0.0-rc.1", "1.0.0",
        ])
        self.assertEqual(ordered, sorted(ordered))
        self.assertLess(Version.parse("2.0.0-rc.1"), Version.parse("2.0.0"))

    def test_invalid_version(self):
        for bad in ["1", "1.2", "v1.2.3", "01.2.3", "1.2.3-"]:
            with self.subTest(bad=bad):
                with self.assertRaises(InputError):
                    Version.parse(bad)


class ConstraintTests(unittest.TestCase):
    def test_range_and_operators(self):
        constraint = Constraint(">=1.0.0,<2.0.0")
        self.assertTrue(constraint.matches(Version.parse("1.0.0")))
        self.assertTrue(constraint.matches(Version.parse("1.9.9")))
        self.assertFalse(constraint.matches(Version.parse("2.0.0")))

    def test_bare_version_is_exact_match(self):
        self.assertTrue(Constraint("1.2.3").matches(Version.parse("1.2.3")))
        self.assertFalse(Constraint("1.2.3").matches(Version.parse("1.2.4")))

    def test_not_equal_and_star(self):
        self.assertFalse(Constraint("!=1.2.3").matches(Version.parse("1.2.3")))
        self.assertTrue(Constraint("*").matches(Version.parse("9.9.9-rc.2")))


class ResolverTests(unittest.TestCase):
    PACKAGES = {
        "app-server": {
            "1.4.0": {"deps": {"http-lib": ">=1.0.0,<2.0.0", "json-util": ">=1.0.0"}},
            "2.0.0": {"deps": {"http-lib": ">=2.0.0,<3.0.0", "json-util": ">=1.0.0"}},
            "2.1.0": {"deps": {"http-lib": ">=2.1.0,<3.0.0", "json-util": ">=1.0.0"}},
        },
        "http-lib": {
            "1.8.0": {"deps": {"sock": ">=1.0.0,<2.0.0"}},
            "2.0.0": {"deps": {"sock": ">=2.0.0"}},
            "2.1.0": {"deps": {"sock": ">=2.0.0"}},
        },
        "json-util": {"1.1.0": {"deps": {}}, "1.3.0": {"deps": {}}},
        "sock": {
            "1.2.0": {"deps": {}},
            "2.0.0-rc.1": {"deps": {}},
            "2.0.0": {"deps": {}},
        },
        "metrics": {"0.9.0": {"deps": {}}, "1.0.0": {"deps": {"sock": ">=1.0.0"}}},
    }
    LOCK = {
        "app-server": "1.4.0",
        "http-lib": "1.8.0",
        "json-util": "1.1.0",
        "sock": "1.2.0",
        "metrics": "0.9.0",
    }

    def resolve(self, requirements, lock=None, max_states=100_000):
        return Resolver(
            index_from(self.PACKAGES), requirements,
            lock if lock is not None else self.LOCK,
            max_states=max_states,
        ).solve()

    def test_lock_kept_when_constraints_still_satisfied(self):
        result = self.resolve({
            "app-server": ">=1.0.0",
            "json-util": ">=1.0.0",
            "metrics": "*",
        })
        self.assertEqual(result.status, "ok")
        self.assertEqual(result.solution["app-server"], Version.parse("1.4.0"))
        self.assertEqual(result.solution["http-lib"], Version.parse("1.8.0"))
        self.assertEqual(result.solution["sock"], Version.parse("1.2.0"))
        self.assertEqual(result.solution["json-util"], Version.parse("1.1.0"))
        self.assertEqual(result.solution["metrics"], Version.parse("0.9.0"))
        self.assertEqual(result.changes, ())
        self.assertEqual(result.cost, 0)

    def test_minimal_changes_then_newer_versions(self):
        result = self.resolve({"app-server": ">=2.0.0", "json-util": ">=1.0.0"})
        self.assertEqual(result.status, "ok")
        self.assertEqual(result.solution["app-server"], Version.parse("2.1.0"))
        self.assertEqual(result.solution["http-lib"], Version.parse("2.1.0"))
        self.assertEqual(result.solution["sock"], Version.parse("2.0.0"))
        self.assertEqual(result.solution["json-util"], Version.parse("1.1.0"))
        names = {change.name for change in result.changes if change.new is not None}
        self.assertEqual(names, {"app-server", "http-lib", "sock"})
        self.assertEqual(result.cost, 3)

    def test_stable_version_beats_release_candidate(self):
        result = self.resolve({"sock": ">=2.0.0-rc.1"})
        self.assertEqual(result.solution["sock"], Version.parse("2.0.0"))

    def test_upgrade_shared_dependency_only_not_everything(self):
        packages = {
            "a": {"1.0.0": {"deps": {"c": ">=1.0.0,<3.0.0"}}},
            "b": {"1.0.0": {"deps": {"c": ">=1.0.0,<3.0.0"}}},
            "c": {"1.0.0": {"deps": {}}, "2.0.0": {"deps": {}}},
        }
        lock = {"a": "1.0.0", "b": "1.0.0", "c": "1.0.0"}
        result = Resolver(index_from(packages),
                          {"a": ">=1.0.0", "b": ">=1.0.0", "c": ">=2.0.0"},
                          lock).solve()
        self.assertEqual(result.status, "ok")
        self.assertEqual(result.solution["a"], Version.parse("1.0.0"))
        self.assertEqual(result.solution["b"], Version.parse("1.0.0"))
        self.assertEqual(result.solution["c"], Version.parse("2.0.0"))
        self.assertEqual([change.name for change in result.changes], ["c"])

    def test_fewer_changes_beats_newer_version(self):
        packages = {
            "app": {
                "1.0.0": {"deps": {"lib": ">=1.0.0,<2.0.0"}},
                "2.0.0": {"deps": {"lib": ">=1.0.0,<2.0.0"}},
                "2.1.0": {"deps": {"lib": ">=2.0.0"}},
            },
            "lib": {"1.5.0": {"deps": {}}, "2.0.0": {"deps": {}}},
        }
        result = Resolver(index_from(packages), {"app": ">=2.0.0"},
                          {"app": "1.0.0", "lib": "1.5.0"}).solve()
        self.assertEqual(result.solution["app"], Version.parse("2.0.0"))
        self.assertEqual(result.solution["lib"], Version.parse("1.5.0"))
        self.assertEqual(result.cost, 1)

    def test_causal_chain_links_root_to_transitive_change(self):
        result = self.resolve({"app-server": ">=2.0.0", "json-util": ">=1.0.0"})
        text = depres.render_report(result)
        self.assertIn("sock: 1.2.0 -> 2.0.0", text)
        self.assertIn('because http-lib@2.1.0 requires "sock>=2.0.0"', text)
        self.assertIn("http-lib: 1.8.0 -> 2.1.0", text)
        self.assertIn('because app-server@2.1.0 requires "http-lib>=2.1.0,<3.0.0"', text)
        self.assertIn("app-server: 1.4.0 -> 2.1.0", text)
        self.assertIn('because root requires "app-server>=2.0.0"', text)

    def test_root_removal_prunes_unreachable_packages(self):
        result = self.resolve({"json-util": ">=1.0.0"})
        self.assertEqual(result.solution, {"json-util": Version.parse("1.1.0")})
        removed = {change.name for change in result.changes if change.new is None}
        self.assertEqual(removed, {"app-server", "http-lib", "sock", "metrics"})
        text = depres.render_report(result)
        self.assertIn("retention policy:", text)
        self.assertIn("no remaining package requires it", text)

    def test_unknown_requirement_is_unsatisfiable(self):
        result = self.resolve({"does-not-exist": ">=1.0.0"})
        self.assertEqual(result.status, "unsat")

    def test_impossible_range_is_unsatisfiable(self):
        result = self.resolve({"sock": ">=3.0.0"})
        self.assertEqual(result.status, "unsat")

    def test_search_limit_is_distinct_from_unsatisfiable(self):
        result = self.resolve({"app-server": ">=2.0.0"}, max_states=1)
        self.assertEqual(result.status, "limit")
        self.assertIsNone(result.solution)

    def test_locked_version_missing_from_index_is_input_error(self):
        with self.assertRaisesRegex(InputError, "does not exist"):
            Resolver(index_from(self.PACKAGES), {"sock": ">=1.0.0"},
                     {"sock": "9.9.9"})


class CliTests(unittest.TestCase):
    def run_cli(self, *args):
        stdout, stderr = io.StringIO(), io.StringIO()
        with redirect_stdout(stdout), redirect_stderr(stderr):
            code = depres.main(list(args))
        return code, stdout.getvalue(), stderr.getvalue()

    def test_resolve_exit_zero_and_outputs_lock(self):
        with tempfile.TemporaryDirectory() as tmp:
            out = Path(tmp) / "lock.new.json"
            code, stdout, _ = self.run_cli(
                "resolve",
                "--packages", str(ROOT_EXAMPLE / "packages.json"),
                "--build", str(ROOT_EXAMPLE / "build.json"),
                "--lock", str(ROOT_EXAMPLE / "lock.json"),
                "--out", str(out),
            )
            self.assertEqual(code, 0)
            self.assertIn("status: ok", stdout)
            written = json.loads(out.read_text())
            self.assertEqual(written["app-server"], "2.1.0")

    def test_unsat_exit_code(self):
        code, _, stderr = self.run_cli(
            "resolve",
            "--packages", str(ROOT_EXAMPLE / "packages.json"),
            "--build", str(ROOT_EXAMPLE / "build-unsat.json"),
        )
        self.assertEqual(code, 1)
        self.assertIn("unsatisfiable", stderr)

    def test_limit_exit_code(self):
        code, stdout, stderr = self.run_cli(
            "resolve",
            "--packages", str(ROOT_EXAMPLE / "packages.json"),
            "--build", str(ROOT_EXAMPLE / "build.json"),
            "--lock", str(ROOT_EXAMPLE / "lock.json"),
            "--max-states", "1",
        )
        self.assertEqual(code, 2)
        self.assertIn("status: limit", stdout)
        self.assertIn("search limit exceeded", stderr)

    def test_missing_locked_version_exit_code(self):
        code, _, stderr = self.run_cli(
            "resolve",
            "--packages", str(ROOT_EXAMPLE / "packages.json"),
            "--build", str(ROOT_EXAMPLE / "build.json"),
            "--lock", str(ROOT_EXAMPLE / "lock-missing.json"),
        )
        self.assertEqual(code, 3)
        self.assertIn("does not exist", stderr)


if __name__ == "__main__":
    unittest.main()
