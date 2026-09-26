import unittest

from automata import samples
from automata.inclusion import Status


class TestSamples(unittest.TestCase):
    def test_all_samples_replay(self):
        results = {r["name"]: r for r in samples.run_all()}
        self.assertEqual(len(results), 9)
        self.assertEqual(results["overlapping_char_classes"]["ab"], "not_included")
        self.assertEqual(results["overlapping_char_classes"]["cex"], [ord("A")])
        self.assertEqual(results["epsilon_loops"]["ab"], "included")
        self.assertEqual(results["large_intervals"]["cex"], [2**19])
        self.assertEqual(results["empty_string"]["cex"], [])
        self.assertEqual(results["dead_state"]["cex"], [97, 98])
        self.assertEqual(results["unified_alphabets"]["cex"], [201])
        self.assertEqual(results["exponential_blowup"]["first"], "undecided")
        self.assertEqual(results["exponential_blowup"]["second"], "included")
        self.assertEqual(results["exponential_blowup"]["dfa_states"], 1024)
        self.assertEqual(results["pause_resume"]["cex"], [6, 0, 0])
        self.assertEqual(results["cache_invalidation"]["before"], "included")
        self.assertEqual(results["cache_invalidation"]["after"], "not_included")
        self.assertGreater(results["cache_invalidation"]["survived"], 0)


if __name__ == "__main__":
    unittest.main()
