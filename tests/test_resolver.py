from __future__ import annotations

import json
import io
from pathlib import Path
import unittest
from contextlib import redirect_stdout

from package_selector.cli import main as cli_main
from package_selector import SelectionError, resolve
from package_selector.manifest import load_manifest, load_manifest_file
from package_selector.versions import Constraint, Version


def package(name, version, provides=None, requires=None, conflicts=None, targets=None):
    item = {
        "name": name,
        "version": version,
        "provides": provides if provides is not None else [name],
    }
    if requires is not None:
        item["requires"] = requires
    if conflicts is not None:
        item["conflicts"] = conflicts
    if targets is not None:
        item["targets"] = targets
    return item


def capability(name, version=None, targets=None):
    item = {"capability": name}
    if version is not None:
        item["version"] = version
    if targets is not None:
        item["targets"] = targets
    return item


def manifest(packages, requirements=None, target="", name="test", allow_prereleases=None):
    build = {"name": name, "requirements": requirements or []}
    if target:
        build["target"] = target
    if allow_prereleases is not None:
        build["allow_prereleases"] = allow_prereleases
    return {"packages": packages, "builds": [build]}


def resolve_data(data, build_name=None):
    return resolve(load_manifest(data), build_name)


class VersionTests(unittest.TestCase):
    def test_semantic_version_order_and_prerelease(self):
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0"))
        self.assertLess(Version.parse("1.0.0-alpha.1"), Version.parse("1.0.0-alpha.2"))
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0-alpha.1"))
        self.assertLess(Version.parse("1.0.0-1"), Version.parse("1.0.0-alpha"))
        self.assertGreater(Version.parse("2.0.0"), Version.parse("1.99.99"))

    def test_constraints(self):
        self.assertTrue(Constraint.parse("^1.2.0").allows(Version.parse("1.9.0")))
        self.assertFalse(Constraint.parse("^1.2.0").allows(Version.parse("2.0.0")))
        self.assertTrue(Constraint.parse("~1.2.0").allows(Version.parse("1.2.9")))
        self.assertFalse(Constraint.parse("~1.2.0").allows(Version.parse("1.3.0")))
        self.assertTrue(Constraint.parse(">=1.0.0").allows(Version.parse("1.0.0")))
        self.assertFalse(Constraint.parse("!=1.0.0").allows(Version.parse("1.0.0")))


class ResolverTests(unittest.TestCase):
    def test_x_falls_back_from_conflicting_a_to_b(self):
        data = manifest(
            [
                package("blocker", "1.0.0"),
                package("A", "2.0.0", provides=[capability("X", "3.0.0")],
                        conflicts=["blocker"]),
                package("B", "1.5.0", provides=[capability("X", "2.9.0")]),
            ],
            requirements=["blocker", "X"],
        )
        selection = resolve_data(data)
        self.assertEqual(selection.capabilities["X"], "B")
        self.assertEqual(selection.implementations["X"], "B@1.5.0")
        self.assertNotIn("A", selection.selected_packages)

    def test_higher_capability_version_wins_then_name_breaks_ties(self):
        data = manifest(
            [
                package("A", "1.0.0", provides=[capability("X", "2.0.0")]),
                package("B", "1.0.0", provides=[capability("X", "2.0.0")]),
            ],
            requirements=["X"],
        )
        self.assertEqual(resolve_data(data).capabilities["X"], "A")

        data["packages"][0]["provides"][0]["version"] = "2.0.1"
        self.assertEqual(resolve_data(data).capabilities["X"], "A")

    def test_same_capability_has_single_provider_for_multiple_consumers(self):
        data = manifest(
            [
                package("A", "1.0.0", provides=[capability("X", "1.0.0")]),
                package("B", "1.0.0", provides=[capability("X", "1.0.0")]),
                package("consumer-one", "1.0.0", requires=["X >=1.0.0"]),
                package("consumer-two", "1.0.0", requires=["X <2.0.0"]),
            ],
            requirements=["consumer-one", "consumer-two"],
        )
        selection = resolve_data(data)
        self.assertEqual(selection.capabilities["X"], "A")
        self.assertEqual(sum(
            name in selection.selected_packages for name in ("A", "B")
        ), 1)

    def test_target_constraint_filters_implementation(self):
        data = manifest(
            [
                package("A", "3.0.0", provides=[capability("X", "3.0.0", ["windows"])]),
                package("B", "1.0.0", provides=[capability("X", "1.0.0", ["linux"])]),
            ],
            requirements=["X"],
            target="linux",
        )
        self.assertEqual(resolve_data(data).capabilities["X"], "B")

    def test_prerelease_is_excluded_unless_explicitly_requested(self):
        packages = [
            package("A", "1.0.0", provides=[capability("X", "2.0.0-rc.1")]),
            package("B", "1.0.0", provides=[capability("X", "1.0.0")]),
        ]
        self.assertEqual(resolve_data(manifest(packages, requirements=["X"])).capabilities["X"], "B")
        explicit = manifest(packages, requirements=["X >=2.0.0-rc.1"])
        self.assertEqual(resolve_data(explicit).capabilities["X"], "A")

    def test_alternative_group_chooses_first_feasible_option(self):
        data = manifest(
            [
                package("A", "1.0.0", provides=[capability("X", "1.0.0")],
                        conflicts=["blocker"]),
                package("B", "1.0.0", provides=[capability("Y", "1.0.0")]),
                package("blocker", "1.0.0"),
            ],
            requirements=[{"any_of": ["X", "Y"]}, "blocker"],
        )
        self.assertEqual(resolve_data(data).capabilities["Y"], "B")

    def test_later_requirement_can_force_earlier_backtracking(self):
        data = manifest(
            [
                package("A", "2.0.0", provides=[capability("X", "2.0.0")],
                        conflicts=["later"]),
                package("B", "1.0.0", provides=[capability("X", "1.0.0")]),
                package("later", "1.0.0"),
            ],
            requirements=["X", "later"],
        )
        selection = resolve_data(data)
        self.assertEqual(selection.capabilities["X"], "B")
        self.assertIn("later", selection.selected_packages)

    def test_mutual_capability_conflict_triggers_backtracking(self):
        data = manifest(
            [
                package("A", "1.0.0", provides=[capability("X", "1.0.0"), "a-extra"]),
                package("B", "1.0.0", provides=[capability("Y", "1.0.0")],
                        conflicts=[{"capability": "a-extra"}]),
                package("C", "1.0.0", provides=[capability("X", "0.9.0")],
                        requires=["Y"]),
            ],
            requirements=["X", "Y"],
        )
        selection = resolve_data(data)
        self.assertEqual(selection.capabilities["X"], "C")
        self.assertEqual(selection.capabilities["Y"], "B")

    def test_missing_version_has_no_solution(self):
        data = manifest(
            [package("A", "1.0.0", provides=[capability("X", "1.0.0")])],
            requirements=["X >=2.0.0"],
        )
        with self.assertRaisesRegex(SelectionError, "no eligible implementation"):
            resolve_data(data)

    def test_alternative_chain_cycle_terminates_without_recursion(self):
        data = manifest(
            [
                package("only-a", "1.0.0",
                        requires=[{"any_of": ["B"]}]),
                package("only-b", "1.0.0",
                        requires=[{"any_of": ["A"]}]),
                package("only-a-provider", "1.0.0", provides=["A"],
                        requires=["only-a"]),
                package("only-b-provider", "1.0.0", provides=["B"],
                        requires=["only-b"]),
            ],
            requirements=[{"any_of": ["A", "B"]}],
        )
        with self.assertRaises(SelectionError):
            resolve_data(data)

    def test_cyclic_alternative_path_falls_back_to_another_option(self):
        data = manifest(
            [
                package("loop-package", "1.0.0", provides=["loop-start"],
                        requires=[{"any_of": ["loop-start"]}]),
                package("safe", "1.0.0", provides=["safe-capability"]),
            ],
            requirements=[{"any_of": ["loop-start", "safe-capability"]}],
        )
        selection = resolve_data(data)
        self.assertEqual(selection.capabilities["safe-capability"], "safe")
        self.assertNotIn("loop-package", selection.selected_packages)

    def test_sample_manifest(self):
        sample = Path(__file__).resolve().parents[1] / "examples" / "sample_manifest.json"
        loaded = load_manifest_file(sample)
        self.assertEqual(resolve(loaded, "linux-app").capabilities["X"], "impl-b")
        self.assertTrue(json.loads(sample.read_text(encoding="utf-8"))["packages"])

    def test_cli_json_output(self):
        sample = Path(__file__).resolve().parents[1] / "examples" / "sample_manifest.json"
        output = io.StringIO()
        with redirect_stdout(output):
            exit_code = cli_main([str(sample), "--build", "linux-app", "--indent", "0"])
        self.assertEqual(exit_code, 0)
        payload = json.loads(output.getvalue())
        self.assertTrue(payload["ok"])
        self.assertEqual(payload["capability_to_implementation"]["X"], "impl-b@1.5.0")


if __name__ == "__main__":
    unittest.main()
