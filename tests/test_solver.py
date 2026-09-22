import json
import random
import unittest
from pathlib import Path

from licsel.expr import Atom
from licsel.model import compute_instances, load_problem
from licsel.solver import (
    STATUS_FEASIBLE_UPPER_BOUND,
    STATUS_INFEASIBLE,
    STATUS_OPTIMAL,
    STATUS_UNKNOWN,
    Solver,
    enumerate_optimal,
    instance_holds,
)

SAMPLES = Path(__file__).resolve().parent.parent / "samples"


def load_sample(name):
    return load_problem(str(SAMPLES / name))


class TestThreePackageGlobalConflict(unittest.TestCase):
    """app -dynamic-> libX (BSD OR GPL-3.0) -static-> libY (GPL-3.0).

    Locally, libX=BSD-2-Clause is the cheapest choice for libX and libY's
    only choice is fine on its own; but jointly they violate the
    static-dependents propagation rule. The solver must pick the
    alternative branch libX=GPL-3.0.
    """

    @classmethod
    def setUpClass(cls):
        cls.problem = load_sample("three_package_global.json")

    def test_greedy_local_choice_is_globally_infeasible(self):
        greedy = {
            "app@1.0.0": (Atom("MIT"),),
            "libX@1.0.0": (Atom("BSD-2-Clause"),),  # locally cheapest
            "libY@1.0.0": (Atom("GPL-3.0"),),
        }
        instances = compute_instances(self.problem)
        self.assertTrue(instances, "expected at least one rule instance")
        self.assertFalse(
            all(instance_holds(self.problem, i, greedy) for i in instances),
            "per-package greedy assignment should violate the global rule",
        )

    def test_solver_finds_feasible_alternative_branch(self):
        result = Solver(self.problem).solve()
        self.assertEqual(result.status, STATUS_OPTIMAL)
        self.assertEqual(result.choices["libX@1.0.0"], (Atom("GPL-3.0"),))
        self.assertEqual(result.objective, (0, 2))  # 0 exceptions, libX+libY disclosed
        self.assertEqual(set(result.disclosures), {"libX@1.0.0", "libY@1.0.0"})

    def test_matches_brute_force(self):
        best, checked = enumerate_optimal(self.problem)
        self.assertEqual(checked, 2)  # only libX has a choice
        self.assertEqual(best, (0, 2))


class TestWithNotUnconditional(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.problem = load_sample("with_exception.json")

    def test_exception_branch_chosen_and_costed(self):
        result = Solver(self.problem).solve()
        self.assertEqual(result.status, STATUS_OPTIMAL)
        self.assertEqual(
            result.choices["libZ@1.0.0"],
            (Atom("GPL-2.0", "Classpath-2.0"),),
        )
        self.assertEqual(result.objective, (1, 1))  # the WITH costs one exception

    def test_plain_branch_without_exception_is_infeasible(self):
        plain = {
            "app@1.0.0": (Atom("MIT"),),
            "libZ@1.0.0": (Atom("GPL-2.0"),),  # no exception attached
        }
        instances = compute_instances(self.problem)
        self.assertFalse(
            all(instance_holds(self.problem, i, plain) for i in instances),
            "WITH must not be treated as an unconditional allowance",
        )


class TestSharedDependencyCountedOnce(unittest.TestCase):
    def test_diamond(self):
        problem = load_sample("diamond_shared.json")
        result = Solver(problem).solve()
        self.assertEqual(result.status, STATUS_OPTIMAL)
        # libC is shared by libA and libB but counts once
        self.assertEqual(result.objective, (0, 2))
        self.assertEqual(set(result.disclosures), {"libB@1.0.0", "libC@1.0.0"})


class TestBudgetExhaustion(unittest.TestCase):
    def test_upper_bound_not_success_claim(self):
        problem = load_sample("budget.json")
        result = Solver(problem, budget=12).solve()
        self.assertEqual(result.status, STATUS_FEASIBLE_UPPER_BOUND)
        self.assertFalse(result.complete)
        self.assertTrue(result.pending)
        self.assertIsNotNone(result.objective)  # a feasible upper bound exists

    def test_unknown_before_any_feasible(self):
        problem = load_sample("budget.json")
        result = Solver(problem, budget=3).solve()
        self.assertEqual(result.status, STATUS_UNKNOWN)
        self.assertFalse(result.complete)
        self.assertIsNone(result.objective)

    def test_generous_budget_proves_optimum(self):
        problem = load_sample("budget.json")
        result = Solver(problem, budget=100_000).solve()
        self.assertEqual(result.status, STATUS_OPTIMAL)
        self.assertEqual(result.objective, (0, 0))  # all-MIT is feasible and best


class TestInfeasible(unittest.TestCase):
    def test_infeasible_status(self):
        problem = load_sample("conflict_core.json")
        result = Solver(problem).solve()
        self.assertEqual(result.status, STATUS_INFEASIBLE)
        self.assertTrue(result.complete)
        self.assertIsNone(result.objective)


# ------------------------------------------------------------ random cross-check

_LICENSES = ["MIT", "GPL-3.0", "LGPL-2.1", "BSD-2-Clause"]
_EXPRS = [
    "MIT",
    "GPL-3.0",
    "MIT OR GPL-3.0",
    "LGPL-2.1 OR MIT",
    "BSD-2-Clause OR LGPL-2.1",
    "GPL-2.0 WITH Classpath-2.0 OR MIT",
]


def _random_problem_dict(seed: int) -> dict:
    rng = random.Random(seed)
    n = rng.randint(2, 5)
    packages = []
    for i in range(n):
        deps = []
        for j in range(i + 1, n):
            if rng.random() < 0.5:
                deps.append({
                    "name": f"p{j}",
                    "constraint": "1.0.0",
                    "link": rng.choice(["static", "dynamic"]),
                })
        packages.append({
            "name": f"p{i}",
            "version": "1.0.0",
            "license": rng.choice(_EXPRS),
            "dependencies": deps,
        })
    rules = {
        "license_tags": {
            "MIT": ["permissive"],
            "BSD-2-Clause": ["permissive"],
            "LGPL-2.1": ["weak-copyleft"],
            "GPL-2.0": ["strong-copyleft"],
            "GPL-3.0": ["strong-copyleft"],
        },
        "disclosure_tags": ["weak-copyleft", "strong-copyleft"],
        "propagation": [
            {
                "id": "r-static",
                "trigger": {"link": "static", "tag": "strong-copyleft"},
                "scope": "static_dependents",
                "require_tag_in": ["strong-copyleft", "weak-copyleft"],
            },
            {
                "id": "r-dynamic",
                "trigger": {"link": "dynamic", "tag": "strong-copyleft"},
                "scope": "direct_dependents",
                "require_tag_in": ["strong-copyleft", "weak-copyleft"],
                "unless_exception": "Classpath-2.0",
            },
        ],
    }
    return {
        "rules": rules,
        "targets": [{
            "name": "t",
            "conditions": {},
            "roots": [{"name": "p0", "constraint": "1.0.0"}],
        }],
        "packages": packages,
    }


class TestEnumerationCrossCheck(unittest.TestCase):
    def test_solver_matches_brute_force_on_random_small_graphs(self):
        for seed in range(40):
            with self.subTest(seed=seed):
                problem = load_problem(_random_problem_dict(seed))
                best, _ = enumerate_optimal(problem)
                result = Solver(problem, budget=1_000_000).solve()
                if best is None:
                    self.assertEqual(result.status, STATUS_INFEASIBLE)
                    self.assertIsNone(result.objective)
                else:
                    self.assertEqual(result.status, STATUS_OPTIMAL)
                    self.assertEqual(result.objective, best)


if __name__ == "__main__":
    unittest.main()
