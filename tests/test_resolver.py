import unittest

from depres import Constraint, Registry, ResolutionConflict, Version, resolve


def make_registry(packages):
    return Registry.from_dict(packages)


def reqs(specs):
    return {name: Constraint.parse(spec) for name, spec in specs.items()}


def solved(solution):
    return {name: str(version) for name, version in sorted(solution.items())}


class TestResolveBasic(unittest.TestCase):
    def test_picks_newest_matching_version(self):
        registry = make_registry({
            "A": {"1.0.0": {}, "1.2.0": {}, "2.0.0": {}},
        })
        solution = resolve(registry, reqs({"A": ">=1.0.0, <2.0.0"}))
        self.assertEqual(solved(solution), {"A": "1.2.0"})

    def test_transitive_dependencies(self):
        registry = make_registry({
            "A": {"1.0.0": {"dependencies": {"B": ">=1.0.0"}}},
            "B": {"1.0.0": {"dependencies": {"C": "<2.0.0"}},
                  "2.0.0": {"dependencies": {"C": ">=2.0.0"}}},
            "C": {"1.5.0": {}, "2.1.0": {}},
        })
        solution = resolve(registry, reqs({"A": "*", "C": ">=1.0.0"}))
        self.assertEqual(
            solved(solution), {"A": "1.0.0", "B": "2.0.0", "C": "2.1.0"}
        )

    def test_exclusion_point_forces_older_version(self):
        registry = make_registry({
            "A": {"1.0.0": {}, "1.5.0": {}, "1.6.0": {}},
        })
        solution = resolve(registry, reqs({"A": ">=1.0.0, <2.0.0, !=1.6.0"}))
        self.assertEqual(solved(solution), {"A": "1.5.0"})

    def test_backtracking_downgrades_earlier_package(self):
        registry = make_registry({
            "A": {"1.0.0": {"dependencies": {"C": "<2.0.0"}},
                  "2.0.0": {"dependencies": {"C": ">=2.0.0"}}},
            "B": {"1.0.0": {"dependencies": {"C": "<2.0.0"}}},
            "C": {"1.9.0": {}, "2.0.0": {}},
        })
        solution = resolve(registry, reqs({"A": "*", "B": "*"}))
        self.assertEqual(
            solved(solution), {"A": "1.0.0", "B": "1.0.0", "C": "1.9.0"}
        )

    def test_lexicographic_resolution_order(self):
        registry = make_registry({
            "B": {"1.0.0": {"dependencies": {"D": "<2.0.0"}}},
            "A": {"1.0.0": {"dependencies": {"D": ">=2.0.0"}}},
            "D": {"1.0.0": {}, "2.0.0": {}},
        })
        # A is resolved first (lexicographic), so D gets >=2.0.0 from A and
        # <2.0.0 from B; the reported conflict is on D with both constraints.
        with self.assertRaises(ResolutionConflict) as ctx:
            resolve(registry, reqs({"A": "*", "B": "*"}))
        self.assertEqual(ctx.exception.package, "D")
        report = ctx.exception.report()
        self.assertIn(">=2.0.0", report)
        self.assertIn("<2.0.0", report)


class TestResolveConflict(unittest.TestCase):
    def test_conflicting_ranges_report_constraint_set(self):
        registry = make_registry({
            "A": {"1.0.0": {"dependencies": {"C": "<2.0.0"}}},
            "B": {"1.0.0": {"dependencies": {"C": ">=2.0.0"}}},
            "C": {"1.0.0": {}, "2.0.0": {}},
        })
        with self.assertRaises(ResolutionConflict) as ctx:
            resolve(registry, reqs({"A": "*", "B": "*"}))
        error = ctx.exception
        self.assertEqual(error.package, "C")
        report = error.report()
        self.assertIn("<2.0.0", report)
        self.assertIn(">=2.0.0", report)
        self.assertIn("A@1.0.0", report)
        self.assertIn("B@1.0.0", report)

    def test_unknown_package_conflicts(self):
        registry = make_registry({})
        with self.assertRaises(ResolutionConflict) as ctx:
            resolve(registry, reqs({"ghost": "*"}))
        self.assertEqual(ctx.exception.package, "ghost")
        self.assertIn("root", ctx.exception.report())


class TestPrereleasePolicy(unittest.TestCase):
    def setUp(self):
        self.registry = make_registry({
            "A": {"1.0.0": {}, "2.0.0-beta.1": {}},
        })

    def test_prerelease_excluded_by_default(self):
        solution = resolve(self.registry, reqs({"A": "*"}))
        self.assertEqual(solved(solution), {"A": "1.0.0"})

    def test_prerelease_used_when_explicitly_allowed(self):
        solution = resolve(self.registry, reqs({"A": ">=2.0.0-beta.1"}))
        self.assertEqual(solved(solution), {"A": "2.0.0-beta.1"})

    def test_prerelease_via_transitive_constraint(self):
        registry = make_registry({
            "top": {"1.0.0": {"dependencies": {"lib": ">=2.0.0-rc.1"}}},
            "lib": {"1.0.0": {}, "2.0.0-rc.1": {}},
        })
        solution = resolve(registry, reqs({"top": "*"}))
        self.assertEqual(solved(solution), {"top": "1.0.0", "lib": "2.0.0-rc.1"})


class TestCyclicDependencies(unittest.TestCase):
    def test_compatible_cycle_resolves(self):
        registry = make_registry({
            "A": {"1.0.0": {"dependencies": {"B": ">=1.0.0, <2.0.0"}}},
            "B": {"1.5.0": {"dependencies": {"A": ">=1.0.0"}}},
        })
        solution = resolve(registry, reqs({"A": "*"}))
        self.assertEqual(solved(solution), {"A": "1.0.0", "B": "1.5.0"})

    def test_incompatible_cycle_conflicts(self):
        registry = make_registry({
            "A": {"1.0.0": {"dependencies": {"B": ">=1.0.0"}}},
            "B": {"1.0.0": {"dependencies": {"A": ">=2.0.0"}}},
        })
        with self.assertRaises(ResolutionConflict) as ctx:
            resolve(registry, reqs({"A": "*"}))
        self.assertEqual(ctx.exception.package, "A")


class TestVersionTypes(unittest.TestCase):
    def test_solution_values_are_versions(self):
        registry = make_registry({"A": {"1.0.0": {}}})
        solution = resolve(registry, reqs({"A": "*"}))
        self.assertIsInstance(solution["A"], Version)


if __name__ == "__main__":
    unittest.main()
