import unittest

from coverage_ledger import Ledger, query, reconcile, revision_impact
from coverage_ledger import intervals as iv


class DeltaTests(unittest.TestCase):
    def setUp(self):
        self.led = Ledger()
        for device in ("A", "B", "C"):
            self.led.add_membership(device, 0, None, rule_id=f"m-{device}")
        self.led.add_observation("A", 0, 4, observation_id="a")
        self.led.add_observation("B", 2, 6, observation_id="b")
        self.led.add_observation("C", 8, 10, observation_id="c")

    def test_gain_loss_conservation_and_inverse(self):
        self.led.publish_report("union", (0, 10), "union")
        self.led.publish_report("k2", (0, 10), "at_least_k", k=2)
        self.led.retract_observation("c", "C")            # lose [8,10)
        self.led.add_observation("C", 5, 7, observation_id="c2")  # add [5,7)
        self.led.add_observation("A", 6, 9, observation_id="a2")  # extend A

        for report_id in ("union", "k2"):
            published, current, delta = reconcile(self.led, report_id)
            self.assertTrue(delta.is_conserving)
            gained = iv.length(delta.gained_segments)
            lost = iv.length(delta.lost_segments)
            self.assertEqual(delta.old_length + gained - lost, delta.new_length)
            self.assertEqual(delta.difference, gained - lost)
            inverse = delta.inverse()
            self.assertTrue(inverse.is_conserving)
            self.assertEqual(inverse.new_length, published.known_coverage_length)
            self.assertEqual(inverse.gained_segments, delta.lost_segments)

    def test_delta_against_old_membership_rules(self):
        self.led.publish_report("R", (0, 10), "at_least_k", k=2)
        self.led.end_membership("C", 3, rule_id="m-C-2")  # C member only [0,3)
        # C online [8,10) stops counting; k=2 coverage loses nothing there
        # (only one device up at 8-10 anyway), but publish view stays intact
        published = query(self.led.report_snapshot("R"), (0, 10), "at_least_k", k=2)
        self.assertEqual(published.known_coverage_length, 2)  # overlap [2,4)
        _, current, delta = reconcile(self.led, "R")
        self.assertTrue(delta.is_conserving)

    def test_impact_is_local_and_clipped_to_membership(self):
        self.led.end_membership("C", 2, rule_id="m-C-2")
        retract = self.led.retract_observation("c", "C")
        impact = revision_impact(self.led, retract.seq, (0, 10))
        # raw change [8,10) but C was only a member until 2 -> effective zero
        self.assertEqual(impact["raw_changed_ranges"], [(8, 10)])
        self.assertEqual(impact["effective_changed_ranges"], [])
        self.assertEqual(impact["devices"], ["C"])


if __name__ == "__main__":
    unittest.main()
