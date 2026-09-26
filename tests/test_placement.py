import itertools
import unittest
from collections import defaultdict
from fractions import Fraction

from placement import (
    Problem,
    Status,
    apply_steps,
    build_migration_plan,
    placement_cost,
    solve,
    validate_placement,
)


def small_problem():
    """2 racks x 2 nodes, 2 objects; cheapest candidates conflict on capacity."""
    return Problem.build(
        nodes=[("n1", "r1", 4), ("n2", "r1", 4), ("n3", "r2", 4), ("n4", "r2", 4)],
        objects=[("A", 4, 2), ("B", 4, 2)],
        costs={
            ("A", "n1"): 1, ("A", "n2"): 1, ("A", "n3"): 1, ("A", "n4"): 1,
            ("B", "n1"): 1, ("B", "n2"): 5, ("B", "n3"): 1, ("B", "n4"): 5,
        },
    )


def brute_force_optimum(problem):
    """Reference optimum via exhaustive enumeration (test-only)."""
    per_object = []
    for obj in problem.objects:
        cands = []
        for combo in itertools.combinations(problem.nodes, obj.replicas):
            if len({n.rack for n in combo}) < obj.replicas:
                continue
            if any(n.capacity < obj.size for n in combo):
                continue
            cost = sum(problem.costs[(obj.obj_id, n.node_id)] for n in combo)
            cands.append((obj, combo, cost))
        per_object.append(cands)
    best = None
    for choice in itertools.product(*per_object):
        load = defaultdict(Fraction)
        placed = {}
        for obj, combo, _ in choice:
            placed[obj.obj_id] = {n.node_id for n in combo}
            for n in combo:
                load[n.node_id] += obj.size
        if any(load[n.node_id] > n.capacity for n in problem.nodes):
            continue
        if any(
            placed[a] & placed[b]
            for a in placed
            for b in problem.anti_affinity.get(a, ())
        ):
            continue
        total = sum(c for _, _, c in choice)
        if best is None or total < best:
            best = total
    return best


class TestValidation(unittest.TestCase):
    def setUp(self):
        self.problem = Problem.build(
            nodes=[("n1", "r1", 10), ("n2", "r1", 10), ("n3", "r2", 4)],
            objects=[("A", 4, 2), ("C", 3, 1)],
            costs={(o, n): 1 for o in ("A", "C") for n in ("n1", "n2", "n3")},
            anti_affinity=[("A", "C")],
        )

    def test_valid_placement_passes(self):
        self.assertEqual(validate_placement(self.problem, {"A": ("n1", "n3"), "C": ("n2",)}), [])

    def test_same_rack_nodes_do_not_satisfy_rack_isolation(self):
        errors = validate_placement(self.problem, {"A": ("n1", "n2"), "C": ("n3",)})
        self.assertTrue(any("rack isolation" in e for e in errors))

    def test_replica_counted_twice_on_same_node(self):
        errors = validate_placement(self.problem, {"A": ("n1", "n1"), "C": ("n3",)})
        self.assertTrue(any("twice" in e for e in errors))

    def test_capacity_violation_detected(self):
        errors = validate_placement(self.problem, {"A": ("n3", "n1"), "C": ("n3",)})
        self.assertTrue(any("exceeds capacity" in e for e in errors))

    def test_anti_affinity_violation_detected(self):
        errors = validate_placement(self.problem, {"A": ("n1", "n3"), "C": ("n1",)})
        self.assertTrue(any("anti-affinity" in e for e in errors))

    def test_missing_replicas_detected(self):
        errors = validate_placement(self.problem, {"A": ("n1",), "C": ("n3",)})
        self.assertTrue(any("minimum is 2" in e for e in errors))


class TestSolver(unittest.TestCase):
    def test_optimal_matches_brute_force(self):
        problem = small_problem()
        result = solve(problem)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.cost, brute_force_optimum(problem))
        self.assertEqual(validate_placement(problem, result.placement), [])

    def test_optimal_cost_value(self):
        # Optimum: A on (n2, n4) cost 2, B on (n1, n3) cost 2 -> total 4.
        result = solve(small_problem())
        self.assertEqual(result.cost, Fraction(4))

    def test_feasible_status_under_tiny_search_budget(self):
        result = solve(small_problem(), node_limit=4)
        self.assertEqual(result.status, Status.FEASIBLE)
        self.assertEqual(validate_placement(small_problem(), result.placement), [])
        self.assertGreaterEqual(result.cost, solve(small_problem()).cost)

    def test_budget_undecided_without_search_budget(self):
        result = solve(small_problem(), budget=3, node_limit=0)
        self.assertEqual(result.status, Status.BUDGET_UNDECIDED)
        self.assertIsNone(result.placement)

    def test_budget_exceeded_proven(self):
        result = solve(small_problem(), budget=3)
        self.assertEqual(result.status, Status.BUDGET_EXCEEDED)
        self.assertEqual(result.cost, Fraction(4))

    def test_budget_met_is_optimal(self):
        result = solve(small_problem(), budget=4)
        self.assertEqual(result.status, Status.OPTIMAL)

    def test_infeasible_rack_starvation_with_diagnostics(self):
        problem = Problem.build(
            nodes=[("m1", "r1", 8), ("m2", "r1", 8), ("m3", "r2", 8)],
            objects=[("E", 4, 3)],
            costs={("E", n): 1 for n in ("m1", "m2", "m3")},
        )
        result = solve(problem)
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertTrue(any("object E" in d and "distinct rack" in d for d in result.diagnostics))

    def test_infeasible_capacity_deficit_with_diagnostics(self):
        problem = Problem.build(
            nodes=[("m1", "r1", 4), ("m2", "r2", 4)],
            objects=[("X", 3, 1), ("Y", 3, 1), ("Z", 3, 1)],
            costs={(o, n): 1 for o in ("X", "Y", "Z") for n in ("m1", "m2")},
        )
        result = solve(problem)
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertTrue(any("deficit 1" in d for d in result.diagnostics))

    def test_infeasible_by_anti_affinity_proven_by_search(self):
        problem = Problem.build(
            nodes=[("m1", "r1", 8), ("m2", "r2", 8)],
            objects=[("X", 4, 2), ("Y", 4, 2)],
            costs={(o, n): 1 for o in ("X", "Y") for n in ("m1", "m2")},
            anti_affinity=[("X", "Y")],
        )
        result = solve(problem)
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertTrue(any("exhaustive search" in d for d in result.diagnostics))

    def test_rational_costs_exact(self):
        problem = Problem.build(
            nodes=[("a1", "r1", 5), ("b1", "r2", 5)],
            objects=[("X", 1, 2)],
            costs={("X", "a1"): "1/2", ("X", "b1"): "1/3"},
        )
        result = solve(problem)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.cost, Fraction(5, 6))
        self.assertIsInstance(result.cost, Fraction)

    def test_rational_sizes_and_capacities(self):
        problem = Problem.build(
            nodes=[("a1", "r1", "3/2"), ("b1", "r2", 2)],
            objects=[("X", "1/2", 2), ("Y", 1, 2)],
            costs={(o, n): 1 for o in ("X", "Y") for n in ("a1", "b1")},
        )
        result = solve(problem)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.cost, Fraction(4))

    def test_missing_cost_raises(self):
        problem = Problem.build(
            nodes=[("a1", "r1", 5)],
            objects=[("X", 1, 1)],
            costs={},
        )
        with self.assertRaises(ValueError):
            solve(problem)

    def test_no_double_counted_replica_in_solution(self):
        result = solve(small_problem())
        for oid, nids in result.placement.items():
            self.assertEqual(len(nids), len(set(nids)), oid)


class TestMigration(unittest.TestCase):
    def setUp(self):
        self.problem = Problem.build(
            nodes=[("n1", "r1", 10), ("n2", "r1", 10), ("n3", "r2", 10), ("n4", "r2", 10)],
            objects=[("A", 4, 2), ("B", 3, 2)],
            costs={(o, n): 1 for o in ("A", "B") for n in ("n1", "n2", "n3", "n4")},
        )
        self.current = {"A": ("n1", "n3"), "B": ("n1", "n3")}
        self.target = {"A": ("n2", "n4"), "B": ("n2", "n4")}

    def test_plan_reaches_target(self):
        plan = build_migration_plan(self.problem, self.current, self.target)
        final = apply_steps(self.current, plan)
        self.assertEqual(final, {k: tuple(sorted(v)) for k, v in self.target.items()})

    def test_minimum_replicas_hold_at_every_step(self):
        plan = build_migration_plan(self.problem, self.current, self.target)
        counts = {"A": 2, "B": 2}
        for step in plan:
            counts[step.obj_id] += 1 if step.action == "add" else -1
            self.assertGreaterEqual(counts[step.obj_id], 2, f"violated at {step}")

    def test_adds_precede_removes(self):
        plan = build_migration_plan(self.problem, self.current, self.target)
        actions = [s.action for s in plan]
        self.assertEqual(actions, sorted(actions, key=lambda a: a != "add"))

    def test_invalid_target_rejected(self):
        bad_target = {"A": ("n2",), "B": ("n2", "n4")}  # A below minimum
        with self.assertRaises(ValueError):
            build_migration_plan(self.problem, self.current, bad_target)

    def test_same_rack_target_rejected(self):
        bad_target = {"A": ("n1", "n2"), "B": ("n3", "n4")}  # A in one rack only
        with self.assertRaises(ValueError):
            build_migration_plan(self.problem, self.current, bad_target)


if __name__ == "__main__":
    unittest.main()
