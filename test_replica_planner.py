"""replica_planner 的 unittest 测试套件。运行: python3.11 -m unittest -v"""
import random
import unittest
from fractions import Fraction
from itertools import combinations

from replica_planner import (
    Node,
    ObjectSpec,
    Status,
    build_diagnostics,
    build_migration_plan,
    simulate_plan,
    solve,
    validate_placement,
)


def brute_force_cost(nodes, spec):
    """暴力枚举全部合法放置，返回最优成本（不可行返回 None）。用于交叉验证。"""
    selectable = [n for n in nodes if n.capacity >= 1]
    current = set(spec.current)
    best = None
    for combo in combinations(selectable, spec.replicas):
        if len({n.rack for n in combo}) < spec.replicas:
            continue
        cost = sum(
            Fraction(0) if n.node_id in current else n.transfer_cost for n in combo
        )
        if best is None or cost < best:
            best = cost
    return best


def assert_placement_valid(testcase, nodes, placement, replicas):
    nodes_by_id = {n.node_id: n for n in nodes}
    testcase.assertEqual(len(placement), replicas)
    testcase.assertEqual(len(set(placement)), replicas, "副本不能重复计在同节点")
    racks = [nodes_by_id[nid].rack for nid in placement]
    testcase.assertEqual(len(set(racks)), replicas, "同机架不同节点不满足机架隔离")
    for nid in placement:
        testcase.assertGreaterEqual(nodes_by_id[nid].capacity, 1)


class ModelTest(unittest.TestCase):
    def test_validate_placement_rejects_same_rack(self):
        nodes = {n.node_id: n for n in [
            Node("a1", "rackA", 1, 1),
            Node("a2", "rackA", 1, 1),
        ]}
        errors = validate_placement(nodes, ("a1", "a2"))
        self.assertTrue(any("机架隔离" in e for e in errors))

    def test_validate_placement_rejects_duplicate_node(self):
        nodes = {"a1": Node("a1", "rackA", 2, 1)}
        errors = validate_placement(nodes, ("a1", "a1"))
        self.assertTrue(any("重复" in e for e in errors))

    def test_rational_cost_parsing(self):
        node = Node("n1", "rackA", 1, "3/2")
        self.assertEqual(node.transfer_cost, Fraction(3, 2))
        with self.assertRaises(ValueError):
            Node("n2", "rackA", 1, -1)
        with self.assertRaises(ValueError):
            Node("n3", "rackA", -1, 1)


class SolverTest(unittest.TestCase):
    def setUp(self):
        self.nodes = [
            Node("a1", "rackA", 1, 5),
            Node("a2", "rackA", 1, 7),
            Node("b1", "rackB", 1, Fraction(3, 2)),
            Node("b2", "rackB", 0, 2),
            Node("c1", "rackC", 1, 4),
            Node("c2", "rackC", 1, Fraction(9, 2)),
            Node("d1", "rackD", 0, 6),
        ]

    def test_optimal_with_decommissioned_node(self):
        spec = ObjectSpec("obj", 3, 2, current=("a1", "b1", "d1"))
        result = solve(self.nodes, spec, budget=Fraction(9, 2))
        self.assertIs(result.status, Status.OPTIMAL)
        self.assertEqual(result.cost, 4)
        self.assertEqual(set(result.placement), {"a1", "b1", "c1"})
        self.assertTrue(result.within_budget)
        self.assertEqual(result.lower_bound, result.cost)
        assert_placement_valid(self, self.nodes, result.placement, 3)

    def test_over_budget_optimal(self):
        spec = ObjectSpec("obj", 3, 2, current=("a1", "b1", "d1"))
        result = solve(self.nodes, spec, budget=Fraction(7, 2))
        self.assertIs(result.status, Status.OPTIMAL)
        self.assertFalse(result.within_budget)

    def test_keeps_existing_replicas_at_zero_cost(self):
        spec = ObjectSpec("obj", 2, 1, current=("a2", "c2"))
        result = solve(self.nodes, spec)
        self.assertIs(result.status, Status.OPTIMAL)
        self.assertEqual(result.cost, 0)
        self.assertEqual(set(result.placement), {"a2", "c2"})

    def test_rack_isolation_forces_distinct_racks(self):
        nodes = [
            Node("a1", "rackA", 1, 1),
            Node("a2", "rackA", 1, 1),
            Node("b1", "rackB", 1, 100),
        ]
        spec = ObjectSpec("obj", 2, 1)
        result = solve(nodes, spec)
        self.assertIs(result.status, Status.OPTIMAL)
        self.assertEqual(result.cost, 101)
        assert_placement_valid(self, nodes, result.placement, 2)

    def test_full_nodes_are_not_selected(self):
        spec = ObjectSpec("obj", 2, 1)
        result = solve(self.nodes, spec)
        self.assertNotIn("b2", result.placement)
        self.assertNotIn("d1", result.placement)

    def test_infeasible_with_diagnostics(self):
        spec = ObjectSpec("obj", 4, 3)
        result = solve(self.nodes, spec)
        self.assertIs(result.status, Status.INFEASIBLE)
        self.assertIsNone(result.placement)
        diag = result.diagnostics
        self.assertIsNotNone(diag)
        self.assertEqual(diag.deficit, 1)
        self.assertEqual(diag.racks_with_capacity, 3)
        text = diag.render()
        self.assertIn("缺口", text)
        self.assertIn("rackD", text)

    def test_budget_undecided_when_search_budget_exhausted(self):
        nodes = [Node("x1", "rackX", 1, 4), Node("y1", "rackY", 1, 5), Node("z1", "rackZ", 1, 1)]
        spec = ObjectSpec("obj", 2, 2)
        result = solve(nodes, spec, budget=3, max_expansions=1)
        self.assertIs(result.status, Status.BUDGET_UNDECIDED)
        self.assertIsNone(result.placement)
        self.assertIsNone(result.within_budget)

    def test_feasible_unproven_with_larger_search_budget(self):
        nodes = [Node("x1", "rackX", 1, 4), Node("y1", "rackY", 1, 5), Node("z1", "rackZ", 1, 1)]
        spec = ObjectSpec("obj", 2, 2)
        result = solve(nodes, spec, max_expansions=2)
        self.assertIs(result.status, Status.FEASIBLE)
        self.assertIsNotNone(result.placement)
        assert_placement_valid(self, nodes, result.placement, 2)
        optimal = solve(nodes, spec)
        self.assertIs(optimal.status, Status.OPTIMAL)
        self.assertLessEqual(optimal.cost, result.cost)

    def test_invalid_current_placement_rejected(self):
        spec = ObjectSpec("obj", 2, 1, current=("a1", "a2"))
        with self.assertRaises(ValueError):
            solve(self.nodes, spec)
        spec2 = ObjectSpec("obj", 2, 1, current=("a1", "a1"))
        with self.assertRaises(ValueError):
            solve(self.nodes, spec2)

    def test_rational_cost_exactness(self):
        nodes = [
            Node("a1", "rackA", 1, Fraction(1, 3)),
            Node("b1", "rackB", 1, Fraction(1, 6)),
        ]
        spec = ObjectSpec("obj", 2, 1)
        result = solve(nodes, spec)
        self.assertEqual(result.cost, Fraction(1, 2))


class MigrationTest(unittest.TestCase):
    def test_plan_maintains_min_replicas(self):
        current = ("a1", "b1", "d1")
        target = ("a1", "b1", "c1")
        plan = build_migration_plan(current, target, min_replicas=2)
        self.assertEqual([op.action for op in plan], ["add", "remove"])
        counts = simulate_plan(plan, len(current))
        self.assertTrue(all(c >= 2 for c in counts))
        self.assertEqual(counts[-1], len(target))

    def test_plan_adds_before_removes(self):
        plan = build_migration_plan(("a", "b"), ("c", "d"), min_replicas=2)
        self.assertEqual([op.action for op in plan], ["add", "add", "remove", "remove"])
        counts = simulate_plan(plan, 2)
        self.assertEqual(counts, [3, 4, 3, 2])

    def test_plan_rejects_degraded_current(self):
        with self.assertRaises(ValueError):
            build_migration_plan(("a",), ("b", "c"), min_replicas=2)

    def test_plan_rejects_duplicate_nodes(self):
        with self.assertRaises(ValueError):
            build_migration_plan(("a", "a"), ("b", "c"), min_replicas=1)


class DiagnosticsTest(unittest.TestCase):
    def test_rack_reports(self):
        nodes = [
            Node("a1", "rackA", 1, 1),
            Node("b1", "rackB", 0, 1),
            Node("b2", "rackB", 2, 1),
        ]
        spec = ObjectSpec("obj", 3, 2)
        diag = build_diagnostics(tuple(nodes), spec)
        self.assertEqual(diag.racks_total, 2)
        self.assertEqual(diag.racks_with_capacity, 2)
        self.assertEqual(diag.deficit, 1)
        reports = {r.rack: r for r in diag.rack_reports}
        self.assertEqual(reports["rackB"].available_nodes, ("b2",))
        self.assertEqual(reports["rackB"].exhausted_nodes, ("b1",))
        self.assertEqual(reports["rackB"].total_capacity, 2)


class BruteForceCrossCheck(unittest.TestCase):
    def test_random_instances_match_brute_force(self):
        rng = random.Random(20260926)
        for _ in range(200):
            rack_count = rng.randint(1, 4)
            nodes = []
            for rack_idx in range(rack_count):
                for node_idx in range(rng.randint(1, 3)):
                    nodes.append(Node(
                        f"r{rack_idx}n{node_idx}",
                        f"rack{rack_idx}",
                        rng.randint(0, 2),
                        Fraction(rng.randint(0, 9), rng.randint(1, 4)),
                    ))
            replicas = rng.randint(1, 3)
            candidates = [n for n in nodes if n.capacity >= 1]
            rng.shuffle(candidates)
            current, used_racks = [], set()
            for node in candidates:
                if node.rack not in used_racks and len(current) < replicas:
                    current.append(node.node_id)
                    used_racks.add(node.rack)
            spec = ObjectSpec("obj", replicas, rng.randint(0, replicas), current=tuple(current))
            result = solve(nodes, spec)
            expected = brute_force_cost(nodes, spec)
            if expected is None:
                self.assertIs(result.status, Status.INFEASIBLE)
            else:
                self.assertIs(result.status, Status.OPTIMAL)
                self.assertEqual(result.cost, expected)
                assert_placement_valid(self, nodes, result.placement, replicas)


if __name__ == "__main__":
    unittest.main()
