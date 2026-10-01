import unittest

from example_model import (ERR_STALE_READ, build_model)
from model import CLEAN, INVALID, VIOLATION, replay
from shrinker import Scenario, Shrinker


def bloated_stale_read():
    return Scenario(
        config={"primary_up": True, "cache_warm": True, "debug": True},
        actions=["noop", "write", "noop", "replicate", "noop", "read",
                 "noop", "noop"],
        faults=frozenset({"partition", "logging", "metrics"}),
    )


class ReplayTest(unittest.TestCase):
    def setUp(self):
        self.model = build_model()

    def test_replay_is_deterministic(self):
        scenario = bloated_stale_read()
        first = replay(self.model, scenario.config, scenario.actions, scenario.faults)
        second = replay(self.model, scenario.config, scenario.actions, scenario.faults)
        self.assertEqual((first.status, first.error, first.steps),
                         (second.status, second.error, second.steps))

    def test_precondition_failure_is_invalid_not_violation(self):
        # 'write' requires primary_up; defaults have it False.
        result = replay(self.model, {}, ["write"], frozenset())
        self.assertEqual(result.status, INVALID)
        self.assertIsNone(result.error)

    def test_unknown_action_is_invalid(self):
        result = replay(self.model, {}, ["fly_to_moon"], frozenset())
        self.assertEqual(result.status, INVALID)

    def test_clean_trace_replays_clean(self):
        result = replay(self.model, {"primary_up": True},
                        ["write", "replicate", "read"], frozenset())
        self.assertEqual(result.status, CLEAN)


class ShrinkerTest(unittest.TestCase):
    def setUp(self):
        self.model = build_model()

    def assert_one_minimal(self, scenario, error):
        """Brute-force check: no single deletion reproduces `error`."""
        for i in range(len(scenario.actions)):
            cand = Scenario(dict(scenario.config),
                            scenario.actions[:i] + scenario.actions[i + 1:],
                            scenario.faults)
            r = replay(self.model, cand.config, cand.actions, cand.faults)
            self.assertFalse(r.status == VIOLATION and r.error == error,
                             f"deleting action[{i}] still reproduces {error}")
        for key in scenario.config:
            cand_config = dict(scenario.config)
            del cand_config[key]
            r = replay(self.model, cand_config, scenario.actions, scenario.faults)
            self.assertFalse(r.status == VIOLATION and r.error == error,
                             f"resetting config[{key}] still reproduces {error}")
        for fault in scenario.faults:
            r = replay(self.model, scenario.config, scenario.actions,
                       scenario.faults - {fault})
            self.assertFalse(r.status == VIOLATION and r.error == error,
                             f"dropping fault {fault} still reproduces {error}")

    def test_refuses_non_violating_input(self):
        clean = Scenario({"primary_up": True}, ["write", "read"], frozenset())
        with self.assertRaises(ValueError):
            Shrinker(self.model).shrink(clean)
        invalid = Scenario({}, ["write"], frozenset())
        with self.assertRaises(ValueError):
            Shrinker(self.model).shrink(invalid)

    def test_bloated_scenario_shrinks_to_local_minimum(self):
        report = Shrinker(self.model, budget=1000).shrink(bloated_stale_read())
        self.assertTrue(report.locally_minimal)
        self.assertFalse(report.budget_exhausted)
        self.assertEqual(report.error, ERR_STALE_READ)
        self.assertEqual(report.scenario.config, {"primary_up": True})
        self.assertEqual(report.scenario.actions, ["write", "replicate", "read"])
        self.assertEqual(report.scenario.faults, frozenset({"partition"}))
        self.assertLessEqual(report.replays_used, report.budget)
        # Independent replay of the reported minimum triggers the same error.
        r = replay(self.model, report.scenario.config, report.scenario.actions,
                   report.scenario.faults)
        self.assertEqual((r.status, r.error), (VIOLATION, ERR_STALE_READ))
        self.assert_one_minimal(report.scenario, report.error)

    def test_deletion_breaking_precondition_is_rejected(self):
        # 'start' enables primary_up; deleting it breaks write/replicate's
        # preconditions (INVALID replay), so it must survive shrinking.
        scenario = Scenario({}, ["start", "write", "replicate", "read"],
                            frozenset({"partition"}))
        report = Shrinker(self.model, budget=1000).shrink(scenario)
        self.assertIn("start", report.scenario.actions)
        self.assertEqual(report.scenario.actions,
                         ["start", "write", "replicate", "read"])
        self.assert_one_minimal(report.scenario, report.error)

    def test_different_error_is_not_the_same_counterexample(self):
        # Target error is ERR_STALE_READ (fires at 'read').  Deleting 'read'
        # still fails, but with ERR_REPLICA_AHEAD at 'audit' -- a different
        # counterexample -- so 'read' must be kept.
        scenario = Scenario({"primary_up": True},
                            ["write", "replicate", "read", "audit"],
                            frozenset({"double_apply"}))
        report = Shrinker(self.model, budget=1000).shrink(scenario)
        self.assertEqual(report.error, ERR_STALE_READ)
        self.assertIn("read", report.scenario.actions)
        self.assertEqual(report.scenario.actions, ["write", "replicate", "read"])
        self.assertEqual(report.scenario.faults, frozenset({"double_apply"}))
        self.assert_one_minimal(report.scenario, report.error)

    def test_budget_exhaustion_claims_no_minimality(self):
        report = Shrinker(self.model, budget=3).shrink(bloated_stale_read())
        self.assertTrue(report.budget_exhausted)
        self.assertFalse(report.locally_minimal)
        self.assertLessEqual(report.replays_used, 3)
        self.assertIn("NOT confirmed", report.summary())
        # The partial result must still reproduce the same error.
        r = replay(self.model, report.scenario.config, report.scenario.actions,
                   report.scenario.faults)
        self.assertEqual((r.status, r.error), (VIOLATION, ERR_STALE_READ))

    def test_budget_of_one_only_confirms_the_replay(self):
        report = Shrinker(self.model, budget=1).shrink(bloated_stale_read())
        self.assertEqual(report.replays_used, 1)
        self.assertTrue(report.budget_exhausted)
        self.assertFalse(report.locally_minimal)

    def test_never_claims_global_minimality(self):
        report = Shrinker(self.model, budget=1000).shrink(bloated_stale_read())
        self.assertIn("NOT a claim of global minimality", report.summary())


if __name__ == "__main__":
    unittest.main()
