import unittest

from temporal.enumeration import feasible_assignments, verify
from temporal.network import TemporalNetwork


class EnumerationTest(unittest.TestCase):
    def test_interval_example_agrees(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 2)
        net.add_interval("B", 3, 4)
        net.add_interval("C", 1, 3)
        report = verify(net, range(0, 5))
        self.assertTrue(report.consistent)
        self.assertGreater(report.num_assignments, 0)
        self.assertTrue(report.agrees)
        self.assertEqual(report.derived, (("A", "B"),))

    def test_causal_example_agrees(self):
        net = TemporalNetwork()
        net.add_interval("X", 0, 4)
        net.add_interval("Y", 0, 4)
        net.add_causal("X", "Y")
        report = verify(net, range(0, 5))
        self.assertTrue(report.agrees)
        self.assertEqual(report.derived, (("X", "Y"),))

    def test_mixed_example_agrees(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 2)
        net.add_interval("B", 3, 4)
        net.add_interval("C", 0, 9)
        net.add_causal("B", "C")
        report = verify(net, range(0, 10))
        self.assertTrue(report.agrees)
        self.assertEqual(report.derived, (("A", "B"), ("A", "C"), ("B", "C")))

    def test_enumeration_confirms_incomparability(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 2)
        net.add_interval("C", 1, 3)
        assigns = list(feasible_assignments(net, range(0, 4)))
        self.assertTrue(any(a["A"] < a["C"] for a in assigns))
        self.assertTrue(any(a["A"] >= a["C"] for a in assigns))

    def test_retraction_reverified_by_enumeration(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 2)
        net.add_interval("B", 3, 4)
        net.add_interval("C", 5, 6)
        self.assertTrue(verify(net, range(0, 7)).agrees)
        net.retract("interval:B")
        report = verify(net, range(0, 7))
        self.assertTrue(report.agrees)
        self.assertEqual(report.derived, (("A", "C"),))

    def test_contradictory_network_reported(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 2)
        net.add_interval("B", 3, 4)
        net.add_causal("B", "A")
        report = verify(net, range(0, 5))
        self.assertFalse(report.consistent)
        self.assertFalse(report.agrees)


if __name__ == "__main__":
    unittest.main()
