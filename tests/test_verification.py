import unittest
from dataclasses import replace

from secure_sum_sim.protocol import RoundStatus, run_round
from secure_sum_sim.sharing import DEFAULT_PRIME
from secure_sum_sim.verification import check_conservation, explore_states

INPUTS = {"A": 10, "B": 20, "C": 30, "D": 40, "E": 50}


class TestConservation(unittest.TestCase):
    def test_conservation_holds(self):
        result, _ = run_round("r1", INPUTS, 3, DEFAULT_PRIME, dropped={"E"})
        self.assertTrue(check_conservation(result, INPUTS, DEFAULT_PRIME))

    def test_conservation_detects_tampering(self):
        result, _ = run_round("r1", INPUTS, 3, DEFAULT_PRIME)
        fake = replace(result, value=(result.value + 1) % DEFAULT_PRIME)
        self.assertFalse(check_conservation(fake, INPUTS, DEFAULT_PRIME))

    def test_incomplete_round_cannot_be_verified(self):
        result, _ = run_round("r1", INPUTS, 5, DEFAULT_PRIME, dropped={"D", "E"})
        self.assertIs(result.status, RoundStatus.INCOMPLETE)
        with self.assertRaises(ValueError):
            check_conservation(result, INPUTS, DEFAULT_PRIME)


class TestStateExploration(unittest.TestCase):
    def test_explore_states_no_violations(self):
        stats = explore_states(100, seed=7)
        self.assertEqual(stats["violations"], 0)
        self.assertEqual(stats["complete"] + stats["incomplete"], stats["trials"])
        self.assertEqual(stats["conservation_ok"], stats["complete"])
        self.assertGreater(stats["complete"], 0)
        self.assertGreater(stats["incomplete"], 0)


if __name__ == "__main__":
    unittest.main()
