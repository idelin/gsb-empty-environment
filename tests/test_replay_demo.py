"""The replayable demo scenario must run cleanly and stay deterministic."""

import os
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), "..", "examples"))

import replay_demo


class TestReplayDemo(unittest.TestCase):
    def test_scenario_replays_identically(self):
        first = replay_demo.run_scenario()
        second = replay_demo.run_scenario()
        self.assertEqual(first, second)
        self.assertEqual(first[-1], "SCENARIO OK")
        self.assertTrue(any("cache hits on replay: 2" in l for l in first))


if __name__ == "__main__":
    unittest.main()
