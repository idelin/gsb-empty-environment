import unittest

from shrinker import replay, shrink
from wallet_model import WalletModel, initial_counterexample


class ReplayTests(unittest.TestCase):
    def setUp(self):
        self.model = WalletModel()
        self.config, self.trace, self.faults = initial_counterexample()

    def test_initial_counterexample_violates(self):
        result = replay(self.model, self.config, self.trace, self.faults)
        self.assertEqual(result.outcome, "violation")
        self.assertEqual(result.signature, "OVERDRAFT_BEYOND_LIMIT")

    def test_replay_is_deterministic(self):
        first = replay(self.model, self.config, self.trace, self.faults)
        second = replay(self.model, self.config, self.trace, self.faults)
        self.assertEqual(first, second)

    def test_precondition_failure_detected(self):
        result = replay(self.model, {"balance": 3}, [("withdraw", 5)], frozenset())
        self.assertEqual(result.outcome, "precondition_failed")
        self.assertEqual(result.step, 0)

    def test_clean_run(self):
        result = replay(self.model, {"balance": 3}, [("deposit", 5)], frozenset())
        self.assertEqual(result.outcome, "clean")


class ShrinkTests(unittest.TestCase):
    def setUp(self):
        self.model = WalletModel()
        self.config, self.trace, self.faults = initial_counterexample()
        self.result = shrink(
            self.model, self.config, self.trace, self.faults, max_replays=500
        )

    def test_local_minimum_found(self):
        self.assertEqual(self.result.status, "local_minimum")
        self.assertEqual(self.result.config, {"balance": 10, "overdraft": True})
        self.assertEqual(
            self.result.trace,
            [("set_limit", 8), ("withdraw", 3), ("withdraw", 9)],
        )
        self.assertEqual(self.result.faults, frozenset({"double_withdraw"}))

    def test_result_replays_to_same_violation(self):
        check = replay(
            self.model, self.result.config, self.result.trace, self.result.faults
        )
        self.assertEqual(check.outcome, "violation")
        self.assertEqual(check.signature, self.result.signature)

    def test_result_is_one_minimal(self):
        target = self.result.signature
        candidates = []
        for key in self.result.config:
            candidates.append(
                (
                    {k: v for k, v in self.result.config.items() if k != key},
                    self.result.trace,
                    self.result.faults,
                )
            )
        for index in range(len(self.result.trace)):
            candidates.append(
                (
                    self.result.config,
                    self.result.trace[:index] + self.result.trace[index + 1 :],
                    self.result.faults,
                )
            )
        for fault in self.result.faults:
            candidates.append(
                (self.result.config, self.result.trace, self.result.faults - {fault})
            )
        self.assertTrue(candidates)
        for config, trace, faults in candidates:
            outcome = replay(self.model, config, trace, faults)
            self.assertFalse(
                outcome.outcome == "violation" and outcome.signature == target,
                msg=f"deletion candidate still violates: {config} {trace} {faults}",
            )

    def test_irrelevant_config_entry_removed(self):
        self.assertNotIn("log_level", self.result.config)

    def test_different_error_is_not_same_counterexample(self):
        # Dropping "overdraft" still violates the property, but with a
        # different signature (NEGATIVE_BALANCE), so it must be rejected.
        config = {"balance": 5, "overdraft": True}
        trace = [("withdraw", 3)]
        faults = frozenset({"double_withdraw"})
        flipped = dict(config)
        del flipped["overdraft"]
        outcome = replay(self.model, flipped, trace, faults)
        self.assertEqual(outcome.outcome, "violation")
        self.assertEqual(outcome.signature, "NEGATIVE_BALANCE")
        result = shrink(self.model, config, trace, faults, max_replays=100)
        self.assertEqual(result.signature, "OVERDRAFT_BEYOND_LIMIT")
        self.assertEqual(result.config, {"balance": 5, "overdraft": True})

    def test_fault_set_shrunk_to_essential_fault(self):
        self.assertEqual(self.result.faults, frozenset({"double_withdraw"}))

    def test_precondition_breaking_deletion_rejected(self):
        # The withdraw is only enabled because of the preceding deposit;
        # deleting the deposit must not be accepted.
        config = {"balance": 5}
        trace = [("deposit", 10), ("withdraw", 12)]
        faults = frozenset({"double_withdraw"})
        result = shrink(self.model, config, trace, faults, max_replays=500)
        self.assertEqual(result.status, "local_minimum")
        self.assertEqual(result.trace, [("deposit", 10), ("withdraw", 12)])
        self.assertEqual(result.config, {"balance": 5})

    def test_budget_exhaustion_stays_undecided(self):
        result = shrink(
            self.model, self.config, self.trace, self.faults, max_replays=8
        )
        self.assertEqual(result.status, "undecided")
        self.assertLessEqual(result.replays_used, 8)
        check = replay(self.model, result.config, result.trace, result.faults)
        self.assertEqual(check.outcome, "violation")
        self.assertEqual(check.signature, result.signature)

    def test_non_violating_initial_input_rejected(self):
        with self.assertRaises(ValueError):
            shrink(self.model, {"balance": 1}, [("deposit", 1)], frozenset())

    def test_invalid_budget_rejected(self):
        with self.assertRaises(ValueError):
            shrink(
                self.model, self.config, self.trace, self.faults, max_replays=0
            )


if __name__ == "__main__":
    unittest.main()
