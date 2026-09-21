import unittest

from scheduler.model import ProblemError, load_problem
from scheduler.scheduler import InfeasibleError, lower_bound, schedule


def make_problem(tasks, cpu=4, memory=8, **extra):
    data = {
        "version": "1.0.0",
        "capacity": {"cpu": cpu, "memory": memory},
        "tasks": tasks,
    }
    data.update(extra)
    return load_problem(data)


def assert_valid_schedule(case, problem, result):
    tasks = problem.task_map()
    entries = {entry.id: entry for entry in result.entries}
    case.assertEqual(set(entries), set(tasks))
    for entry in result.entries:
        case.assertEqual(entry.end - entry.start, tasks[entry.id].duration)
        for dep in tasks[entry.id].deps:
            case.assertLessEqual(entries[dep].end, entry.start)
    points = sorted(
        {e.start for e in result.entries} | {e.end for e in result.entries}
    )
    for start, end in zip(points, points[1:]):
        active = [e for e in result.entries if e.start <= start < e.end]
        case.assertLessEqual(
            sum(tasks[e.id].cpu for e in active), problem.cpu_capacity
        )
        case.assertLessEqual(
            sum(tasks[e.id].memory for e in active), problem.memory_capacity
        )
    case.assertEqual(result.makespan, max(e.end for e in result.entries))
    case.assertLessEqual(result.lower_bound, result.makespan)


class TestScheduling(unittest.TestCase):
    def test_valid_schedule_on_diamond(self):
        problem = make_problem([
            {"id": "a", "cpu": 2, "memory": 2, "duration": 2},
            {"id": "b", "cpu": 2, "memory": 2, "duration": 3, "deps": ["a"]},
            {"id": "c", "cpu": 2, "memory": 2, "duration": 3, "deps": ["a"]},
            {"id": "d", "cpu": 1, "memory": 1, "duration": 1, "deps": ["b", "c"]},
        ])
        assert_valid_schedule(self, problem, schedule(problem))

    def test_deterministic(self):
        problem = make_problem([
            {"id": "a", "cpu": 1, "memory": 1, "duration": 2},
            {"id": "b", "cpu": 2, "memory": 3, "duration": 4},
            {"id": "c", "cpu": 1, "memory": 2, "duration": 1, "deps": ["a"]},
            {"id": "d", "cpu": 3, "memory": 1, "duration": 2, "deps": ["b"]},
        ])
        first = schedule(problem)
        second = schedule(load_problem({
            "version": "1.0.0",
            "capacity": {"cpu": 4, "memory": 8},
            "tasks": [
                {"id": "a", "cpu": 1, "memory": 1, "duration": 2},
                {"id": "b", "cpu": 2, "memory": 3, "duration": 4},
                {"id": "c", "cpu": 1, "memory": 2, "duration": 1, "deps": ["a"]},
                {"id": "d", "cpu": 3, "memory": 1, "duration": 2, "deps": ["b"]},
            ],
        }))
        self.assertEqual(first, second)

    def test_memory_pair_cannot_overlap(self):
        # Both tasks are ready at t=0 but 6 + 6 > 10 memory, so they must
        # run sequentially even though cpu would allow both.
        problem = make_problem(
            [
                {"id": "x", "cpu": 1, "memory": 6, "duration": 3},
                {"id": "y", "cpu": 1, "memory": 6, "duration": 2},
            ],
            cpu=4,
            memory=10,
        )
        result = schedule(problem)
        assert_valid_schedule(self, problem, result)
        self.assertEqual(result.makespan, 5)

    def test_cpu_pair_cannot_overlap(self):
        problem = make_problem(
            [
                {"id": "x", "cpu": 3, "memory": 1, "duration": 2},
                {"id": "y", "cpu": 3, "memory": 1, "duration": 2},
            ],
            cpu=4,
            memory=8,
        )
        result = schedule(problem)
        assert_valid_schedule(self, problem, result)
        self.assertEqual(result.makespan, 4)

    def test_single_task_over_capacity_is_infeasible(self):
        problem = make_problem(
            [{"id": "big", "cpu": 5, "memory": 1, "duration": 1}], cpu=4
        )
        with self.assertRaises(InfeasibleError):
            schedule(problem)
        problem = make_problem(
            [{"id": "big", "cpu": 1, "memory": 9, "duration": 1}], memory=8
        )
        with self.assertRaises(InfeasibleError):
            schedule(problem)

    def test_chain_reaches_critical_path_bound(self):
        problem = make_problem([
            {"id": "a", "cpu": 4, "memory": 8, "duration": 2},
            {"id": "b", "cpu": 4, "memory": 8, "duration": 2, "deps": ["a"]},
            {"id": "c", "cpu": 4, "memory": 8, "duration": 2, "deps": ["b"]},
        ])
        result = schedule(problem)
        assert_valid_schedule(self, problem, result)
        self.assertEqual(result.makespan, 6)
        self.assertEqual(result.lower_bound, 6)

    def test_resource_bound(self):
        problem = make_problem(
            [
                {"id": name, "cpu": 1, "memory": 1, "duration": 1}
                for name in ("a", "b", "c", "d")
            ],
            cpu=2,
            memory=2,
        )
        result = schedule(problem)
        assert_valid_schedule(self, problem, result)
        self.assertEqual(result.makespan, 2)
        self.assertEqual(result.lower_bound, 2)

    def test_lower_bound_takes_max_of_bounds(self):
        # Critical path = 5, cpu work bound = ceil(14/2) = 7 dominates.
        problem = make_problem(
            [
                {"id": "a", "cpu": 2, "memory": 1, "duration": 3},
                {"id": "b", "cpu": 2, "memory": 1, "duration": 3},
                {"id": "c", "cpu": 1, "memory": 1, "duration": 2, "deps": ["a"]},
            ],
            cpu=2,
            memory=8,
        )
        self.assertEqual(lower_bound(problem), 7)


class TestInputValidation(unittest.TestCase):
    def test_cycle_rejected(self):
        with self.assertRaises(ProblemError):
            make_problem([
                {"id": "a", "cpu": 1, "memory": 1, "duration": 1, "deps": ["b"]},
                {"id": "b", "cpu": 1, "memory": 1, "duration": 1, "deps": ["a"]},
            ])

    def test_unknown_dep_rejected(self):
        with self.assertRaises(ProblemError):
            make_problem([
                {"id": "a", "cpu": 1, "memory": 1, "duration": 1, "deps": ["zz"]},
            ])

    def test_duplicate_id_rejected(self):
        with self.assertRaises(ProblemError):
            make_problem([
                {"id": "a", "cpu": 1, "memory": 1, "duration": 1},
                {"id": "a", "cpu": 1, "memory": 1, "duration": 1},
            ])

    def test_bad_version_rejected(self):
        with self.assertRaises(ProblemError):
            load_problem({
                "version": "1.0",
                "capacity": {"cpu": 1, "memory": 1},
                "tasks": [{"id": "a", "cpu": 1, "memory": 1, "duration": 1}],
            })

    def test_prerelease_version_accepted(self):
        problem = make_problem(
            [{"id": "a", "cpu": 1, "memory": 1, "duration": 1}],
            version="2.0.0-alpha.1",
            packages=[{"name": "zlib", "version": "1.2.13"}],
        )
        self.assertEqual(str(problem.version), "2.0.0-alpha.1")
        self.assertEqual(str(problem.packages[0].version), "1.2.13")

    def test_bad_package_version_rejected(self):
        with self.assertRaises(ProblemError):
            make_problem(
                [{"id": "a", "cpu": 1, "memory": 1, "duration": 1}],
                packages=[{"name": "zlib", "version": "1.2"}],
            )

    def test_non_positive_values_rejected(self):
        with self.assertRaises(ProblemError):
            make_problem([{"id": "a", "cpu": 0, "memory": 1, "duration": 1}])
        with self.assertRaises(ProblemError):
            make_problem([{"id": "a", "cpu": 1, "memory": 1, "duration": 0}])
        with self.assertRaises(ProblemError):
            make_problem(
                [{"id": "a", "cpu": 1, "memory": 1, "duration": 1}], cpu=0
            )


if __name__ == "__main__":
    unittest.main()
