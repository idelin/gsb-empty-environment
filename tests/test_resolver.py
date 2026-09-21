import json
import unittest
from pathlib import Path

from capability_resolver import (
    CatalogError,
    ResolutionError,
    Requirement,
    Resolver,
    Target,
    Version,
    VersionError,
    load_build_request,
    load_catalog,
)
from capability_resolver.version import parse_constraint, satisfies_all

ROOT = Path(__file__).resolve().parent.parent


def catalog(packages):
    return load_catalog({"packages": packages})


def resolve(packages, requirements, target=None):
    cat = catalog(packages)
    reqs = tuple(Requirement.parse(r) for r in requirements)
    return Resolver(cat, Target.from_mapping(target)).resolve(reqs)


def chosen(resolution, capability):
    package = resolution.mapping[capability]
    return package.name, str(package.version)


class VersionTests(unittest.TestCase):
    def test_ordering_core(self):
        self.assertTrue(Version("1.2.3") < Version("1.2.4"))
        self.assertTrue(Version("1.10.0") > Version("1.9.0"))
        self.assertEqual(Version("2.0.0"), Version("2.0.0"))

    def test_prerelease_lower_than_release(self):
        self.assertTrue(Version("1.0.0-alpha") < Version("1.0.0"))
        self.assertTrue(Version("1.0.0-alpha.1") < Version("1.0.0-alpha.2"))
        self.assertTrue(Version("1.0.0-alpha") < Version("1.0.0-alpha.1"))
        self.assertTrue(Version("1.0.0-alpha") < Version("1.0.0-beta"))

    def test_invalid_versions(self):
        for bad in ["1.2", "1.2.x", "v1.2.3", "1.2.3-01", "1..3.0"]:
            with self.assertRaises(VersionError):
                Version(bad)

    def test_constraints(self):
        v = Version("2.1.0")
        self.assertTrue(parse_constraint(">=2.0.0").satisfies(v))
        self.assertFalse(parse_constraint("<2.1.0").satisfies(v))
        self.assertTrue(parse_constraint("!=3.0.0").satisfies(v))
        self.assertTrue(parse_constraint("2.1.0").satisfies(v))
        self.assertTrue(satisfies_all(v, [parse_constraint(">=2.0.0"), parse_constraint("<3.0.0")]))


class ResolverTests(unittest.TestCase):
    BASE = [
        {"name": "A", "version": "2.0.0", "provides": ["X"], "conflicts_with": ["C"]},
        {"name": "A", "version": "1.5.0", "provides": ["X"], "conflicts_with": ["C"]},
        {"name": "B", "version": "1.1.0", "provides": ["X"]},
        {"name": "C", "version": "1.0.0", "provides": ["Y"]},
    ]

    def test_single_capability_prefers_higher_version(self):
        res = resolve(self.BASE, ["X"])
        self.assertEqual(chosen(res, "X"), ("A", "2.0.0"))

    def test_conflict_falls_back_to_other_provider(self):
        # Y forces C, which conflicts with every A, so X must come from B.
        res = resolve(self.BASE, ["X", "Y"])
        self.assertEqual(chosen(res, "Y"), ("C", "1.0.0"))
        self.assertEqual(chosen(res, "X"), ("B", "1.1.0"))
        names = {p.name for p in res.selected}
        self.assertNotIn("A", names)

    def test_each_capability_has_single_provider(self):
        res = resolve(self.BASE, ["X", "Y"])
        self.assertEqual(set(res.mapping), {"X", "Y"})
        for package in res.mapping.values():
            self.assertIsInstance(package.version, Version)

    def test_version_constraint_filters_candidates(self):
        res = resolve(self.BASE, ["X<2.0.0"])
        self.assertEqual(chosen(res, "X"), ("A", "1.5.0"))

    def test_prerelease_deselected_when_release_exists(self):
        packages = self.BASE + [
            {"name": "A", "version": "2.0.0-rc.9", "provides": ["X"], "conflicts_with": ["C"]}
        ]
        res = resolve(packages, ["X"])
        self.assertEqual(chosen(res, "X"), ("A", "2.0.0"))

    def test_prerelease_used_when_only_option_matches(self):
        packages = [
            {"name": "A", "version": "2.0.0-rc.1", "provides": ["X"]},
            {"name": "A", "version": "1.9.0", "provides": ["X"]},
        ]
        res = resolve(packages, ["X>=2.0.0-rc.1"])
        self.assertEqual(chosen(res, "X"), ("A", "2.0.0-rc.1"))

    def test_no_provider_is_an_error(self):
        with self.assertRaises(ResolutionError):
            resolve(self.BASE, ["MISSING"])

    def test_no_version_satisfies_constraint(self):
        with self.assertRaises(ResolutionError):
            resolve(self.BASE, ["X>=9.9.9"])

    def test_tie_breaks_by_provider_name(self):
        packages = [
            {"name": "H", "version": "1.0.0", "provides": ["TIE"]},
            {"name": "I", "version": "1.0.0", "provides": ["TIE"]},
        ]
        res = resolve(packages, ["TIE"])
        self.assertEqual(chosen(res, "TIE"), ("H", "1.0.0"))

    def test_target_requires_and_excludes(self):
        packages = [
            {"name": "L", "version": "1.0.0", "provides": ["K"],
             "requires_target": [{"os": "linux"}]},
            {"name": "M", "version": "1.0.0", "provides": ["K"],
             "excludes_target": [{"os": "linux"}]},
        ]
        on_linux = resolve(packages, ["K"], target={"os": "linux"})
        self.assertEqual(chosen(on_linux, "K"), ("L", "1.0.0"))
        on_mac = resolve(packages, ["K"], target={"os": "macos"})
        self.assertEqual(chosen(on_mac, "K"), ("M", "1.0.0"))

    def test_alternative_chain_inherits_capability(self):
        packages = [
            {"name": "G", "version": "1.0.0", "provides": ["Q"], "replaced_by": "G2"},
            {"name": "G2", "version": "2.0.0", "provides": []},
        ]
        res = resolve(packages, ["Q"])
        self.assertEqual(chosen(res, "Q"), ("G2", "2.0.0"))

    def test_alternative_cycle_is_detected(self):
        packages = [
            {"name": "P", "version": "1.0.0", "provides": ["R"], "replaced_by": "Q"},
            {"name": "Q", "version": "1.0.0", "provides": [], "replaced_by": "P"},
        ]
        with self.assertRaises(ResolutionError) as ctx:
            resolve(packages, ["R"])
        self.assertIn("cycle", str(ctx.exception))

    def test_three_node_cycle_does_not_recurse_forever(self):
        packages = [
            {"name": "P", "version": "1.0.0", "provides": ["R"], "replaced_by": "Q"},
            {"name": "Q", "version": "1.0.0", "provides": [], "replaced_by": "S"},
            {"name": "S", "version": "1.0.0", "provides": [], "replaced_by": "P"},
        ]
        with self.assertRaises(ResolutionError):
            resolve(packages, ["R"])

    def test_conflict_drives_coexistence_choice(self):
        packages = [
            {"name": "A", "version": "1.0.0", "provides": ["X"], "conflicts_with": ["B"]},
            {"name": "B", "version": "1.0.0", "provides": ["X"], "conflicts_with": ["A"]},
            {"name": "C", "version": "1.0.0", "provides": ["Y"], "conflicts_with": ["B"]},
        ]
        # A and C coexist; B is blocked by both. Stable pick is A (name order).
        res = resolve(packages, ["X", "Y"])
        self.assertEqual(chosen(res, "X"), ("A", "1.0.0"))
        self.assertEqual(chosen(res, "Y"), ("C", "1.0.0"))

    def test_all_providers_conflict_is_error(self):
        packages = [
            {"name": "A", "version": "1.0.0", "provides": ["X"], "conflicts_with": ["D"]},
            {"name": "B", "version": "1.0.0", "provides": ["X"], "conflicts_with": ["D"]},
            {"name": "D", "version": "1.0.0", "provides": ["Y"]},
        ]
        with self.assertRaises(ResolutionError):
            resolve(packages, ["X", "Y"])

    def test_deterministic_across_runs(self):
        first = resolve(self.BASE, ["X", "Y"]).to_dict()
        second = resolve(self.BASE, ["X", "Y"]).to_dict()
        self.assertEqual(first, second)

    def test_same_package_serves_two_capabilities_once(self):
        packages = [
            {"name": "Multi", "version": "1.0.0", "provides": ["X", "Y"]},
        ]
        res = resolve(packages, ["X", "Y"])
        self.assertEqual(len(res.selected), 1)
        self.assertEqual(chosen(res, "X"), chosen(res, "Y"))


class CatalogValidationTests(unittest.TestCase):
    def test_duplicate_package_version_rejected(self):
        with self.assertRaises(CatalogError):
            catalog([
                {"name": "A", "version": "1.0.0", "provides": ["X"]},
                {"name": "A", "version": "1.0.0", "provides": ["X"]},
            ])

    def test_unknown_alternative_rejected(self):
        with self.assertRaises(CatalogError):
            catalog([{"name": "A", "version": "1.0.0", "provides": ["X"],
                      "replaced_by": "GHOST"}])

    def test_inconsistent_alternative_rejected(self):
        with self.assertRaises(CatalogError):
            catalog([
                {"name": "A", "version": "1.0.0", "provides": ["X"], "replaced_by": "B"},
                {"name": "A", "version": "2.0.0", "provides": ["X"], "replaced_by": "C"},
                {"name": "B", "version": "1.0.0", "provides": []},
                {"name": "C", "version": "1.0.0", "provides": []},
            ])

    def test_self_alternative_rejected(self):
        with self.assertRaises(CatalogError):
            catalog([{"name": "A", "version": "1.0.0", "provides": ["X"],
                      "replaced_by": "A"}])


class JsonIntegrationTests(unittest.TestCase):
    def test_example_files_resolve(self):
        cat = load_catalog(ROOT / "examples" / "catalog.json")
        build = load_build_request(ROOT / "examples" / "build.json")
        res = Resolver(cat, build.target).resolve(build.requirements)
        mapping = res.to_dict()["mapping"]
        self.assertEqual(mapping["X"], {"package": "B", "version": "1.1.0"})
        self.assertEqual(mapping["Y"], {"package": "C", "version": "1.0.0"})
        self.assertEqual(mapping["W"], {"package": "F", "version": "1.0.0"})
        self.assertEqual(mapping["Q"], {"package": "G2", "version": "2.0.0"})
        self.assertEqual(mapping["Z"], {"package": "D", "version": "1.0.0"})
        self.assertEqual(mapping["TIE"], {"package": "H", "version": "1.0.0"})

    def test_cycle_example_errors(self):
        cat = load_catalog(ROOT / "examples" / "catalog_cycle.json")
        build = load_build_request(ROOT / "examples" / "build_cycle.json")
        with self.assertRaises(ResolutionError):
            Resolver(cat, build.target).resolve(build.requirements)

    def test_output_is_json_serializable(self):
        packages = [
            {"name": "A", "version": "1.0.0", "provides": ["X"], "conflicts_with": ["C"]},
            {"name": "B", "version": "1.0.0", "provides": ["X"]},
            {"name": "C", "version": "1.0.0", "provides": ["Y"]},
        ]
        res = resolve(packages, ["X", "Y"])
        encoded = json.dumps(res.to_dict())
        self.assertIn("decision_policy", encoded)


if __name__ == "__main__":
    unittest.main(verbosity=2)
