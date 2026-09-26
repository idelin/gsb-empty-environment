import unittest
from fractions import Fraction

from allocator import Node, Status, allocate, verify


def leaf(node_id, demand, weight):
    return Node.leaf(node_id, demand, weight)


class SplitTests(unittest.TestCase):
    def test_proportional_with_remainder(self):
        root = Node.branch("root", [
            leaf("a", 100, 1), leaf("b", 100, 1), leaf("c", 100, 1),
        ], upper=10)
        res = allocate(root)
        self.assertEqual(res.status, Status.FEASIBLE)
        self.assertEqual(res.total, 10)
        # 10/3 -> 3 each with one leftover unit; equal remainders go to
        # the smallest stable id.
        self.assertEqual(res.allocations, {"a": 4, "b": 3, "c": 3})

    def test_tie_break_by_stable_id(self):
        root = Node.branch("root", [
            leaf("z", 10, 1), leaf("y", 10, 1),
        ], upper=3)
        res = allocate(root)
        self.assertEqual(res.allocations, {"y": 2, "z": 1})

    def test_largest_remainder_order(self):
        root = Node.branch("root", [
            leaf("a", 9, 1), leaf("b", 9, 1), leaf("c", 9, 1),
        ], upper=8)
        res = allocate(root)
        self.assertEqual(res.allocations, {"a": 3, "b": 3, "c": 2})

    def test_weighted_split(self):
        root = Node.branch("root", [
            leaf("a", 100, 1), leaf("b", 100, 3),
        ], upper=10)
        res = allocate(root)
        # ideals 2.5 / 7.5, leftover unit breaks the .5/.5 tie by id.
        self.assertEqual(res.allocations, {"a": 3, "b": 7})

    def test_demand_cap_redistribution(self):
        root = Node.branch("root", [
            leaf("a", 2, 1), leaf("b", 100, 1),
        ], upper=10)
        res = allocate(root)
        self.assertEqual(res.allocations, {"a": 2, "b": 8})

    def test_zero_weight_fallback_equal_split(self):
        root = Node.branch("root", [
            leaf("a", 5, 0), leaf("b", 5, 0),
        ], upper=3)
        res = allocate(root)
        self.assertEqual(res.allocations, {"a": 2, "b": 1})

    def test_zero_weight_fills_after_positive_capped(self):
        root = Node.branch("root", [
            leaf("a", 2, 1), leaf("b", 100, 0),
        ], upper=10)
        res = allocate(root)
        self.assertEqual(res.allocations, {"a": 2, "b": 8})


class RationalDataTests(unittest.TestCase):
    def test_rational_upper_rounded_down(self):
        root = Node.branch("root", [
            leaf("a", 10, 1), leaf("b", 10, 1),
        ], upper=Fraction(7, 2))
        res = allocate(root)
        self.assertEqual(res.totals["root"], 3)
        self.assertEqual(res.allocations, {"a": 2, "b": 1})

    def test_string_rational_bound(self):
        root = Node.branch("root", [
            leaf("a", 10, 1), leaf("b", 10, 1),
        ], upper="9/2")
        res = allocate(root)
        self.assertEqual(res.totals["root"], 4)

    def test_rational_demand_floored(self):
        root = Node.branch("root", [
            leaf("a", Fraction(5, 2), 1),
        ], upper=10)
        res = allocate(root)
        self.assertEqual(res.allocations["a"], 2)

    def test_rational_lower_rounded_up(self):
        root = Node.branch("root", [
            Node.branch("p", [leaf("a", 10, 1)], lower=Fraction(5, 2)),
            leaf("b", 10, 1),
        ], upper=10)
        res = allocate(root)
        self.assertEqual(verify(root, res), [])
        self.assertGreaterEqual(res.totals["p"], 3)

    def test_float_rejected(self):
        with self.assertRaises(TypeError):
            leaf("a", 1.5, 1)


class StatusTests(unittest.TestCase):
    def test_optimal_when_budget_covers_demand(self):
        root = Node.branch("root", [
            leaf("a", 4, 1), leaf("b", 6, 1),
        ], upper=100)
        res = allocate(root)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.allocations, {"a": 4, "b": 6})

    def test_feasible_when_budget_binds(self):
        root = Node.branch("root", [
            leaf("a", 4, 1), leaf("b", 6, 1),
        ], upper=7)
        res = allocate(root)
        self.assertEqual(res.status, Status.FEASIBLE)
        self.assertEqual(res.total, 7)

    def test_budget_undecided_without_root_bounds(self):
        root = Node.branch("root", [
            leaf("a", 4, 1), leaf("b", 6, 1),
        ])
        res = allocate(root)
        self.assertEqual(res.status, Status.BUDGET_UNDECIDED)
        self.assertEqual(res.allocations, {"a": 4, "b": 6})

    def test_statuses_are_distinct(self):
        self.assertEqual(
            len({Status.OPTIMAL, Status.FEASIBLE, Status.BUDGET_UNDECIDED,
                 Status.INFEASIBLE}), 4)


class ConflictTests(unittest.TestCase):
    def test_minimal_conflicting_subtree(self):
        root = Node.branch("root", [
            Node.branch("bad", [
                leaf("x", 3, 1), leaf("y", 3, 1),
            ], lower=10),
            Node.branch("ok", [leaf("z", 5, 1)]),
        ], upper=100)
        res = allocate(root)
        self.assertEqual(res.status, Status.INFEASIBLE)
        self.assertEqual(res.allocations, {})
        conflict = res.conflict
        self.assertEqual(conflict.node_id, "bad")
        self.assertEqual(conflict.effective_lower, 10)
        self.assertEqual(conflict.effective_upper, 6)
        # The returned subtree is exactly the conflicting one.
        self.assertEqual(conflict.subtree["id"], "bad")
        self.assertEqual(
            [c["id"] for c in conflict.subtree["children"]], ["x", "y"])

    def test_self_conflict_lower_above_upper(self):
        root = Node.branch("root", [leaf("a", 100, 1)], lower=9, upper=4)
        res = allocate(root)
        self.assertEqual(res.status, Status.INFEASIBLE)
        self.assertEqual(res.conflict.node_id, "root")
        self.assertEqual(res.conflict.effective_lower, 9)
        self.assertEqual(res.conflict.effective_upper, 4)

    def test_children_lowers_exceed_parent_upper(self):
        root = Node.branch("root", [
            Node.branch("p", [leaf("a", 5, 1)], lower=4),
            Node.branch("q", [leaf("b", 5, 1)], lower=4),
        ], upper=5)
        res = allocate(root)
        self.assertEqual(res.status, Status.INFEASIBLE)
        self.assertEqual(res.conflict.node_id, "root")
        self.assertEqual(res.conflict.effective_lower, 8)
        self.assertEqual(res.conflict.effective_upper, 5)

    def test_deepest_conflict_reported_first(self):
        # The conflict inside "inner" is smaller than the one it would
        # cause at "outer", so "inner" must be reported.
        root = Node.branch("root", [
            Node.branch("outer", [
                Node.branch("inner", [leaf("a", 2, 1)], lower=5),
            ]),
        ], upper=100)
        res = allocate(root)
        self.assertEqual(res.status, Status.INFEASIBLE)
        self.assertEqual(res.conflict.node_id, "inner")


class ConservationTests(unittest.TestCase):
    def build(self, budget):
        return Node.branch("root", [
            Node.branch("d1", [
                leaf("a", 7, 1), leaf("b", 5, 3),
            ], upper=9),
            Node.branch("d2", [
                leaf("c", 6, 2), leaf("d", 4, 1),
            ], lower=1),
        ], upper=budget)

    def test_multi_level_conservation(self):
        root = Node.branch("root", [
            Node.branch("left", [
                leaf("l1", 10, 1), leaf("l2", 10, 2),
            ], upper=9),
            Node.branch("right", [
                leaf("r1", 10, 1), leaf("r2", 10, 1),
            ], lower=2),
        ], upper=20)
        res = allocate(root)
        self.assertEqual(verify(root, res), [])
        self.assertEqual(res.totals["root"], 20)
        self.assertEqual(res.totals["left"] + res.totals["right"], 20)
        self.assertLessEqual(res.totals["left"], 9)
        self.assertGreaterEqual(res.totals["right"], 2)
        self.assertEqual(res.allocations["l1"] + res.allocations["l2"],
                         res.totals["left"])

    def test_budget_sweep(self):
        # d1 caps at 9 (demand 12), d2 at 10, so the root can never
        # receive more than 19 regardless of the budget.
        # budget 0 is infeasible: d2 enforces a lower bound of 1.
        res = allocate(self.build(0))
        self.assertEqual(res.status, Status.INFEASIBLE)
        self.assertEqual(res.conflict.node_id, "root")
        for budget in range(1, 30):
            with self.subTest(budget=budget):
                root = self.build(budget)
                res = allocate(root)
                self.assertEqual(verify(root, res), [])
                self.assertEqual(res.totals["root"], min(budget, 19))
                self.assertEqual(res.total, res.totals["root"])
                for value in res.allocations.values():
                    self.assertIsInstance(value, int)

    def test_lower_bound_forces_allocation(self):
        root = Node.branch("root", [
            Node.branch("p", [leaf("a", 10, 1)], lower=3),
            Node.branch("q", [leaf("b", 10, 1)]),
        ], upper=5)
        res = allocate(root)
        self.assertEqual(verify(root, res), [])
        self.assertEqual(res.totals["root"], 5)
        self.assertGreaterEqual(res.totals["p"], 3)

    def test_verify_detects_tampering(self):
        root = self.build(10)
        res = allocate(root)
        broken = dict(res.totals)
        broken["d1"] += 1
        from allocator import AllocationResult
        tampered = AllocationResult(res.status, res.allocations, broken, None)
        self.assertTrue(verify(root, tampered))


class ValidationTests(unittest.TestCase):
    def test_duplicate_ids_rejected(self):
        root = Node.branch("root", [leaf("a", 1, 1), leaf("a", 1, 1)])
        with self.assertRaises(ValueError):
            allocate(root)

    def test_negative_demand_rejected(self):
        with self.assertRaises(ValueError):
            leaf("a", -1, 1)

    def test_branch_with_demand_rejected(self):
        with self.assertRaises(ValueError):
            Node("b", children=[leaf("a", 1, 1)], demand=1)


if __name__ == "__main__":
    unittest.main()
