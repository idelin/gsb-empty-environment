import copy
import json
import unittest
from pathlib import Path

from licsel.incremental import repair
from licsel.model import load_problem
from licsel.solver import STATUS_INFEASIBLE, STATUS_OPTIMAL, Solver

SAMPLES = Path(__file__).resolve().parent.parent / "samples"


def solve_choices(sample):
    problem = load_problem(str(SAMPLES / sample))
    result = Solver(problem).solve()
    assert result.status == STATUS_OPTIMAL
    return result.choices_as_json()


class TestRepairAfterVersionChange(unittest.TestCase):
    """repair_v2 adds libA 2.0.0 to the local index; the version constraint
    >=1.0.0 <3.0.0 now resolves libA to 2.0.0 (new license, new deps)."""

    @classmethod
    def setUpClass(cls):
        cls.previous = solve_choices("repair_v1.json")
        cls.problem_v2 = load_problem(str(SAMPLES / "repair_v2.json"))

    def test_resolution_picks_new_version(self):
        self.assertIn("libA@2.0.0", self.problem_v2.graph.nodes)
        self.assertNotIn("libA@1.0.0", self.problem_v2.graph.nodes)

    def test_repair_keeps_unaffected_choices(self):
        result = repair(self.problem_v2, self.previous)
        self.assertEqual(result.status, STATUS_OPTIMAL)
        self.assertEqual(result.mode, "pinned")
        self.assertIn("app@1.0.0", result.kept)  # untouched package kept as-is
        self.assertIn("libA@1.0.0", result.dropped)  # old version gone
        self.assertIn("libB@1.0.0", result.dropped)  # no longer reachable
        # libA 2.0.0 must take the LGPL-2.1 branch: GPL-3.0 would force app
        # (MIT) to be copyleft under the synthetic static-dependents rule.
        self.assertEqual(
            result.solve.choices_as_json()["libA@2.0.0"],
            [{"license": "LGPL-2.1", "exception": None}],
        )
        self.assertEqual(result.solve.objective, (0, 1))

    def test_repair_result_is_globally_optimal(self):
        # incremental repair must not be worse than a cold re-solve
        cold = Solver(self.problem_v2).solve()
        warm = repair(self.problem_v2, self.previous)
        self.assertEqual(warm.solve.objective, cold.objective)


class TestRepairInfeasibleGivesCore(unittest.TestCase):
    def test_core_reported_when_no_branch_works(self):
        raw = json.loads((SAMPLES / "repair_v2.json").read_text())
        # make libA 2.0.0 GPL-3.0-only: app (MIT) can never satisfy the rule
        for pkg in raw["packages"]:
            if pkg["name"] == "libA" and pkg["version"] == "2.0.0":
                pkg["license"] = "GPL-3.0"
        problem = load_problem(raw)
        result = repair(problem, solve_choices("repair_v1.json"))
        self.assertEqual(result.status, STATUS_INFEASIBLE)
        self.assertEqual(result.mode, "resolved")
        self.assertIsNotNone(result.core)
        triggers = {inst.trigger for inst in result.core.instances}
        self.assertIn("libA@2.0.0", triggers)


class TestRepairWithPinOverride(unittest.TestCase):
    def test_pin_forces_version(self):
        raw = json.loads((SAMPLES / "repair_v2.json").read_text())
        pinned = copy.deepcopy(raw)
        pinned["packages"] = [
            p for p in pinned["packages"]
            if not (p["name"] == "libA" and p["version"] != "1.0.0")
        ]
        problem = load_problem(pinned)
        self.assertIn("libA@1.0.0", problem.graph.nodes)
        result = repair(problem, self_previous())
        self.assertEqual(result.status, STATUS_OPTIMAL)
        self.assertEqual(result.solve.objective, (0, 0))


def self_previous():
    return solve_choices("repair_v1.json")


if __name__ == "__main__":
    unittest.main()
