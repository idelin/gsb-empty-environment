import random
import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from license_solver import (
    FEASIBLE_UPPER_BOUND,
    INFEASIBLE,
    OPTIMAL,
    UNKNOWN,
    Solver,
    brute_force,
    build_from_dict,
    greedy_select,
    load_build,
    load_repository,
    repository_from_dict,
)

SAMPLES = Path(__file__).resolve().parent.parent / "samples"

RULES = {
    "propagation": [
        {"tag": "copyleft-strong", "links": ["static", "dynamic"],
         "obligations": ["disclose"]},
        {"tag": "copyleft-weak", "links": ["static"], "obligations": ["disclose"]},
    ],
    "incompatible": [["GPL-2.0-only", "Apache-2.0"]],
    "exception_required": [{"tag": "copyleft-strong", "when": {"proprietary": True}}],
}

LICENSE_DB = {
    "MIT": {"obligations": ["notice"], "tags": ["permissive"]},
    "Apache-2.0": {"obligations": ["notice"], "tags": ["permissive"]},
    "LGPL-2.1-only": {"obligations": ["notice", "disclose"], "tags": ["copyleft-weak"]},
    "GPL-2.0-only": {"obligations": ["notice", "disclose", "same-license"],
                     "tags": ["copyleft-strong"]},
}


class GreedyTrapTest(unittest.TestCase):
    """Three packages (left/right/shared): every per-package local pick is
    valid, yet the naive combination is globally infeasible."""

    def setUp(self):
        self.repo = load_repository(SAMPLES / "repo.json")
        self.build = load_build(SAMPLES / "build_trap.json")

    def test_greedy_paints_itself_into_a_corner(self):
        greedy = greedy_select(self.repo, self.build)
        self.assertFalse(greedy.ok)
        self.assertIn("shared", greedy.unresolved)
        chosen = {n: str(pv.version) for n, (pv, _c) in greedy.choices.items()}
        self.assertEqual(chosen.get("left"), "2.0.0")  # highest local pick

    def test_solver_finds_feasible_alternative_branch(self):
        result = Solver(self.repo, self.build).solve()
        self.assertEqual(result.status, OPTIMAL)
        self.assertEqual(result.cost, (0, 0))
        self.assertEqual(str(result.selection["left"].version), "1.0.0")
        self.assertEqual(result.selection["left"].branches, (0,))  # MIT side
        self.assertEqual(str(result.selection["shared"].version), "1.0.0")
        self.assertEqual(str(result.selection["right"].version), "1.0.0")

    def test_solver_matches_brute_force(self):
        result = Solver(self.repo, self.build).solve()
        brute = brute_force(self.repo, self.build)
        self.assertEqual(result.cost, brute.optimal_cost)
        self.assertGreater(brute.feasible_count, 0)


class DiamondTest(unittest.TestCase):
    def test_shared_dependency_counted_once(self):
        repo = load_repository(SAMPLES / "repo.json")
        build = load_build(SAMPLES / "build_diamond.json")
        result = Solver(repo, build).solve()
        self.assertEqual(result.status, OPTIMAL)
        # jsonlib (LGPL, disclose) is shared by ui and net; the dynamic link
        # edges block weak-copyleft propagation, so exactly one package
        # carries the disclosure obligation.
        self.assertEqual(result.cost, (0, 1))
        disclosers = [n for n, s in result.selection.items()
                      if "disclose" in s.obligations]
        self.assertEqual(disclosers, ["jsonlib"])


class WithTest(unittest.TestCase):
    def setUp(self):
        self.repo = load_repository(SAMPLES / "repo.json")
        self.build = load_build(SAMPLES / "build_with.json")

    def test_exception_condition_holds(self):
        self.build.attributes["independent_modules"] = True
        result = Solver(self.repo, self.build).solve()
        self.assertEqual(result.cost, (0, 0))

    def test_exception_condition_fails(self):
        self.build.attributes["independent_modules"] = False
        result = Solver(self.repo, self.build).solve()
        # base GPL obligations stand: 1 exception (strong copyleft in a
        # proprietary target) and 1 disclosure.
        self.assertEqual(result.cost, (1, 1))
        self.assertTrue(result.selection["guilib"].needs_exception)


class BudgetTest(unittest.TestCase):
    def setUp(self):
        self.repo = load_repository(SAMPLES / "repo.json")
        self.build = load_build(SAMPLES / "build_trap.json")
        self.full = Solver(self.repo, self.build).solve()
        self.assertEqual(self.full.status, OPTIMAL)

    def test_tiny_budget_reports_unknown_not_success(self):
        result = Solver(self.repo, self.build, budget=1).solve()
        self.assertEqual(result.status, UNKNOWN)
        self.assertIsNone(result.selection)
        self.assertIn("undecided", result.message)
        # must explicitly refuse to claim full constraint satisfaction
        self.assertIn("no claim", result.message)

    def test_partial_budget_gives_feasible_upper_bound(self):
        result = Solver(self.repo, self.build, budget=self.full.nodes - 1).solve()
        self.assertIn(result.status, (FEASIBLE_UPPER_BOUND, UNKNOWN))
        self.assertNotEqual(result.status, OPTIMAL)
        self.assertIn("undecided", result.message)
        if result.feasible:
            # an upper bound: never better than the proven optimum
            self.assertGreaterEqual(result.cost, self.full.cost)


def _random_instance(rng):
    names = ["p0", "p1", "p2", "p3"]
    licenses = ["MIT", "Apache-2.0", "GPL-2.0-only", "LGPL-2.1-only",
                "MIT OR GPL-2.0-only"]
    packages = {}
    for i, name in enumerate(names):
        versions = [{"version": "1.0.0", "license": rng.choice(licenses),
                     "deps": {}}]
        if rng.random() < 0.5:
            versions.append({"version": "2.0.0", "license": rng.choice(licenses),
                             "deps": {}})
        for entry in versions:
            for j in range(i):
                if rng.random() < 0.5:
                    entry["deps"][names[j]] = rng.choice(
                        ["*", ">=1.0.0 <2.0.0", ">=2.0.0"])
        packages[name] = {"versions": versions}
    repo = repository_from_dict({"licenses": LICENSE_DB, "exceptions": {},
                                 "packages": packages})
    build = build_from_dict({
        "target": "t",
        "link": rng.choice(["static", "dynamic"]),
        "attributes": {"proprietary": rng.random() < 0.5},
        "roots": {"p3": "*"},
        "rules": RULES,
    })
    return repo, build


class CrossCheckTest(unittest.TestCase):
    def test_solver_matches_exhaustive_enumeration(self):
        rng = random.Random(20260921)
        checked = 0
        for _ in range(60):
            repo, build = _random_instance(rng)
            result = Solver(repo, build, budget=100000).solve()
            brute = brute_force(repo, build)
            if brute.optimal_cost is None:
                self.assertEqual(result.status, INFEASIBLE)
            else:
                self.assertEqual(result.status, OPTIMAL)
                self.assertEqual(result.cost, brute.optimal_cost)
            checked += 1
        self.assertGreaterEqual(checked, 60)


if __name__ == "__main__":
    unittest.main()
