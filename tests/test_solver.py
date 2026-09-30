import unittest

from muskit.checker import check_assignment, is_satisfying
from muskit.constraints import Constraint, Problem, Variable
from muskit.solver import SAT, UNKNOWN, UNSAT, BacktrackingSolver


def make_problem():
    variables = [Variable("x", (0, 1)), Variable("y", (0, 1, 2))]
    constraints = [
        Constraint("a", ("x",), lambda x: x == 0),
        Constraint("b", ("x",), lambda x: x == 1),
        Constraint("c", ("y",), lambda y: y != 0),
        Constraint("d", ("y",), lambda y: y != 1),
        Constraint("e", ("y",), lambda y: y != 2),
    ]
    return Problem(variables, constraints)


class SolverTest(unittest.TestCase):
    def setUp(self):
        self.problem = make_problem()
        self.solver = BacktrackingSolver(self.problem)

    def test_sat_with_valid_witness(self):
        result = self.solver.solve(["a", "c"])
        self.assertEqual(result.status, SAT)
        self.assertEqual(check_assignment(self.problem, ["a", "c"], result.assignment), [])

    def test_unsat(self):
        self.assertEqual(self.solver.solve(["a", "b"]).status, UNSAT)
        self.assertEqual(self.solver.solve(["c", "d", "e"]).status, UNSAT)

    def test_empty_assumption_set_is_sat(self):
        result = self.solver.solve([])
        self.assertEqual(result.status, SAT)
        self.assertEqual(result.assignment, {})

    def test_zero_budget_is_unknown_not_decisive(self):
        result = self.solver.solve(["a", "b"], budget=0)
        self.assertEqual(result.status, UNKNOWN)
        self.assertFalse(result.decisive)

    def test_budget_boundary(self):
        full = self.solver.solve(["a", "b", "c", "d", "e"])
        self.assertEqual(full.status, UNSAT)
        exact = self.solver.solve(["a", "b", "c", "d", "e"], budget=full.nodes)
        self.assertEqual(exact.status, UNSAT)
        short = self.solver.solve(["a", "b", "c", "d", "e"], budget=full.nodes - 1)
        self.assertEqual(short.status, UNKNOWN)


class CheckerTest(unittest.TestCase):
    def setUp(self):
        self.problem = make_problem()

    def test_accepts_good_assignment(self):
        self.assertTrue(is_satisfying(self.problem, ["a", "c"], {"x": 0, "y": 1}))

    def test_rejects_violating_assignment(self):
        self.assertEqual(check_assignment(self.problem, ["a", "b"], {"x": 0, "y": 0}), ["b"])

    def test_rejects_out_of_domain_value(self):
        self.assertEqual(check_assignment(self.problem, ["a"], {"x": 7}), ["a"])

    def test_rejects_missing_variable(self):
        self.assertEqual(check_assignment(self.problem, ["a"], {"y": 0}), ["a"])


if __name__ == "__main__":
    unittest.main()
