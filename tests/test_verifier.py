import unittest

from example_model import (ERR_STALE_READ, FAULT_UNIVERSE, build_model)
from model import VIOLATION, replay
from shrinker import Shrinker
from verifier import SAFE, UNDECIDED, VIOLATED, verify


class VerifierTest(unittest.TestCase):
    def setUp(self):
        self.model = build_model()

    def test_violated_with_replayable_counterexample(self):
        result = verify(self.model, fault_universe=FAULT_UNIVERSE,
                        max_states=10000, max_depth=32)
        self.assertEqual(result.status, VIOLATED)
        self.assertIsNotNone(result.counterexample)
        # The counterexample must independently replay to the same error.
        cx = result.counterexample
        r = replay(self.model, cx.config, cx.actions, cx.faults)
        self.assertEqual(r.status, VIOLATION)
        self.assertEqual(r.error, result.error)

    def test_shrinking_verifier_counterexample_preserves_error(self):
        result = verify(self.model, fault_universe=FAULT_UNIVERSE,
                        max_states=10000, max_depth=32)
        report = Shrinker(self.model, budget=1000).shrink(result.counterexample)
        self.assertTrue(report.locally_minimal)
        self.assertEqual(report.error, result.error)
        r = replay(self.model, report.scenario.config, report.scenario.actions,
                   report.scenario.faults)
        self.assertEqual((r.status, r.error), (VIOLATION, result.error))

    def test_safe_when_fault_universe_empty(self):
        result = verify(self.model, fault_universe=frozenset(),
                        max_states=10000, max_depth=32)
        self.assertEqual(result.status, SAFE)
        self.assertFalse(result.truncated)
        self.assertIsNone(result.counterexample)

    def test_undecided_when_state_budget_too_small(self):
        result = verify(self.model, fault_universe=FAULT_UNIVERSE,
                        max_states=1, max_depth=32)
        self.assertEqual(result.status, UNDECIDED)
        self.assertTrue(result.truncated)
        self.assertIsNone(result.counterexample)

    def test_undecided_when_depth_budget_too_small(self):
        result = verify(self.model, fault_universe=frozenset(),
                        max_states=10000, max_depth=1)
        self.assertEqual(result.status, UNDECIDED)
        self.assertTrue(result.truncated)

    def test_violation_at_initial_state_has_empty_trace(self):
        config = {"primary_up": True, "primary": 1, "acked": True,
                  "replicated": True, "last_read": 0}
        result = verify(self.model, config=config, fault_universe=frozenset(),
                        max_states=100, max_depth=8)
        self.assertEqual(result.status, VIOLATED)
        self.assertEqual(result.error, ERR_STALE_READ)
        self.assertEqual(result.counterexample.actions, [])
        r = replay(self.model, result.counterexample.config,
                   result.counterexample.actions, result.counterexample.faults)
        self.assertEqual((r.status, r.error), (VIOLATION, ERR_STALE_READ))


if __name__ == "__main__":
    unittest.main()
