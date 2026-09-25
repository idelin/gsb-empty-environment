import unittest

from migration import (Executor, Phase, Plan, StaleAckError)
from migration.solver import Move


def make_executor(budget=1000, generation=0):
    capacities = {"s0": 100, "s1": 100, "s2": 100}
    initial = {"a": ("s0", 60), "b": ("s0", 40), "c": ("s1", 50), "d": ("s1", 30)}
    return Executor(capacities, initial, budget=budget, generation=generation)


def make_plan(generation=1):
    moves = (Move("b", "s0", "s2", 40), Move("d", "s1", "s2", 30))
    return Plan(plan_id="plan-1", generation=generation, moves=moves)


def assert_single_ownership(testcase, ex):
    owners = ex.owners_snapshot()
    testcase.assertEqual(len(owners), len(set(owners)))
    for pid, sid in owners.items():
        testcase.assertEqual(ex.owner_of(pid), sid)


class TestPhasedExecution(unittest.TestCase):
    def test_phases_run_in_order(self):
        ex = make_executor()
        report = ex.execute(make_plan())
        self.assertTrue(report.success)
        kinds = [s.split()[0] for s in report.steps]
        self.assertEqual(kinds, ["reserve", "reserve", "copy", "copy",
                                 "switch", "switch", "release", "release"])

    def test_success_moves_ownership_and_budget(self):
        ex = make_executor()
        report = ex.execute(make_plan())
        self.assertTrue(report.success)
        self.assertFalse(report.rolled_back)
        self.assertEqual(ex.owner_of("b"), "s2")
        self.assertEqual(ex.owner_of("d"), "s2")
        self.assertEqual(ex.owned_loads(), {"s0": 60, "s1": 50, "s2": 70})
        self.assertEqual(ex.budget_spent, 70)
        self.assertEqual(ex.budget_remaining, 930)
        self.assertEqual(ex.generation, 1)
        for sid in ("s0", "s1", "s2"):
            self.assertEqual(ex.reserved_bytes(sid), 0)
        assert_single_ownership(self, ex)

    def test_failure_rolls_back_step_by_step(self):
        ex = make_executor()
        before = ex.owners_snapshot()
        report = ex.execute(make_plan(), fail_at=(Phase.SWITCH, "d"))
        self.assertFalse(report.success)
        self.assertTrue(report.rolled_back)
        rollbacks = [s for s in report.steps if s.startswith("rollback")]
        self.assertEqual(len(rollbacks), 5)  # switch b, copies x2, reserves x2
        self.assertEqual(ex.owners_snapshot(), before)
        self.assertEqual(ex.budget_spent, 0)
        self.assertEqual(ex.budget_remaining, 1000)  # no budget leak
        for sid in ("s0", "s1", "s2"):
            self.assertEqual(ex.reserved_bytes(sid), 0)
        assert_single_ownership(self, ex)

    def test_budget_shortfall_aborts_and_refunds(self):
        ex = make_executor(budget=50)  # first move needs 40, second needs 30
        report = ex.execute(make_plan())
        self.assertFalse(report.success)
        self.assertEqual(ex.budget_spent, 0)
        self.assertEqual(ex.budget_remaining, 50)
        self.assertEqual(ex.owner_of("b"), "s0")
        self.assertEqual(ex.owner_of("d"), "s1")

    def test_capacity_shortfall_aborts(self):
        capacities = {"s0": 100, "s1": 100, "s2": 50}  # s2 cannot hold 40+30
        initial = {"a": ("s0", 60), "b": ("s0", 40), "c": ("s1", 50), "d": ("s1", 30)}
        ex = Executor(capacities, initial, budget=1000)
        report = ex.execute(make_plan())
        self.assertFalse(report.success)
        self.assertEqual(ex.budget_spent, 0)
        assert_single_ownership(self, ex)

    def test_stale_generation_ack_rejected(self):
        ex = make_executor()
        ex.execute(make_plan(generation=7))
        with self.assertRaises(StaleAckError):
            ex.ack("b", generation=6)
        self.assertTrue(ex.ack("b", generation=7))

    def test_replay_is_idempotent(self):
        ex = make_executor()
        plan = make_plan()
        first = ex.execute(plan)
        self.assertTrue(first.success)
        snapshot = ex.owners_snapshot()
        replay = ex.execute(plan)
        self.assertTrue(replay.success)
        self.assertTrue(all("skipped" in s for s in replay.steps))
        self.assertEqual(ex.owners_snapshot(), snapshot)
        self.assertEqual(ex.budget_spent, 70)  # not charged twice
        self.assertEqual(ex.owned_loads(), {"s0": 60, "s1": 50, "s2": 70})
        for sid in ("s0", "s1", "s2"):
            self.assertEqual(ex.reserved_bytes(sid), 0)  # no double occupancy

    def test_retry_after_failure_works(self):
        ex = make_executor()
        plan = make_plan()
        failed = ex.execute(plan, fail_at=(Phase.COPY, "d"))
        self.assertFalse(failed.success)
        retry = ex.execute(plan)
        self.assertTrue(retry.success)
        self.assertEqual(ex.budget_spent, 70)
        self.assertEqual(ex.owned_loads(), {"s0": 60, "s1": 50, "s2": 70})


if __name__ == "__main__":
    unittest.main()
