import unittest
from fractions import Fraction

from cvrptw import (Customer, Problem, Status, solve_exact, solve_heuristic,
                    verify_routes)
from cvrptw.verify import route_distance_if_feasible


def loose(name, demand=1, service=0, due=1000):
    return Customer(name, demand=demand, ready=0, due=due, service=service)


class AsymmetryTests(unittest.TestCase):
    def test_forward_direction_cheaper(self):
        # d(0,1)=2, d(1,0)=5, d(0,2)=3, d(2,0)=1, d(1,2)=1, d(2,1)=4
        # 0->1->2->0 = 2+1+1 = 4  vs  0->2->1->0 = 3+4+5 = 12
        p = Problem(capacity=10, n_vehicles=1,
                    customers=(loose("P"), loose("Q")),
                    distances=[[0, 2, 3], [5, 0, 1], [1, 4, 0]], horizon=1000)
        res = solve_exact(p)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.objective, 4)
        self.assertEqual(res.routes, ((0, 1),))

    def test_reverse_direction_cheaper(self):
        # d(1,2)=10 but d(2,1)=1: optimal order is Q then P.
        p = Problem(capacity=10, n_vehicles=1,
                    customers=(loose("P"), loose("Q")),
                    distances=[[0, 1, 1], [1, 0, 10], [1, 1, 0]], horizon=1000)
        res = solve_exact(p)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.objective, 3)
        self.assertEqual(res.routes, ((1, 0),))

    def test_no_symmetry_assumption(self):
        # A symmetric reading of the matrix above would give 2*1+2*1 = 4 for
        # either order; the asymmetric optimum is 3, proving d(a,b)=d(b,a)
        # is never assumed.
        p = Problem(capacity=10, n_vehicles=1,
                    customers=(loose("P"), loose("Q")),
                    distances=[[0, 1, 1], [1, 0, 10], [1, 1, 0]], horizon=1000)
        self.assertNotEqual(solve_exact(p).objective, 4)


class TimeWindowTests(unittest.TestCase):
    def test_waiting_is_accounted(self):
        cust = Customer("R", demand=1, ready=10, due=20, service=1)
        p = Problem(capacity=10, n_vehicles=1, customers=(cust,),
                    distances=[[0, 2], [2, 0]], horizon=100)
        res = solve_exact(p)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.objective, 4)
        ver = verify_routes(p, res.routes)
        self.assertTrue(ver.ok)
        leg = ver.routes[0].legs[0]
        self.assertEqual(leg.arrive, 2)
        self.assertEqual(leg.wait, 8)
        self.assertEqual(leg.service_start, 10)

    def test_unreachable_time_window_is_infeasible(self):
        cust = Customer("R", demand=1, ready=0, due=1, service=0)
        p = Problem(capacity=10, n_vehicles=1, customers=(cust,),
                    distances=[[0, 5], [5, 0]], horizon=100)
        res = solve_exact(p)
        self.assertEqual(res.status, Status.INFEASIBLE)
        self.assertIsNone(res.objective)

    def test_order_forced_by_due_times(self):
        # P due early, Q due late; both reachable only as 0->P->Q->0.
        p = Problem(capacity=10, n_vehicles=1,
                    customers=(Customer("P", 1, 0, 3, 0), Customer("Q", 1, 0, 100, 0)),
                    distances=[[0, 1, 1], [1, 0, 1], [1, 1, 0]], horizon=100)
        res = solve_exact(p)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.routes, ((0, 1),))


class CapacityTests(unittest.TestCase):
    def test_capacity_forces_second_vehicle(self):
        p = Problem(capacity=10, n_vehicles=2,
                    customers=(loose("P", demand=6), loose("Q", demand=6)),
                    distances=[[0, 1, 3], [2, 0, 1], [4, 1, 0]], horizon=1000)
        res = solve_exact(p)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(len(res.routes), 2)
        self.assertEqual(res.objective, 1 + 2 + 3 + 4)

    def test_demand_above_capacity_infeasible(self):
        p = Problem(capacity=8, n_vehicles=3,
                    customers=(loose("X", demand=9),),
                    distances=[[0, 1], [1, 0]], horizon=10)
        res = solve_exact(p)
        self.assertEqual(res.status, Status.INFEASIBLE)

    def test_fleet_capacity_infeasible(self):
        p = Problem(capacity=10, n_vehicles=1,
                    customers=(loose("P", 6), loose("Q", 6), loose("R", 6)),
                    distances=[[0, 1, 1, 1], [1, 0, 1, 1], [1, 1, 0, 1], [1, 1, 1, 0]],
                    horizon=1000)
        res = solve_exact(p)
        self.assertEqual(res.status, Status.INFEASIBLE)


class StatusDistinctionTests(unittest.TestCase):
    def test_heuristic_not_found_is_not_infeasible(self):
        # Heuristic and exact agree there is no solution, but the heuristic
        # must say NOT_FOUND while only the exact proof says INFEASIBLE.
        p = Problem(capacity=10, n_vehicles=1,
                    customers=(loose("P", 6), loose("Q", 6)),
                    distances=[[0, 1, 1], [1, 0, 1], [1, 1, 0]], horizon=1000)
        h = solve_heuristic(p)
        e = solve_exact(p)
        self.assertEqual(h.status, Status.NOT_FOUND)
        self.assertEqual(e.status, Status.INFEASIBLE)
        self.assertNotEqual(h.status, e.status)

    def test_budget_exhausted_is_undecided(self):
        customers = tuple(loose(f"C{i}") for i in range(6))
        dist = [[0 if i == j else abs(i - j) + 1 for j in range(7)] for i in range(7)]
        p = Problem(capacity=10, n_vehicles=2, customers=customers,
                    distances=dist, horizon=10_000)
        res = solve_exact(p, node_budget=1)
        self.assertEqual(res.status, Status.BUDGET_EXHAUSTED)
        self.assertFalse(res.proven_optimal)

    def test_budget_exhausted_may_carry_feasible_incumbent(self):
        customers = tuple(loose(f"C{i}") for i in range(6))
        dist = [[0 if i == j else abs(i - j) + 1 for j in range(7)] for i in range(7)]
        p = Problem(capacity=10, n_vehicles=2, customers=customers,
                    distances=dist, horizon=10_000)
        full = solve_exact(p, node_budget=100_000)
        limited = solve_exact(p, node_budget=40)
        self.assertEqual(full.status, Status.OPTIMAL)
        self.assertEqual(limited.status, Status.BUDGET_EXHAUSTED)
        if limited.routes:
            # The incumbent's objective is its actual distance, not a bound,
            # and it must verify cleanly.
            ver = verify_routes(p, limited.routes)
            self.assertTrue(ver.ok)
            self.assertEqual(ver.total_distance, limited.objective)
            self.assertGreaterEqual(limited.objective, full.objective)

    def test_heuristic_feasible_status(self):
        p = Problem(capacity=10, n_vehicles=1,
                    customers=(loose("P"), loose("Q")),
                    distances=[[0, 2, 3], [5, 0, 1], [1, 4, 0]], horizon=1000)
        h = solve_heuristic(p)
        self.assertEqual(h.status, Status.FEASIBLE)
        self.assertFalse(h.proven_optimal)
        ver = verify_routes(p, h.routes)
        self.assertTrue(ver.ok)
        self.assertEqual(ver.total_distance, h.objective)


class RationalDataTests(unittest.TestCase):
    def test_exact_rational_objective(self):
        p = Problem(capacity=Fraction(5, 2), n_vehicles=1,
                    customers=(Customer("P", demand=Fraction(3, 2), ready=0,
                                        due=10, service=Fraction(1, 2)),),
                    distances=[[0, Fraction(3, 2)], [Fraction(1, 2), 0]],
                    horizon=Fraction(25, 2))
        res = solve_exact(p)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertIsInstance(res.objective, Fraction)
        self.assertEqual(res.objective, Fraction(2, 1))

    def test_tuple_rationals_accepted(self):
        p = Problem(capacity=(5, 1), n_vehicles=1,
                    customers=(Customer("P", (1, 2), 0, 10, (1, 4)),),
                    distances=[[0, (3, 2)], [(1, 2), 0]], horizon=10)
        res = solve_exact(p)
        self.assertEqual(res.objective, 2)

    def test_floats_rejected(self):
        with self.assertRaises(TypeError):
            Customer("P", demand=1.5, ready=0, due=1, service=0)


class VerificationTests(unittest.TestCase):
    def setUp(self):
        self.problem = Problem(
            capacity=10, n_vehicles=2,
            customers=(loose("P", 4), loose("Q", 4)),
            distances=[[0, 1, 2], [1, 0, 1], [2, 1, 0]], horizon=1000)

    def test_clean_solution_verifies(self):
        ver = verify_routes(self.problem, [(0, 1)])
        self.assertTrue(ver.ok)
        self.assertEqual(ver.total_distance, 1 + 1 + 2)

    def test_duplicate_visit_flagged(self):
        ver = verify_routes(self.problem, [(0, 1, 0)])
        self.assertFalse(ver.ok)
        self.assertTrue(any("visited" in v for v in ver.violations))

    def test_missing_customer_flagged(self):
        ver = verify_routes(self.problem, [(0,)])
        self.assertFalse(ver.ok)
        self.assertTrue(any("never visited" in v for v in ver.violations))

    def test_capacity_violation_flagged(self):
        p = Problem(capacity=5, n_vehicles=1,
                    customers=(loose("P", 4), loose("Q", 4)),
                    distances=[[0, 1, 2], [1, 0, 1], [2, 1, 0]], horizon=1000)
        ver = verify_routes(p, [(0, 1)])
        self.assertFalse(ver.ok)
        self.assertTrue(any("capacity" in v for v in ver.violations))

    def test_time_window_violation_flagged(self):
        p = Problem(capacity=10, n_vehicles=1,
                    customers=(Customer("P", 1, 0, 1, 0),),
                    distances=[[0, 5], [5, 0]], horizon=1000)
        ver = verify_routes(p, [(0,)])
        self.assertFalse(ver.ok)
        self.assertTrue(any("due" in v for v in ver.violations))

    def test_route_distance_helper(self):
        self.assertEqual(route_distance_if_feasible(self.problem, [0, 1]), 4)
        tight = Problem(capacity=5, n_vehicles=1,
                        customers=(loose("P", 4), loose("Q", 4)),
                        distances=[[0, 1, 2], [1, 0, 1], [2, 1, 0]], horizon=1000)
        self.assertIsNone(route_distance_if_feasible(tight, [0, 1]))


if __name__ == "__main__":
    unittest.main()
