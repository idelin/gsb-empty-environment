import unittest
from fractions import Fraction
from itertools import permutations

from temporal.network import ContradictionError, TemporalNetwork


def basic_net():
    net = TemporalNetwork()
    net.add_interval("A", 0, 2)
    net.add_interval("B", 3, 4)
    net.add_interval("C", 1, 3)
    return net


class NecessaryOrderTest(unittest.TestCase):
    def test_closed_intervals_force_precedence(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 2)
        net.add_interval("B", 3, 4)
        self.assertTrue(net.is_necessarily_before("A", "B"))
        self.assertFalse(net.is_necessarily_before("B", "A"))

    def test_touching_intervals_are_incomparable(self):
        # closed intervals may share an endpoint -> no strict precedence
        net = TemporalNetwork()
        net.add_interval("A", 0, 2)
        net.add_interval("B", 2, 4)
        self.assertTrue(net.incomparable("A", "B"))

    def test_overlapping_intervals_incomparable_no_midpoint_order(self):
        net = basic_net()
        # midpoint(A) = 1 < midpoint(C) = 2, yet no order may be claimed
        self.assertTrue(net.incomparable("A", "C"))
        self.assertIn(("A", "C"), net.incomparable_pairs())
        self.assertFalse(net.is_total())

    def test_evidence_points_at_original_constraints(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 2)
        net.add_interval("B", 3, 4)
        pairs = net.necessary_pairs()
        self.assertEqual(pairs[("A", "B")], frozenset({"interval:A", "interval:B"}))

    def test_transitive_reduction_is_compact(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 2)
        net.add_interval("B", 3, 4)
        net.add_interval("C", 5, 6)
        pairs = net.necessary_pairs()
        self.assertIn(("A", "C"), pairs)  # derived, but not compact
        red = net.transitive_reduction()
        self.assertEqual(set(red), {("A", "B"), ("B", "C")})
        self.assertEqual(net.layers(), [("A",), ("B",), ("C",)])

    def test_incomparability_is_not_transitive(self):
        net = basic_net()  # A=[0,2] C=[1,3] B=[3,4]
        self.assertTrue(net.incomparable("A", "C"))
        self.assertTrue(net.incomparable("C", "B"))
        self.assertTrue(net.is_necessarily_before("A", "B"))
        # groups must be genuine antichains, not incomparability classes
        for group in net.incomparable_groups():
            for x in group:
                for y in group:
                    if x != y:
                        self.assertTrue(net.incomparable(x, y))

    def test_layers_group_incomparable_events(self):
        net = basic_net()
        self.assertEqual(net.layers(), [("A", "C"), ("B",)])


class CausalTest(unittest.TestCase):
    def test_causal_edge_derives_precedence(self):
        net = TemporalNetwork()
        net.add_interval("X", 0, 4)
        net.add_interval("Y", 0, 4)
        net.add_causal("X", "Y")
        self.assertTrue(net.is_necessarily_before("X", "Y"))
        self.assertEqual(
            net.necessary_pairs()[("X", "Y")], frozenset({"causal:X->Y"})
        )

    def test_causal_and_intervals_compose(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 2)
        net.add_interval("B", 3, 4)
        net.add_interval("C", 0, 9)
        net.add_causal("B", "C")
        pairs = net.necessary_pairs()
        self.assertIn(("A", "B"), pairs)
        self.assertIn(("B", "C"), pairs)
        self.assertIn(("A", "C"), pairs)
        self.assertEqual(
            pairs[("A", "C")],
            frozenset({"interval:A", "interval:B", "causal:B->C"}),
        )
        red = net.transitive_reduction()
        self.assertEqual(set(red), {("A", "B"), ("B", "C")})


class ContradictionTest(unittest.TestCase):
    def test_cycle_with_evidence(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 2)
        net.add_interval("B", 3, 4)
        net.add_causal("B", "A")  # fights the interval-implied order
        self.assertFalse(net.is_consistent())
        cycles = net.contradictions()
        self.assertTrue(cycles)
        evidence = set().union(*(c.evidence for c in cycles))
        self.assertIn("causal:B->A", evidence)
        self.assertIn("interval:A", evidence)
        self.assertIn("interval:B", evidence)
        with self.assertRaises(ContradictionError):
            net.necessary_pairs()

    def test_impossible_causal_bounds_form_cycle(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 0)
        net.add_interval("B", 0, 0)
        net.add_causal("A", "B")  # needs t(A) < t(B) but both pinned to 0
        self.assertFalse(net.is_consistent())
        self.assertTrue(net.contradictions())

    def test_self_causal_edge_is_contradiction(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 1)
        net.add_causal("A", "A")
        self.assertFalse(net.is_consistent())


class RetractionTest(unittest.TestCase):
    def test_retraction_removes_unsupported_relations(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 2)
        net.add_interval("B", 3, 4)
        net.add_interval("C", 5, 6)
        self.assertIn(("A", "C"), net.necessary_pairs())
        net.retract("interval:B")
        pairs = net.necessary_pairs()
        self.assertNotIn(("A", "B"), pairs)
        self.assertNotIn(("B", "C"), pairs)
        self.assertIn(("A", "C"), pairs)  # still supported by A and C alone
        self.assertEqual(pairs[("A", "C")], frozenset({"interval:A", "interval:C"}))

    def test_retract_causal_edge(self):
        net = TemporalNetwork()
        net.add_interval("X", 0, 4)
        net.add_interval("Y", 0, 4)
        net.add_causal("X", "Y")
        self.assertTrue(net.is_necessarily_before("X", "Y"))
        net.retract("causal:X->Y")
        self.assertTrue(net.incomparable("X", "Y"))

    def test_retraction_restores_consistency(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 2)
        net.add_interval("B", 3, 4)
        net.add_causal("B", "A")
        self.assertFalse(net.is_consistent())
        net.retract("causal:B->A")
        self.assertTrue(net.is_consistent())
        self.assertTrue(net.is_necessarily_before("A", "B"))

    def test_correct_interval(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 2)
        net.add_interval("B", 3, 4)
        self.assertTrue(net.is_necessarily_before("A", "B"))
        net.correct_interval("A", 0, 5)
        self.assertTrue(net.incomparable("A", "B"))

    def test_retract_unknown_raises(self):
        net = TemporalNetwork()
        with self.assertRaises(KeyError):
            net.retract("interval:GHOST")


class OrderIndependenceTest(unittest.TestCase):
    def test_input_permutations_give_same_partial_order(self):
        intervals = [("A", 0, 2), ("B", 3, 4), ("C", 1, 3), ("D", 0, 1)]
        causals = [("A", "D")]
        snapshots = []
        for perm in permutations(range(len(intervals))):
            net = TemporalNetwork()
            for i in perm:
                e, lo, hi = intervals[i]
                net.add_interval(e, lo, hi)
            for c, e in causals:
                net.add_causal(c, e)
            snapshots.append(
                (
                    net.necessary_pairs(),
                    net.transitive_reduction(),
                    net.layers(),
                    net.witness(),
                )
            )
        for snap in snapshots[1:]:
            self.assertEqual(snapshots[0], snap)


class WitnessTest(unittest.TestCase):
    def test_witness_respects_intervals_and_causality(self):
        net = TemporalNetwork()
        net.add_interval("A", 0, 2)
        net.add_interval("B", 3, 4)
        net.add_interval("X", 0, 4)
        net.add_interval("Y", 0, 4)
        net.add_causal("X", "Y")
        w = net.witness()
        iv = net.intervals()
        for e, t in w.items():
            self.assertLessEqual(iv[e].lo, t)
            self.assertLessEqual(t, iv[e].hi)
            self.assertIsInstance(t, Fraction)
        self.assertLess(w["X"], w["Y"])

    def test_witness_respects_derived_order(self):
        net = basic_net()
        w = net.witness()
        for (a, b) in net.necessary_pairs():
            self.assertLess(w[a], w[b])


if __name__ == "__main__":
    unittest.main()
