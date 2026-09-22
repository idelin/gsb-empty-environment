import unittest

from capability_resolver import (
    Build,
    InvalidInput,
    Package,
    Repository,
    ResolutionImpossible,
    Requirement,
    Resolver,
    parse_version,
)


def package(name, version, provides=(), requires=(), conflicts=(), target=None):
    return Package.from_dict(
        {
            "name": name,
            "version": version,
            "provides": list(provides),
            "requires": list(requires),
            "conflicts": list(conflicts),
            "target": target or {},
        }
    )


def resolve(raw_packages, requirements, target=None, allow_prerelease=False):
    build = Build.from_dict(
        {
            "requirements": list(requirements),
            "target": target or {},
            "allow_prerelease": allow_prerelease,
        }
    )
    return Resolver(Repository([package(**item) if isinstance(item, dict) else item for item in raw_packages]), build).resolve()


class VersionTests(unittest.TestCase):
    def test_semantic_version_order_and_prerelease(self):
        ordered = [
            parse_version("1.0.0-alpha"),
            parse_version("1.0.0-alpha.1"),
            parse_version("1.0.0-alpha.beta"),
            parse_version("1.0.0-beta"),
            parse_version("1.0.0-rc.1"),
            parse_version("1.0.0"),
            parse_version("1.0.1"),
            parse_version("1.2.0"),
        ]
        self.assertEqual(ordered, sorted(ordered))
        self.assertEqual(parse_version("1.0.0+build.7"), parse_version("1.0.0"))
        self.assertNotEqual(parse_version("1.0.0-rc.1"), parse_version("1.0.0"))

    def test_invalid_numeric_versions(self):
        with self.assertRaises(InvalidInput):
            parse_version("1.0")
        with self.assertRaises(InvalidInput):
            parse_version("01.0.0")
        with self.assertRaises(InvalidInput):
            parse_version("1.0.0-alpha.01")


class ResolverTests(unittest.TestCase):
    def test_conflicting_first_implementation_falls_back_to_alternative(self):
        result = resolve(
            [
                package("a", "2.0.0", provides=["x"], conflicts=["c"]),
                package("b", "1.0.0", provides=["x"]),
                package("c", "1.0.0", provides=["y"]),
            ],
            ["x", "y"],
        )
        self.assertEqual(result["capability_to_implementation"]["x"], "b==1.0.0")
        self.assertEqual(result["capability_to_implementation"]["y"], "c==1.0.0")

    def test_highest_version_wins_with_stable_name_tie_break(self):
        result = resolve(
            [
                package("zeta", "1.0.0", provides=["x"]),
                package("alpha", "1.0.0", provides=["x"]),
                package("middle", "1.2.0", provides=["x"]),
            ],
            ["x"],
        )
        self.assertEqual(result["capability_to_implementation"]["x"], "middle==1.2.0")
        result_equal = resolve(
            [
                package("zeta", "1.0.0", provides=["x"]),
                package("alpha", "1.0.0", provides=["x"]),
            ],
            ["x"],
        )
        self.assertEqual(result_equal["capability_to_implementation"]["x"], "alpha==1.0.0")

    def test_prerelease_is_filtered_unless_explicitly_allowed(self):
        packages = [
            package("a", "2.0.0-beta.1", provides=["x"]),
            package("a", "1.9.0", provides=["x"]),
        ]
        stable = resolve(packages, ["x"])
        self.assertEqual(stable["capability_to_implementation"]["x"], "a==1.9.0")
        prerelease = resolve(packages, ["x"], allow_prerelease=True)
        self.assertEqual(prerelease["capability_to_implementation"]["x"], "a==2.0.0-beta.1")

    def test_target_constraints_filter_candidates(self):
        result = resolve(
            [
                package("linux-x", "2.0.0", provides=["x"], target={"os": "linux"}),
                package("mac-x", "1.0.0", provides=["x"], target={"os": "macos"}),
            ],
            ["x"],
            target={"os": "macos", "arch": "arm64"},
        )
        self.assertEqual(result["capability_to_implementation"]["x"], "mac-x==1.0.0")

    def test_capability_has_a_single_provider(self):
        with self.assertRaises(ResolutionImpossible):
            resolve(
                [
                    package("a", "1.0.0", provides=["x"]),
                    package("b", "1.0.0", provides=["x"]),
                ],
                ["x", "x>=2.0.0"],
            )

        with self.assertRaises(ResolutionImpossible):
            resolve(
                [
                    package("a", "1.0.0", provides=["x"]),
                    package("b", "1.0.0", provides=["x"]),
                ],
                ["a", "b"],
            )

    def test_version_conflict_between_top_level_requirements_searches_alternatives(self):
        result = resolve(
            [
                package("a", "2.0.0", provides=["x"], requires=["y==1.0.0"]),
                package("a", "1.0.0", provides=["x"], requires=["y==1.0.0"]),
                package("b", "1.0.0", provides=["x"], requires=["y==2.0.0"]),
                package("y", "1.0.0"),
                package("y", "2.0.0"),
            ],
            ["x", "y==2.0.0"],
        )
        self.assertEqual(result["capability_to_implementation"]["x"], "b==1.0.0")
        self.assertEqual(result["capability_to_implementation"]["y"], "y==2.0.0")

    def test_dependency_cycle_is_detected(self):
        with self.assertRaises(ResolutionImpossible) as caught:
            resolve(
                [
                    package("a", "1.0.0", provides=["x"], requires=["y"]),
                    package("b", "1.0.0", provides=["y"], requires=["x"]),
                ],
                ["x"],
            )
        self.assertIn("cycle", str(caught.exception))

    def test_duplicate_package_is_invalid_input(self):
        with self.assertRaises(InvalidInput):
            Repository([package("a", "1.0.0"), package("a", "1.0.0")])

    def test_constraints_accumulate_for_same_capability(self):
        with self.assertRaises(ResolutionImpossible):
            resolve(
                [package("a", "1.5.0", provides=["x"])],
                ["x>=1.0.0", "x<1.5.0"],
            )

    def test_explicit_prerelease_requirement_uses_build_policy(self):
        requirement = Requirement.parse({"capability": "x", "constraints": [">=2.0.0-alpha.1"]})
        self.assertTrue(requirement.satisfied_by(parse_version("2.0.0-beta.1"), False))

    def test_provider_alias_does_not_claim_package_name_as_capability(self):
        result = resolve(
            [
                package("impl-a", "2.0.0", provides=["x"], requires=["helper==1.0.0"]),
                package("helper", "1.0.0"),
            ],
            ["x"],
        )
        self.assertEqual(result["capability_to_implementation"]["x"], "impl-a==2.0.0")
        self.assertEqual(result["capability_to_implementation"]["helper"], "helper==1.0.0")


if __name__ == "__main__":
    unittest.main()
