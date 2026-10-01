import unittest
from fractions import Fraction

from planrepair import (
    Disruption,
    Plan,
    Placement,
    Resource,
    Status,
    Task,
    TimeWindow,
    repair,
)


def base_problem():
    resources = [
        Resource("A", (TimeWindow(0, 24),)),
        Resource("B", (TimeWindow(0, 24),)),
    ]
    tasks = [
        Task("t1", 3, {"A": 2}),
        Task("t2", 3, {"A": 1, "B": 4}),
        Task("t3", 4, {"B": 2}),
        Task("t4", 2, {"A": 1, "B": 3}),
    ]
    plan = Plan(
        {
            "t1": Placement("A", 0),
            "t2": Placement("A", 5),
            "t3": Placement("B", 6),
            "t4": Placement("A", 10),
        },
        executed={"t1"},
    )
    return resources, tasks, plan


def assert_plan_feasible(test, result, tasks_by_id, resources_by_id, now):
    """Independent feasibility check of a returned plan."""
    assignments = result.plan.assignments
    for tid, placement in assignments.items():
        task = tasks_by_id[tid]
        start, end = placement.start, placement.start + task.duration
        if tid not in result.plan.executed:
            test.assertGreaterEqual(start, now, f"{tid} starts before now")
        if placement.rid in resources_by_id:  # failed resources only keep frozen tasks
            res = resources_by_id[placement.rid]
            test.assertTrue(
                any(w.start <= start and end <= w.end for w in res.windows),
                f"{tid} outside windows of {placement.rid}",
            )
        test.assertGreaterEqual(start, task.release)
        if task.deadline is not None:
            test.assertLessEqual(end, task.deadline)
    # capacity / overlap check per resource
    for rid, res in resources_by_id.items():
        intervals = [
            (p.start, p.start + tasks_by_id[tid].duration)
            for tid, p in assignments.items() if p.rid == rid
        ]
        points = sorted({x for iv in intervals for x in iv})
        for a, b in zip(points, points[1:]):
            load = sum(1 for s, e in intervals if s <= a and e >= b)
            test.assertLessEqual(load, res.capacity, f"capacity of {rid} exceeded")


class TestRepair(unittest.TestCase):
    def test_no_disruption_keeps_everything(self):
        resources, tasks, plan = base_problem()
        result = repair(resources, tasks, plan, now=4)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.moved_count, 0)
        self.assertEqual(result.total_cost, 1 + 2 + 1)  # t2 + t3 + t4 (t1 frozen/sunk)
        kinds = {c.kind for c in result.changes}
        self.assertEqual(kinds, {"frozen", "kept"})
        self.assertEqual(result.plan.assignments, plan.assignments)

    def test_added_task_fits_without_moves(self):
        resources, tasks, plan = base_problem()
        disruption = Disruption(added_tasks=(Task("n1", 2, {"B": 5}),))
        result = repair(resources, tasks, plan, disruption, now=4)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.moved_count, 0)
        added = [c for c in result.changes if c.kind == "added"]
        self.assertEqual(len(added), 1)
        self.assertEqual(added[0].new.rid, "B")
        self.assertGreaterEqual(added[0].new.start, 4)

    def test_resource_failure_forces_minimal_moves(self):
        resources, tasks, plan = base_problem()
        disruption = Disruption(
            added_tasks=(Task("n1", 2, {"A": 1, "B": 2}),),
            failed_resources=frozenset({"A"}),
        )
        result = repair(resources, tasks, plan, disruption, now=4)
        self.assertEqual(result.status, Status.OPTIMAL)
        # t2 and t4 sat on failed A -> forced; t3 unaffected and stays.
        self.assertEqual(result.moved_count, 2)
        self.assertEqual(result.stats.forced_moves, 2)
        by_tid = {c.tid: c for c in result.changes}
        self.assertEqual(by_tid["t3"].kind, "kept")
        self.assertEqual(by_tid["t3"].new, Placement("B", 6))
        self.assertEqual(by_tid["t2"].kind, "moved")
        self.assertEqual(by_tid["t4"].kind, "moved")
        self.assertEqual(by_tid["t1"].kind, "frozen")
        self.assertEqual(by_tid["t1"].new, Placement("A", 0))
        self.assertEqual(by_tid["n1"].kind, "added")
        # all remaining tasks must land on B: cost is fixed at 4+2+3+2 = 11
        self.assertEqual(result.total_cost, 11)
        tasks_by_id = {t.tid: t for t in tasks} | {"n1": Task("n1", 2, {"A": 1, "B": 2})}
        assert_plan_feasible(self, result, tasks_by_id, {"B": resources[1]}, 4)

    def test_executed_task_on_failed_resource_is_not_rerun(self):
        resources, tasks, plan = base_problem()
        disruption = Disruption(failed_resources=frozenset({"A"}))
        result = repair(resources, tasks, plan, disruption, now=4)
        self.assertEqual(result.status, Status.OPTIMAL)
        t1 = result.plan.assignments["t1"]
        self.assertEqual((t1.rid, t1.start), ("A", 0))  # frozen in the past
        self.assertIn("t1", result.plan.executed)
        self.assertEqual(result.moved_count, 2)  # only t2, t4

    def test_moves_minimised_before_cost(self):
        # Repair with 1 move costs 7; repair with 2 moves costs 3.
        # Lexicographic objective must pick 1 move / cost 7.
        resources = [
            Resource("A", (TimeWindow(0, 10),)),
            Resource("B", (TimeWindow(0, 10),)),
        ]
        tasks = [
            Task("x", 2, {"A": 5, "B": 1}),
            Task("y", 2, {"A": 5, "B": 1}),
        ]
        plan = Plan({"x": Placement("A", 0), "y": Placement("A", 2)})
        disruption = Disruption(added_tasks=(Task("n", 8, {"A": 1}),))
        result = repair(resources, tasks, plan, disruption, now=0)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.moved_count, 1)
        self.assertEqual(result.total_cost, 7)
        by_tid = {c.tid: c for c in result.changes}
        self.assertEqual(by_tid["x"].kind, "kept")
        self.assertEqual(by_tid["y"].kind, "moved")
        self.assertEqual(by_tid["y"].new.rid, "B")
        self.assertEqual(by_tid["n"].new, Placement("A", 2))

    def test_cost_optimised_among_equal_moves(self):
        resources = [
            Resource("A", (TimeWindow(0, 10),)),
            Resource("B", (TimeWindow(0, 10),)),
            Resource("C", (TimeWindow(0, 10),)),
        ]
        tasks = [Task("w", 2, {"A": 2, "B": 5, "C": 1})]
        plan = Plan({"w": Placement("A", 0)})
        disruption = Disruption(failed_resources=frozenset({"A"}))
        result = repair(resources, tasks, plan, disruption, now=0)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.moved_count, 1)
        self.assertEqual(result.total_cost, 1)
        self.assertEqual(result.plan.assignments["w"].rid, "C")

    def test_infeasible_keeps_original_plan_with_evidence(self):
        resources, tasks, plan = base_problem()
        disruption = Disruption(
            added_tasks=(Task("n2", 2, {"A": 1}),),
            failed_resources=frozenset({"A"}),
        )
        result = repair(resources, tasks, plan, disruption, now=4)
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertFalse(result.ok)
        self.assertEqual(result.plan, plan)  # original plan retained
        self.assertEqual(result.changes, ())
        self.assertIsNone(result.total_cost)
        self.assertEqual(len(result.conflicts), 1)
        self.assertEqual(result.conflicts[0].tid, "n2")
        self.assertTrue(any("failed" in r for r in result.conflicts[0].reasons))

    def test_infeasible_capacity_contention(self):
        resources = [Resource("A", (TimeWindow(0, 2),), capacity=2)]
        tasks = [Task("t1", 2, {"A": 1}), Task("t2", 2, {"A": 1})]
        plan = Plan({"t1": Placement("A", 0), "t2": Placement("A", 0)})
        disruption = Disruption(added_tasks=(Task("n1", 2, {"A": 1}),))
        result = repair(resources, tasks, plan, disruption, now=0)
        self.assertEqual(result.status, Status.INFEASIBLE)
        self.assertEqual(result.plan, plan)
        self.assertEqual(result.conflicts[0].tid, "*")
        self.assertTrue(any("contention" in r for r in result.conflicts[0].reasons))

    def test_budget_undecided_without_incumbent(self):
        resources, tasks, plan = base_problem()
        disruption = Disruption(
            added_tasks=(Task("n1", 2, {"A": 1, "B": 2}),),
            failed_resources=frozenset({"A"}),
        )
        result = repair(resources, tasks, plan, disruption, now=4, node_budget=0)
        self.assertEqual(result.status, Status.BUDGET_UNDECIDED)
        self.assertFalse(result.ok)
        self.assertEqual(result.plan, plan)  # original plan retained
        self.assertEqual(result.changes, ())

    def test_feasible_but_uncertified_under_tight_budget(self):
        resources, tasks, plan = base_problem()
        disruption = Disruption(
            added_tasks=(Task("n1", 2, {"A": 1, "B": 2}),),
            failed_resources=frozenset({"A"}),
        )
        full = repair(resources, tasks, plan, disruption, now=4)
        self.assertEqual(full.status, Status.OPTIMAL)
        tight = repair(resources, tasks, plan, disruption, now=4,
                       node_budget=full.stats.nodes - 1)
        self.assertEqual(tight.status, Status.FEASIBLE)
        self.assertTrue(tight.ok)
        self.assertTrue(tight.stats.aborted)
        # the incumbent is a valid plan even though optimality is uncertified
        tasks_by_id = {t.tid: t for t in tasks} | {"n1": Task("n1", 2, {"A": 1, "B": 2})}
        assert_plan_feasible(self, tight, tasks_by_id, {"B": resources[1]}, 4)

    def test_rational_data_on_grid(self):
        resources = [Resource("R", (TimeWindow(0, 2),))]
        tasks = [Task("u1", Fraction(1, 2), {"R": 1})]
        plan = Plan({"u1": Placement("R", 0)}, executed={"u1"})
        disruption = Disruption(added_tasks=(
            Task("u2", "1/2", {"R": 1}),
            Task("u3", "3/4", {"R": 1}),
        ))
        result = repair(resources, tasks, plan, disruption, now=Fraction(1, 2))
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.moved_count, 0)
        for tid in ("u2", "u3"):
            start = result.plan.assignments[tid].start
            self.assertEqual(start.denominator % 4 % 2, 0)  # on the 1/4 grid
            self.assertGreaterEqual(start, Fraction(1, 2))
        tasks_by_id = {"u1": tasks[0], "u2": Task("u2", "1/2", {"R": 1}),
                       "u3": Task("u3", "3/4", {"R": 1})}
        assert_plan_feasible(self, result, tasks_by_id, {"R": resources[0]}, Fraction(1, 2))

    def test_capacity_allows_parallel_tasks(self):
        resources = [Resource("A", (TimeWindow(0, 4),), capacity=2)]
        tasks = [Task("t1", 2, {"A": 1}), Task("t2", 2, {"A": 1})]
        plan = Plan({"t1": Placement("A", 0), "t2": Placement("A", 0)})
        result = repair(resources, tasks, plan, now=0)
        self.assertEqual(result.status, Status.OPTIMAL)
        self.assertEqual(result.moved_count, 0)

    def test_deterministic(self):
        resources, tasks, plan = base_problem()
        disruption = Disruption(
            added_tasks=(Task("n1", 2, {"A": 1, "B": 2}),),
            failed_resources=frozenset({"A"}),
        )
        r1 = repair(resources, tasks, plan, disruption, now=4)
        r2 = repair(resources, tasks, plan, disruption, now=4)
        self.assertEqual(r1.plan.assignments, r2.plan.assignments)
        self.assertEqual(r1.changes, r2.changes)

    def test_validation_errors(self):
        resources, tasks, plan = base_problem()
        with self.assertRaises(ValueError):  # executed task not in assignments
            Plan({"t1": Placement("A", 0)}, executed={"nope"})
        with self.assertRaises(ValueError):  # unknown failed resource
            repair(resources, tasks, plan, Disruption(failed_resources=frozenset({"ZZ"})), now=4)
        with self.assertRaises(ValueError):  # added task id collision
            repair(resources, tasks, plan,
                   Disruption(added_tasks=(Task("t2", 1, {"B": 1}),)), now=4)
        with self.assertRaises(ValueError):  # original plan not feasible (overlap)
            bad_plan = Plan({"t1": Placement("A", 0), "t2": Placement("A", 1)})
            repair(resources, tasks, bad_plan, now=0)
        with self.assertRaises(ValueError):  # executed task ends after now
            late = Plan({"t1": Placement("A", 0), "t2": Placement("A", 5),
                         "t3": Placement("B", 6), "t4": Placement("A", 10)},
                        executed={"t1"})
            repair(resources, tasks, late, now=2)
        with self.assertRaises(ValueError):  # non-positive duration
            Task("bad", 0, {"A": 1})


if __name__ == "__main__":
    unittest.main()
