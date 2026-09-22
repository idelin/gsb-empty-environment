import itertools
import unittest
from pathlib import Path

from vsets import (
    BuildInput,
    Dependency,
    PackageIndex,
    Version,
    brute_all_solutions,
    brute_solve,
    explain,
    solve,
    trace_lock_change,
)
from vsets.solver import Unsatisfiable
from vsets.target import ConditionalVersionSet

FIXTURES = Path(__file__).parent / "fixtures"


def dep(name: str, spec: str) -> Dependency:
    return Dependency(name, ConditionalVersionSet.parse(spec))


class CompatibleCycleTest(unittest.TestCase):
    def setUp(self):
        self.index = PackageIndex.load(FIXTURES / "packages.json")

    def test_mutual_cycle_resolves_to_highest_pair(self):
        roots = [dep("a", "[1.0.0,3.0.0)")]
        solution = solve(self.index, roots).as_dict()
        brute = brute_solve(self.index, roots)
        self.assertEqual(solution, {"a": "2.0.0", "b": "2.0.0"})
        self.assertEqual(brute.as_dict(), solution)

    def test_prerelease_cycle_requires_explicit_channels(self):
        roots = [dep("a", "[2.0.0,3.0.0)@alpha")]
        build = BuildInput.load(FIXTURES / "build_prerelease.json")
        solution = solve(self.index, build.roots, build.context)
        self.assertEqual(solution.as_dict(), {"a": "2.0.0-alpha.1", "b": "2.0.0-alpha.1"})
        brute = brute_solve(self.index, roots, build.context)
        self.assertEqual(brute.as_dict(), solution.as_dict())

        release_only = solve(self.index, [dep("a", "[2.0.0,3.0.0)")])
        self.assertEqual(release_only.as_dict(), {"a": "2.0.0", "b": "2.0.0"})

    def test_target_condition_gates_transitive_dependency(self):
        build = BuildInput.load(FIXTURES / "build_target.json")
        solution = solve(self.index, build.roots, build.context)
        self.assertEqual(solution.as_dict(), {"codec": "2.0.0", "img": "2.0.0"})

        without = solve(self.index, build.roots, {"format": "jpeg"})
        self.assertEqual(without.as_dict(), {"img": "2.0.0"})
        self.assertEqual(
            brute_solve(self.index, build.roots, build.context).as_dict(),
            solution.as_dict(),
        )


class ConflictingCycleTest(unittest.TestCase):
    def setUp(self):
        self.index = PackageIndex.load(FIXTURES / "packages.json")

    def test_c_d_cycle_has_no_valid_assignment(self):
        roots = [dep("c", "[1.0.0,3.0.0)")]
        self.assertIsNone(brute_solve(self.index, roots))
        with self.assertRaises(Unsatisfiable) as caught:
            solve(self.index, roots)
        explanation = explain(caught.exception)
        self.assertIn("c", explanation.package)
        self.assertTrue(explanation.paths)
        self.assertTrue(explanation.constraint_sets)

    def test_too_tight_root_is_unsatisfiable(self):
        roots = [dep("a", "[9.0.0,*)")]
        self.assertIsNone(brute_solve(self.index, roots))
        with self.assertRaises(Unsatisfiable):
            solve(self.index, roots)


class ExhaustiveCycleCrossCheck(unittest.TestCase):
    SPECS = [
        "[1.0.0,2.0.0)",
        "[2.0.0,3.0.0)",
        "[3.0.0,4.0.0)",
        "[1.0.0,3.0.0)",
        "[2.0.0,4.0.0)",
        "[1.0.0,4.0.0)",
    ]
    ROOT_SPECS = SPECS + ["[1.0.0,4.0.0)", "[1.0.0,2.0.0) | [3.0.0,4.0.0)"]
    VERSIONS = ["1.0.0", "2.0.0", "3.0.0"]

    def _build_index(self, spec_x, spec_y):
        return PackageIndex.from_dict({
            "packages": {
                "x": {version: {"dependencies": {"y": spec_x}} for version in self.VERSIONS},
                "y": {version: {"dependencies": {"x": spec_y}} for version in self.VERSIONS},
            }
        })

    def test_every_cycle_matrix_cell_matches_bruteforce(self):
        checked = 0
        for spec_x, spec_y in itertools.product(self.SPECS, repeat=2):
            index = self._build_index(spec_x, spec_y)
            for root_spec in self.ROOT_SPECS:
                roots = [dep("x", root_spec)]
                brute_solutions = brute_all_solutions(index, roots)
                brute_versions = {
                    tuple(sorted(solution.chosen.items())) for solution in brute_solutions
                }
                try:
                    smart = solve(index, roots)
                except Unsatisfiable:
                    self.assertFalse(
                        brute_versions,
                        msg=f"smart unsat but brute had {brute_versions} "
                            f"for x->{spec_x}, y->{spec_y}, root {root_spec}",
                    )
                    continue
                smart_pair = tuple(sorted(smart.chosen.items()))
                self.assertIn(
                    smart_pair,
                    brute_versions,
                    msg=f"smart {smart_pair} not a brute-valid assignment "
                        f"(x->{spec_x}, y->{spec_y}, root {root_spec})",
                )
                self.assertTrue(brute_versions)
                checked += 1
        self.assertGreater(checked, 100)


class UnboundedAndAbuttingTest(unittest.TestCase):
    def setUp(self):
        self.index = PackageIndex.load(FIXTURES / "packages.json")

    def test_unbounded_upper_end_and_open_abutment(self):
        roots = [dep("web", "[1.0.0,3.0.0)")]
        solution = solve(self.index, roots)
        self.assertEqual(solution.as_dict(), {"http": "2.0.0", "sock": "2.0.0", "web": "2.0.0"})
        brute = brute_solve(self.index, roots)
        self.assertEqual(brute.as_dict(), solution.as_dict())

        exact_boundary = solve(self.index, [dep("http", "[*,2.0.0)")])
        self.assertEqual(exact_boundary.chosen["http"], Version.parse("1.0.0"))
        self.assertEqual(exact_boundary.chosen["sock"], Version.parse("1.0.0"))

        abutted = solve(self.index, [dep("web", "[1.0.0,2.0.0)")])
        self.assertEqual(abutted.chosen["web"], Version.parse("1.0.0"))
        self.assertEqual(abutted.chosen["http"], Version.parse("2.0.0"))
        self.assertEqual(abutted.chosen["sock"], Version.parse("2.0.0"))

    def test_open_boundary_excludes_endpoint(self):
        roots = [dep("sock", "[*,2.0.0)")]
        self.assertEqual(solve(self.index, roots).chosen["sock"], Version.parse("1.0.0"))
        roots2 = [dep("sock", "[2.0.0,*)")]
        self.assertEqual(solve(self.index, roots2).chosen["sock"], Version.parse("2.0.0"))


class LockChangeTest(unittest.TestCase):
    def setUp(self):
        self.index = PackageIndex.load(FIXTURES / "packages.json")
        self.old = BuildInput.load(FIXTURES / "build_lock_old.json")
        self.new = BuildInput.load(FIXTURES / "build_lock_new.json")

    def test_report_traces_constraint_that_changed_selection(self):
        report = trace_lock_change(
            self.index,
            self.old.roots,
            self.new.context,
            self.old.locked,
            self.new.locked,
        )
        self.assertEqual(str(report.before["web"]), "1.0.0")
        self.assertEqual(str(report.after["web"]), "2.0.0")
        by_package = {cause.package: cause for cause in report.causes}
        self.assertIn("web", by_package)
        self.assertEqual(by_package["web"].reason, "forced to new locked pin")
        self.assertIn("http", by_package)
        self.assertEqual(by_package["http"].reason, "forced to new locked pin")

    def test_lock_inducing_conflict_records_core(self):
        impossible = {"web": Version.parse("2.0.0"), "http": Version.parse("1.0.0")}
        report = trace_lock_change(
            self.index, self.old.roots, {}, self.old.locked, impossible
        )
        self.assertIsNotNone(report.unsatisfiable_after)
        self.assertTrue(any(cause.new_version is None for cause in report.causes))


if __name__ == "__main__":
    unittest.main()
