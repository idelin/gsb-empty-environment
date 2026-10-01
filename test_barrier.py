"""Scenario-based unit tests for the barrier model and its model checker."""
import unittest

from barrier_model import (ACTIVE, COMMIT, NO_MSG, OUT, RELEASED, WAITING,
                           BarrierModel)
from model_check import (bfs_safety, check_liveness, replay, replay_lasso)
from verify_reduction import CONFIGS, check_configs


def drive(model, actions):
    """Apply a concrete trace; return the final state."""
    s = model.initial_state()
    for a in actions:
        assert a in model.actions(s), f"{a} not enabled in {s}"
        s = model.step(s, a)
    return s


class CorrectModelSafety(unittest.TestCase):
    def test_full_interleaving_passes(self):
        r = bfs_safety(BarrierModel(n=3))
        self.assertEqual(r.verdict, "PASS")
        self.assertGreater(r.states, 1000)

    def test_symmetry_reduction_same_verdict(self):
        m = BarrierModel(n=3)
        full = bfs_safety(m, use_symmetry=False)
        reduced = bfs_safety(m, use_symmetry=True)
        self.assertEqual(full.verdict, reduced.verdict)
        self.assertLess(reduced.states, full.states)

    def test_reference_check_small_configs(self):
        mismatches = check_configs(ns=(2,), verbose=False)
        self.assertEqual(mismatches, [])


class RoundConfusionBugs(unittest.TestCase):
    BUGS = [
        ("bug_stale_arrival", "I2"),
        ("bug_stale_release", "T3"),
        ("bug_commit_no_reset", "I5"),
    ]

    def test_each_bug_found_with_replayable_shortest_ce(self):
        for flag, tag in self.BUGS:
            with self.subTest(flag=flag):
                m = BarrierModel(n=3, **{flag: True})
                r = bfs_safety(m)
                self.assertEqual(r.verdict, "FAIL")
                self.assertTrue(any(tag in v for v in r.violation))
                rep = replay(m, r.counterexample)
                self.assertTrue(rep.ok)
                self.assertEqual(rep.violation, r.violation)
                # BFS gives the shortest counterexample: no proper
                # prefix of it may violate anything.
                for k in range(1, len(r.counterexample)):
                    pre = replay(m, r.counterexample[:k])
                    self.assertTrue(pre.ok)
                    self.assertEqual(pre.violation, [])

    def test_stale_arrival_ce_is_round_confusion(self):
        m = BarrierModel(n=3, bug_stale_arrival=True)
        r = bfs_safety(m)
        self.assertIn("commit", [a[0] for a in r.counterexample],
                      "counterexample must cross a round boundary")


class ReleaseOnceScenarios(unittest.TestCase):
    """Last arrival racing with a concurrent exit: exactly one release."""

    def test_last_arrival_then_leave_releases_once(self):
        m = BarrierModel(n=2)
        s = drive(m, [("join", 0), ("join", 1),
                      ("send_arrive", 0), ("proc_arrive", 0),
                      ("send_arrive", 1), ("proc_arrive", 1),  # last arrival
                      ("leave", 1),                            # concurrent exit
                      COMMIT])
        self.assertEqual(s.status[0], RELEASED)
        self.assertEqual(s.status[1], OUT)
        self.assertEqual(s.arrived, frozenset())
        self.assertNotIn(COMMIT, m.actions(s), "second release must not fire")

    def test_leave_before_last_arrival_shrinks_barrier(self):
        m = BarrierModel(n=2)
        s = drive(m, [("join", 0), ("join", 1),
                      ("send_arrive", 0), ("proc_arrive", 0),
                      ("leave", 1),          # shrink: members={0}=arrived
                      COMMIT])
        self.assertEqual(s.status[0], RELEASED)
        self.assertEqual(s.members, frozenset({0}))

    def test_commit_only_once_per_round_globally(self):
        r = bfs_safety(BarrierModel(n=3))
        self.assertEqual(r.verdict, "PASS")  # T1/T2 never violated


class StaleMessageScenarios(unittest.TestCase):
    def test_delayed_release_notification_cannot_wake_next_round(self):
        m = BarrierModel(n=1)
        s = drive(m, [("join", 0), ("send_arrive", 0), ("proc_arrive", 0),
                      COMMIT,                    # p0 RELEASED, rel tag = 0
                      ("cancel", 0),             # give up; notification pending
                      ("send_arrive", 0), ("proc_arrive", 0),  # waits round 1
                      ("deliver", 0)])           # stale notification arrives
        self.assertEqual(s.status[0], WAITING,
                         "old-round notification must not release a new-round waiter")
        self.assertEqual(s.rel_msg[0], NO_MSG)

    def test_stale_arrival_rejected_in_new_round(self):
        m = BarrierModel(n=2)
        s = drive(m, [("join", 0), ("send_arrive", 0), ("proc_arrive", 0),
                      ("join", 1),               # pending: barrier mid-round
                      ("send_arrive", 1),        # early arrival, tag = round 0
                      COMMIT,                    # round 1; p1 becomes member
                      ("proc_arrive", 1)])
        self.assertEqual(s.status[1], ACTIVE)
        self.assertEqual(s.arrived, frozenset(),
                         "old-round arrival must not count in the new round")

    def test_disconnect_shrinks_barrier_and_allows_commit(self):
        m = BarrierModel(n=2)
        s = drive(m, [("join", 0), ("join", 1),
                      ("send_arrive", 0), ("proc_arrive", 0),
                      ("disconnect", 1),         # dynamic shrink
                      COMMIT])
        self.assertEqual(s.status[0], RELEASED)
        self.assertEqual(s.members, frozenset({0}))


class LivenessChecks(unittest.TestCase):
    def test_correct_model_fair_liveness_passes(self):
        r = check_liveness(BarrierModel(n=3), fair=True)
        self.assertEqual(r.verdict, "PASS")

    def test_unfair_scheduler_starves_waiter_lasso_replays(self):
        m = BarrierModel(n=2)
        r = check_liveness(m, fair=False)
        self.assertEqual(r.verdict, "FAIL")
        lr = replay_lasso(m, r.prefix, r.cycle, r.victim)
        self.assertTrue(lr.prefix_ok and lr.cycle_ok)
        self.assertTrue(lr.closed, "lasso cycle must close")
        self.assertTrue(lr.victim_stuck, "victim never released on the cycle")

    def test_blocking_wait_without_commit_is_unfair_lasso(self):
        m = BarrierModel(n=2, bug_no_commit=True, blocking_wait=True)
        r = check_liveness(m, fair=True)
        self.assertEqual(r.verdict, "FAIL")
        lr = replay_lasso(m, r.prefix, r.cycle, r.victim)
        self.assertTrue(lr.closed and lr.victim_stuck)


class BudgetDiscipline(unittest.TestCase):
    def test_tiny_budget_is_unknown_not_pass(self):
        m = BarrierModel(n=3)
        self.assertEqual(bfs_safety(m, max_states=100).verdict, "UNKNOWN")
        self.assertEqual(check_liveness(m, max_states=100).verdict, "UNKNOWN")

    def test_unknown_never_reported_as_safe(self):
        r = bfs_safety(BarrierModel(n=3), max_states=100)
        self.assertNotEqual(r.verdict, "PASS")
        self.assertIsNone(r.counterexample)


class ReplayChecks(unittest.TestCase):
    def test_replay_rejects_disabled_action(self):
        m = BarrierModel(n=2)
        rep = replay(m, [("join", 0), ("commit", -1)])
        self.assertFalse(rep.ok)
        self.assertIsNotNone(rep.error)

    def test_replay_of_clean_trace_has_no_violation(self):
        m = BarrierModel(n=2)
        rep = replay(m, [("join", 0), ("join", 1),
                         ("send_arrive", 0), ("proc_arrive", 0),
                         ("send_arrive", 1), ("proc_arrive", 1),
                         COMMIT, ("deliver", 0), ("deliver", 1)])
        self.assertTrue(rep.ok)
        self.assertEqual(rep.violation, [])


if __name__ == "__main__":
    unittest.main(verbosity=2)
