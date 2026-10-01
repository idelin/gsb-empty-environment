"""Unittest suite for the bounded exhaustive starvation checker."""
import unittest

from starvation import check_starvation

SPEC = {
    "nodes": [
        {"id": "g1", "parent": "root", "weight": 1, "quantum": 4, "cap": 8},
        {"id": "A", "parent": "g1", "weight": 1, "quantum": 3, "cap": 6, "leaf": True},
        {"id": "B", "parent": "g1", "weight": 1, "quantum": 3, "cap": 6, "leaf": True},
    ],
}


class StarvationCheckerTest(unittest.TestCase):
    def test_feasible_victim_is_not_starved(self):
        # Victim A streams cost-6 tasks (feasible: path cap min(8,6)=6).
        report = check_starvation(SPEC, streams={"B": [6, 3]},
                                  victim_leaf="A", victim_cost=6)
        self.assertTrue(report["complete"])
        self.assertIs(report["starvation"], False)
        self.assertEqual(report["reason"], "victim_served_on_every_pass_of_terminal_cycle")
        self.assertGreater(report["states_explored"], 0)
        self.assertGreater(report["cycle_rounds"], 0)

    def test_infeasible_victim_yields_shortest_counterexample(self):
        # Victim cost 7 exceeds path cap 6: it can never be served.
        report = check_starvation(SPEC, streams={"B": [3]},
                                  victim_leaf="A", victim_cost=7)
        self.assertTrue(report["complete"])
        self.assertIs(report["starvation"], True)
        self.assertGreater(report["cycle_rounds"], 0)
        # the lasso is exact: prefix + cycle is the shortest counterexample
        self.assertEqual(report["states_explored"],
                         report["prefix_rounds"] + report["cycle_rounds"])
        for round_events in report["cycle_events"]:
            for e in round_events:
                self.assertFalse(
                    e["event"] == "dequeue" and e["leaf"] == "A",
                    "victim served inside the alleged starvation cycle",
                )

    def test_state_bound_is_respected_and_reported(self):
        report = check_starvation(SPEC, streams={"B": [6, 3]},
                                  victim_leaf="A", victim_cost=6, state_bound=2)
        self.assertFalse(report["complete"])
        self.assertIsNone(report["starvation"])
        self.assertEqual(report["reason"], "state_bound_reached")
        self.assertEqual(report["states_explored"], 2)
        self.assertEqual(report["state_bound"], 2)

    def test_paused_free_config_terminates(self):
        # Every explored orbit must terminate in a cycle (finite state space).
        report = check_starvation(SPEC, streams={"B": [1]},
                                  victim_leaf="A", victim_cost=1)
        self.assertTrue(report["complete"])
        self.assertIs(report["starvation"], False)


if __name__ == "__main__":
    unittest.main()
