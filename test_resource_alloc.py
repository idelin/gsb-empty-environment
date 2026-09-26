"""resource_alloc 的单元测试：python3.11 -m unittest -v"""
import unittest
from fractions import Fraction

from resource_alloc import Result, Status, Task, overlaps, peak_overlap, solve


def c5_tasks():
    """5 个互不重叠的任务 + 五元环不可同资源：色数 3，团数 2，可制造 下界<上界。"""
    tasks = [Task(f"t{i}", i, i + 1) for i in range(5)]
    edges = [(f"t{i}", f"t{(i + 1) % 5}") for i in range(5)]
    return tasks, edges


def assert_assignment_valid(testcase, tasks, cannot_share, result):
    assignment = result.assignment
    testcase.assertIsNotNone(assignment)
    testcase.assertEqual(set(assignment), {t.name for t in tasks})
    for t in tasks:
        if t.pinned is not None:
            testcase.assertEqual(assignment[t.name], t.pinned, f"{t.name} 未遵守指定资源")
    for i, a in enumerate(tasks):
        for b in tasks[i + 1:]:
            if overlaps(a, b):
                testcase.assertNotEqual(assignment[a.name], assignment[b.name],
                                        f"重叠任务 {a.name}/{b.name} 共享了资源")
    for x, y in cannot_share:
        testcase.assertNotEqual(assignment[x], assignment[y],
                                f"不可同资源任务 {x}/{y} 共享了资源")


class TestHalfOpenIntervals(unittest.TestCase):
    def test_touching_intervals_share_resource(self):
        tasks = [Task("a", 0, 5), Task("b", 5, 8)]
        self.assertFalse(overlaps(tasks[0], tasks[1]))
        result = solve(tasks)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.num_resources, 1)
        self.assertEqual(result.assignment["a"], result.assignment["b"])

    def test_strict_overlap_requires_two(self):
        tasks = [Task("a", 0, 5), Task("b", 4, 8)]
        self.assertTrue(overlaps(tasks[0], tasks[1]))
        result = solve(tasks)
        self.assertEqual(result.num_resources, 2)
        self.assertNotEqual(result.assignment["a"], result.assignment["b"])

    def test_peak_overlap_sweep(self):
        tasks = [Task("a", 0, 5), Task("b", 1, 3), Task("c", 2, 4), Task("d", 5, 8)]
        self.assertEqual(peak_overlap(tasks), 3)


class TestUnconstrainedEqualsPeak(unittest.TestCase):
    def test_resource_count_equals_peak_overlap(self):
        tasks = [
            Task("a", 0, 4), Task("b", 1, 6), Task("c", 2, 3),
            Task("d", 3, 8), Task("e", 5, 7), Task("f", 6, 9),
            Task("g", 8, 10),
        ]
        result = solve(tasks)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.num_resources, peak_overlap(tasks))
        self.assertEqual(result.lower_bound, result.num_resources)
        assert_assignment_valid(self, tasks, [], result)

    def test_empty_task_set(self):
        result = solve([])
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.num_resources, 0)
        self.assertEqual(result.assignment, {})


class TestPinnedResources(unittest.TestCase):
    def test_pinned_conflict_detected_early(self):
        tasks = [Task("a", 0, 5, pinned=0), Task("b", 2, 6, pinned=0)]
        result = solve(tasks)
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertIsNone(result.assignment)
        self.assertEqual(result.nodes, 0)  # 进入搜索前提前发现

    def test_pinned_touching_intervals_no_conflict(self):
        tasks = [Task("a", 0, 5, pinned=0), Task("b", 5, 8, pinned=0)]
        result = solve(tasks)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.num_resources, 1)

    def test_pinned_ids_are_absolute(self):
        tasks = [Task("a", 0, 2, pinned=2), Task("b", 0, 2)]
        result = solve(tasks)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.assignment["a"], 2)
        self.assertEqual(result.num_resources, 3)  # 资源池必须包含编号 2

    def test_pinned_via_cannot_share_conflict(self):
        tasks = [Task("a", 0, 2, pinned=1), Task("b", 4, 6, pinned=1)]
        result = solve(tasks, cannot_share=[("a", "b")])
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertEqual(result.nodes, 0)


class TestCannotShare(unittest.TestCase):
    def test_disjoint_tasks_forced_apart(self):
        tasks = [Task("a", 0, 2), Task("b", 3, 5)]
        result = solve(tasks, cannot_share=[("a", "b")])
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.num_resources, 2)
        self.assertNotEqual(result.assignment["a"], result.assignment["b"])

    def test_odd_cycle_needs_three(self):
        tasks, edges = c5_tasks()
        result = solve(tasks, cannot_share=edges)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.num_resources, 3)
        self.assertEqual(result.lower_bound, 2)
        assert_assignment_valid(self, tasks, edges, result)


class TestRationals(unittest.TestCase):
    def test_fraction_endpoints_share(self):
        tasks = [Task("a", 0, Fraction(5, 2)), Task("b", Fraction(5, 2), 4)]
        result = solve(tasks)
        self.assertEqual(result.num_resources, 1)
        self.assertEqual(result.assignment["a"], result.assignment["b"])

    def test_mixed_int_and_fraction(self):
        tasks = [
            Task("a", 0, Fraction(3, 2)),
            Task("b", 1, 2),
            Task("c", Fraction(3, 2), Fraction(7, 3)),
        ]
        self.assertEqual(peak_overlap(tasks), 2)
        result = solve(tasks)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.num_resources, 2)
        self.assertEqual(result.assignment["a"], result.assignment["c"])


class TestBudgetTiers(unittest.TestCase):
    def test_undecided_feasible_optimal_separated(self):
        tasks, edges = c5_tasks()
        full = solve(tasks, cannot_share=edges, budget=10**6)
        self.assertEqual(full.status, Status.OPTIMAL)
        self.assertGreater(full.nodes, len(tasks))  # 最优性证明确实需要搜索

        undecided = solve(tasks, cannot_share=edges, budget=len(tasks) - 1)
        self.assertEqual(undecided.status, Status.UNDECIDED)
        self.assertIsNone(undecided.assignment)
        self.assertIsNone(undecided.upper_bound)
        self.assertEqual(undecided.lower_bound, 2)

        feasible = solve(tasks, cannot_share=edges, budget=full.nodes - 1)
        self.assertEqual(feasible.status, Status.FEASIBLE)
        self.assertEqual(feasible.num_resources, 3)
        self.assertLess(feasible.lower_bound, feasible.num_resources)
        assert_assignment_valid(self, tasks, edges, feasible)

    def test_bounds_sandwich(self):
        tasks, edges = c5_tasks()
        for budget in (0, 3, 5, 7, 10, 50, 10**6):
            result = solve(tasks, cannot_share=edges, budget=budget)
            self.assertLessEqual(result.nodes, budget + 1)
            if result.assignment is not None:
                self.assertLessEqual(result.lower_bound, result.num_resources)
                self.assertLessEqual(result.num_resources, result.upper_bound)


class TestInputValidation(unittest.TestCase):
    def test_start_must_precede_end(self):
        with self.assertRaises(ValueError):
            Task("a", 5, 5)
        with self.assertRaises(ValueError):
            Task("b", 6, 5)

    def test_rejects_non_int_or_fraction(self):
        with self.assertRaises(TypeError):
            Task("a", 0.0, 1.0)
        with self.assertRaises(TypeError):
            Task("b", True, 2)

    def test_duplicate_names_rejected(self):
        with self.assertRaises(ValueError):
            solve([Task("a", 0, 1), Task("a", 1, 2)])

    def test_unknown_cannot_share_name(self):
        with self.assertRaises(ValueError):
            solve([Task("a", 0, 1)], cannot_share=[("a", "ghost")])

    def test_self_cannot_share_rejected(self):
        with self.assertRaises(ValueError):
            solve([Task("a", 0, 1)], cannot_share=[("a", "a")])

    def test_bad_pinned(self):
        with self.assertRaises(ValueError):
            Task("a", 0, 1, pinned=-1)
        with self.assertRaises(TypeError):
            Task("b", 0, 1, pinned=1.5)

    def test_bad_budget(self):
        with self.assertRaises(ValueError):
            solve([Task("a", 0, 1)], budget=-1)
        with self.assertRaises(TypeError):
            solve([Task("a", 0, 1)], budget=1.5)


if __name__ == "__main__":
    unittest.main()
