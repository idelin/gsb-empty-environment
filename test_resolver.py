import io
import json
import os
import tempfile
import unittest
from contextlib import redirect_stdout

from resolver import (
    ConflictError,
    Version,
    main,
    parse_clauses,
    resolve,
)


def manifest(packages, roots, allow_prerelease=None):
    data = {"packages": packages, "roots": roots}
    if allow_prerelease is not None:
        data["allow_prerelease"] = allow_prerelease
    return data


class VersionTests(unittest.TestCase):
    def test_parse_basic(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertIsNone(v.prerelease)
        self.assertFalse(v.is_prerelease)

    def test_parse_prerelease(self):
        v = Version.parse("2.0.0-rc.1")
        self.assertEqual(v.prerelease, "rc.1")
        self.assertTrue(v.is_prerelease)
        self.assertEqual(str(v), "2.0.0-rc.1")

    def test_invalid_version_rejected(self):
        for bad in ("1.2", "1.2.3.4", "v1.2.3", "1.02.3", ""):
            with self.assertRaises(ValueError, msg=bad):
                Version.parse(bad)

    def test_ordering(self):
        ordered = [
            "0.9.9", "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta",
            "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1",
            "1.0.0", "1.2.0", "1.10.0", "2.0.0",
        ]
        versions = [Version.parse(s) for s in ordered]
        for earlier, later in zip(versions, versions[1:]):
            self.assertLess(earlier, later)
        self.assertEqual(sorted(reversed(versions)), versions)

    def test_prerelease_before_release(self):
        self.assertLess(Version.parse("2.0.0-rc.1"), Version.parse("2.0.0"))


class ClauseTests(unittest.TestCase):
    def matches(self, spec, version):
        return all(c.matches(Version.parse(version)) for c in parse_clauses(spec))

    def test_wildcard_and_empty(self):
        self.assertTrue(self.matches("*", "3.1.4"))
        self.assertTrue(self.matches("", "3.1.4"))

    def test_exact_pin(self):
        self.assertTrue(self.matches("1.2.3", "1.2.3"))
        self.assertFalse(self.matches("1.2.3", "1.2.4"))
        self.assertTrue(self.matches("==1.2.3", "1.2.3"))

    def test_ranges(self):
        self.assertTrue(self.matches(">=1.0.0,<2.0.0", "1.5.0"))
        self.assertFalse(self.matches(">=1.0.0,<2.0.0", "2.0.0"))
        self.assertFalse(self.matches(">=1.0.0,<2.0.0", "0.9.9"))
        self.assertTrue(self.matches(">1.0.0,<=1.5.0", "1.5.0"))
        self.assertFalse(self.matches(">1.0.0,<=1.5.0", "1.0.0"))

    def test_exclusion_point(self):
        self.assertTrue(self.matches(">=1.0.0,!=1.5.0", "1.4.0"))
        self.assertFalse(self.matches(">=1.0.0,!=1.5.0", "1.5.0"))

    def test_prerelease_boundary(self):
        # 2.0.0-rc.1 < 2.0.0, so it satisfies <2.0.0
        self.assertTrue(self.matches("<2.0.0", "2.0.0-rc.1"))
        self.assertFalse(self.matches(">=2.0.0", "2.0.0-rc.1"))


class ResolveTests(unittest.TestCase):
    def test_picks_newest_version(self):
        m = manifest(
            {"a": {"1.0.0": {}, "1.2.0": {}, "1.1.0": {}}},
            {"a": "*"},
        )
        self.assertEqual(resolve(m), {"a": Version.parse("1.2.0")})

    def test_transitive_constraints_narrow_choice(self):
        m = manifest(
            {
                "app": {"1.0.0": {"lib": ">=1.0.0,<2.0.0"}},
                "lib": {"1.0.0": {}, "1.5.0": {}, "2.0.0": {}},
            },
            {"app": "*"},
        )
        result = resolve(m)
        self.assertEqual(result["lib"], Version.parse("1.5.0"))

    def test_exclusion_point_skipped(self):
        m = manifest(
            {
                "app": {"1.0.0": {"lib": ">=1.0.0,!=1.5.0"}},
                "lib": {"1.4.0": {}, "1.5.0": {}},
            },
            {"app": "*"},
        )
        self.assertEqual(resolve(m)["lib"], Version.parse("1.4.0"))

    def test_lexicographic_order_and_backtracking(self):
        # 'a' is resolved before 'b'; newest 'a' conflicts with b's needs,
        # so the resolver must backtrack to an older 'a'.
        m = manifest(
            {
                "a": {"1.0.0": {"c": "<2.0.0"}, "2.0.0": {"c": ">=2.0.0"}},
                "b": {"1.0.0": {"c": "<2.0.0"}},
                "c": {"1.0.0": {}, "2.0.0": {}},
            },
            {"a": "*", "b": "*"},
        )
        result = resolve(m)
        self.assertEqual(result["a"], Version.parse("1.0.0"))
        self.assertEqual(result["c"], Version.parse("1.0.0"))

    def test_conflict_reports_constraint_set(self):
        m = manifest(
            {
                "a": {"1.0.0": {"c": "<2.0.0"}},
                "b": {"1.0.0": {"c": ">=2.0.0"}},
                "c": {"1.0.0": {}, "2.0.0": {}},
            },
            {"a": "*", "b": "*"},
        )
        with self.assertRaises(ConflictError) as ctx:
            resolve(m)
        err = ctx.exception
        self.assertEqual(err.package, "c")
        raws = {(sc.raw, sc.source) for sc in err.constraints}
        self.assertIn(("<2.0.0", "a@1.0.0"), raws)
        self.assertIn((">=2.0.0", "b@1.0.0"), raws)
        message = str(err)
        self.assertIn("c <2.0.0 (required by a@1.0.0)", message)
        self.assertIn("c >=2.0.0 (required by b@1.0.0)", message)
        self.assertIn("available versions", message)

    def test_prerelease_excluded_by_default(self):
        m = manifest(
            {"a": {"1.0.0": {}, "2.0.0-rc.1": {}}},
            {"a": "*"},
        )
        self.assertEqual(resolve(m)["a"], Version.parse("1.0.0"))

    def test_prerelease_allowed_via_manifest_list(self):
        m = manifest(
            {"a": {"1.0.0": {}, "2.0.0-rc.1": {}}},
            {"a": "*"},
            allow_prerelease=["a"],
        )
        self.assertEqual(resolve(m)["a"], Version.parse("2.0.0-rc.1"))

    def test_prerelease_allowed_globally(self):
        m = manifest(
            {"a": {"1.0.0": {}, "2.0.0-rc.1": {}}},
            {"a": "*"},
            allow_prerelease=True,
        )
        self.assertEqual(resolve(m)["a"], Version.parse("2.0.0-rc.1"))

    def test_prerelease_allowed_via_constraint_reference(self):
        m = manifest(
            {"a": {"1.0.0": {}, "2.0.0-rc.1": {}}},
            {"a": ">=2.0.0-rc.1"},
        )
        self.assertEqual(resolve(m)["a"], Version.parse("2.0.0-rc.1"))

    def test_cyclic_dependencies_resolve(self):
        m = manifest(
            {
                "a": {"1.0.0": {"b": ">=1.0.0"}},
                "b": {"1.0.0": {"a": ">=1.0.0,<2.0.0"}},
            },
            {"a": "*"},
        )
        result = resolve(m)
        self.assertEqual(result["a"], Version.parse("1.0.0"))
        self.assertEqual(result["b"], Version.parse("1.0.0"))

    def test_incompatible_cycle_conflicts(self):
        m = manifest(
            {
                "a": {"1.0.0": {"b": ">=1.0.0"}},
                "b": {"1.0.0": {"a": ">=2.0.0"}},
            },
            {"a": "*"},
        )
        with self.assertRaises(ConflictError):
            resolve(m)

    def test_unknown_dependency_conflicts(self):
        m = manifest(
            {"a": {"1.0.0": {"ghost": "*"}}},
            {"a": "*"},
        )
        with self.assertRaises(ConflictError) as ctx:
            resolve(m)
        self.assertEqual(ctx.exception.package, "ghost")
        self.assertEqual(ctx.exception.available, [])

    def test_shared_dependency_intersection(self):
        m = manifest(
            {
                "app": {"1.0.0": {"x": ">=1.0.0", "y": ">=1.0.0"}},
                "x": {"1.0.0": {"z": "<2.0.0"}},
                "y": {"1.0.0": {"z": ">=1.5.0"}},
                "z": {"1.0.0": {}, "1.5.0": {}, "2.0.0": {}},
            },
            {"app": "*"},
        )
        self.assertEqual(resolve(m)["z"], Version.parse("1.5.0"))


class CliTests(unittest.TestCase):
    def run_cli(self, data):
        with tempfile.NamedTemporaryFile(
            "w", suffix=".json", delete=False, encoding="utf-8"
        ) as fh:
            json.dump(data, fh)
            path = fh.name
        try:
            buf = io.StringIO()
            with redirect_stdout(buf):
                code = main(["resolver.py", path])
            return code, json.loads(buf.getvalue())
        finally:
            os.unlink(path)

    def test_cli_success(self):
        m = manifest({"a": {"1.0.0": {}, "1.1.0": {}}}, {"a": "*"})
        code, out = self.run_cli(m)
        self.assertEqual(code, 0)
        self.assertEqual(out, {"status": "resolved", "versions": {"a": "1.1.0"}})

    def test_cli_conflict(self):
        m = manifest(
            {
                "a": {"1.0.0": {"c": "<2.0.0"}},
                "b": {"1.0.0": {"c": ">=2.0.0"}},
                "c": {"1.0.0": {}, "2.0.0": {}},
            },
            {"a": "*", "b": "*"},
        )
        code, out = self.run_cli(m)
        self.assertEqual(code, 1)
        self.assertEqual(out["status"], "conflict")
        self.assertEqual(out["detail"]["package"], "c")
        self.assertEqual(
            {tuple(sorted(c.items())) for c in out["detail"]["constraints"]},
            {
                (("constraint", "<2.0.0"), ("required_by", "a@1.0.0")),
                (("constraint", ">=2.0.0"), ("required_by", "b@1.0.0")),
            },
        )


if __name__ == "__main__":
    unittest.main()
