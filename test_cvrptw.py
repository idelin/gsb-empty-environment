import unittest
from fractions import Fraction

from cvrptw import (
    Instance,
    Status,
    solve_exact,
    solve_heuristic,
    verify_routes,
)


def tiny_asymmetric():
    # d(1,2)=1 but d(2,1)=10: direction matters, matrix is asymmetric.
    return Instance.from_lists(
        dist=[[0, 2, 5],
              [2, 0, 1],
              [5, 10, 0]],
        demand=[0, 4, 4],
        capacity=10,
        early=[0, 0, 0],
        late=[100, 100, 100],
        service=[0, 0, 0],
    )


def medium():
    return Instance.from_lists(
        dist=[[0, 2, Fraction(9, 2), 3, 7],
              [5, 0, 2, 8, 3],
              [2, 3, 0, 2, 9],
              [4, 9, 3, 0, 2],
              [3, 2, 8, 4, 0]],
        demand=[0, 3, 4, 2, 3],
        capacity=10,
        early=[0, 0, 3, 0, 5],
        late=[60, 20, 25, 30, 40],
        service=[0, 1, 2, 1, 2],
    )


class ExactSolverTests(unittest.TestCase):
    def test_optimal_hand_computed(self):
        res = solve_exact(tiny_asymmetric())
        self.assertIs(res.status, Status.OPTIMAL)
        self.assertEqual(res.objective, 8)  # 0->1->2->0 = 2+1+5
        self.assertEqual(res.routes, [[1, 2]])

    def test_asymmetry_not_assumed(self):
        # If d(a,b)=d(b,a) were (wrongly) assumed, [[2,1]] would look optimal.
        res = solve_exact(tiny_asymmetric())
        self.assertNotEqual(res.routes, [[2, 1]])
        self.assertLess(res.objective, 17)  # 0->2->1->0 would cost 5+10+2

    def test_each_customer_visited_once(self):
        inst = medium()
        res = solve_exact(inst)
        self.assertIs(res.status, Status.OPTIMAL)
        flat = [c for route in res.routes for c in route]
        self.assertEqual(sorted(flat), [1, 2, 3, 4])
        ok, _, total = verify_routes(inst, res.routes)
        self.assertTrue(ok)
        self.assertEqual(total, res.objective)

    def test_rational_objective_exact(self):
        inst = Instance.from_lists(
            dist=[[0, Fraction(3, 2)], [Fraction(5, 2), 0]],
            demand=[0, 1], capacity=5,
            early=[0, 0], late=[50, 50], service=[0, 0],
        )
        res = solve_exact(inst)
        self.assertIs(res.status, Status.OPTIMAL)
        self.assertIsInstance(res.objective, Fraction)
        self.assertEqual(res.objective, Fraction(4))

    def test_infeasible_capacity(self):
        inst = Instance.from_lists(
            dist=[[0, 1], [1, 0]],
            demand=[0, 11], capacity=10,
            early=[0, 0], late=[50, 50], service=[0, 0],
        )
        res = solve_exact(inst)
        self.assertIs(res.status, Status.INFEASIBLE)
        self.assertIsNone(res.objective)
        self.assertIsNone(res.routes)

    def test_infeasible_vehicle_limit(self):
        inst = Instance.from_lists(
            dist=[[0, 1, 1], [1, 0, 1], [1, 1, 0]],
            demand=[0, 6, 6], capacity=10,
            early=[0, 0, 0], late=[50, 50, 50], service=[0, 0, 0],
            max_vehicles=1,
        )
        self.assertIs(solve_exact(inst).status, Status.INFEASIBLE)

    def test_infeasible_time_window(self):
        inst = Instance.from_lists(
            dist=[[0, 3], [3, 0]],
            demand=[0, 1], capacity=10,
            early=[0, 0], late=[50, 2], service=[0, 0],
        )
        self.assertIs(solve_exact(inst).status, Status.INFEASIBLE)

    def test_budget_undecided(self):
        res = solve_exact(medium(), node_limit=1)
        self.assertIs(res.status, Status.BUDGET_UNDECIDED)
        self.assertIsNone(res.objective)
        self.assertIsNone(res.routes)

    def test_budget_partial_gives_feasible_not_optimal(self):
        inst = medium()
        full = solve_exact(inst)
        self.assertIs(full.status, Status.OPTIMAL)
        saw_feasible = False
        for limit in range(1, full.nodes):
            res = solve_exact(inst, node_limit=limit)
            self.assertIn(res.status, (Status.FEASIBLE, Status.BUDGET_UNDECIDED))
            if res.status is Status.FEASIBLE:
                saw_feasible = True
                self.assertIsNotNone(res.objective)
                self.assertGreaterEqual(res.objective, full.objective)
                ok, _, total = verify_routes(inst, res.routes)
                self.assertTrue(ok)
                self.assertEqual(total, res.objective)
        self.assertTrue(saw_feasible)


class HeuristicTests(unittest.TestCase):
    def test_heuristic_feasible(self):
        inst = medium()
        res = solve_heuristic(inst)
        self.assertIs(res.status, Status.FEASIBLE)
        self.assertIsNotNone(res.objective)
        ok, _, total = verify_routes(inst, res.routes)
        self.assertTrue(ok)
        self.assertEqual(total, res.objective)
        self.assertGreaterEqual(res.objective, solve_exact(inst).objective)

    def test_heuristic_not_found_differs_from_infeasible(self):
        inst = Instance.from_lists(
            dist=[[0, 3], [3, 0]],
            demand=[0, 1], capacity=10,
            early=[0, 0], late=[50, 2], service=[0, 0],
        )
        heur = solve_heuristic(inst)
        exact = solve_exact(inst)
        self.assertIs(heur.status, Status.HEURISTIC_NOT_FOUND)
        self.assertIs(exact.status, Status.INFEASIBLE)
        self.assertIsNot(heur.status, exact.status)


class VerifierTests(unittest.TestCase):
    def test_detects_duplicate_and_missing(self):
        inst = medium()
        ok, lines, _ = verify_routes(inst, [[1, 2, 1], [3]])
        self.assertFalse(ok)
        text = "\n".join(lines)
        self.assertIn("duplicate visit", text)
        self.assertIn("missing customers: [4]", text)

    def test_detects_overload_and_window_miss(self):
        inst = Instance.from_lists(
            dist=[[0, 1, 1], [1, 0, 1], [1, 1, 0]],
            demand=[0, 6, 6], capacity=10,
            early=[0, 0, 0], late=[50, 50, 1], service=[0, 0, 0],
        )
        ok, lines, _ = verify_routes(inst, [[1, 2]])
        self.assertFalse(ok)
        text = "\n".join(lines)
        self.assertIn("overload", text)
        self.assertIn("window miss", text)

    def test_valid_route_passes(self):
        inst = tiny_asymmetric()
        ok, lines, total = verify_routes(inst, [[1, 2]])
        self.assertTrue(ok)
        self.assertEqual(total, 8)


class InstanceValidationTests(unittest.TestCase):
    def test_rejects_bad_data(self):
        with self.assertRaises(ValueError):
            Instance.from_lists(dist=[[0, 1]], demand=[0, 1], capacity=1,
                                early=[0, 0], late=[1, 1], service=[0, 0])
        with self.assertRaises(ValueError):
            Instance.from_lists(dist=[[0, 1], [1, 0]], demand=[0, 1], capacity=1,
                                early=[0, 5], late=[1, 1], service=[0, 0])


if __name__ == "__main__":
    unittest.main(verbosity=2)
