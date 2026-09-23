import unittest
from fractions import Fraction

from nearest_sources import Graph


def build_chain(weight=Fraction(3, 2)):
    g = Graph()
    g.add_edge("e_AX", "A", "X", weight)
    g.add_edge("e_XB", "X", "B", weight)
    g.add_source("srcA", "A")
    g.add_source("srcB", "B")
    return g


def check_proof(test_case, graph, proof, expected_total):
    test_case.assertEqual(proof.total, expected_total)
    running = Fraction(0)
    cur = proof.source_vertex
    for step in proof.steps:
        edge = graph.edges[step.edge_id]
        test_case.assertEqual(step.from_vertex, cur)
        test_case.assertIn(step.to_vertex, (edge.u, edge.v))
        test_case.assertEqual(step.weight, edge.weight)
        running += edge.weight
        test_case.assertEqual(step.cumulative, running)
        cur = step.to_vertex
    test_case.assertEqual(cur, proof.target_vertex)
    test_case.assertEqual(running, proof.total)


class ChainTieTest(unittest.TestCase):
    def test_equal_edges_give_tie_at_middle(self):
        snap = build_chain().assignments(0)
        self.assertEqual(snap.owners_of("X"), frozenset({"srcA", "srcB"}))
        self.assertEqual(snap.distance_of("X"), Fraction(3, 2))
        self.assertEqual(snap.owners_of("A"), frozenset({"srcA"}))
        self.assertEqual(snap.owners_of("B"), frozenset({"srcB"}))

    def test_deactivate_a_reassigns_to_b(self):
        g = build_chain()
        report = g.deactivate_source("srcA", 10)
        self.assertEqual(set(report.changes), {"A", "X"})
        self.assertEqual(report.changes["X"].new_sources, frozenset({"srcB"}))
        self.assertEqual(report.changes["X"].new_distance, Fraction(3, 2))
        self.assertEqual(report.changes["A"].new_sources, frozenset({"srcB"}))
        self.assertEqual(report.changes["A"].new_distance, Fraction(3))
        self.assertEqual(report.boundary_edges, ["e_XB"])

    def test_deactivation_proofs(self):
        g = build_chain()
        report = g.deactivate_source("srcA", 10)
        check_proof(self, g, report.changes["A"].proof, Fraction(3))
        check_proof(self, g, report.changes["X"].proof, Fraction(3, 2))
        self.assertEqual(report.changes["A"].proof.source_vertex, "B")
        self.assertEqual([s.edge_id for s in report.changes["A"].proof.steps],
                         ["e_XB", "e_AX"])

    def test_before_snapshot_survives_deactivation(self):
        g = build_chain()
        report = g.deactivate_source("srcA", 10)
        self.assertEqual(report.before.owners_of("X"), frozenset({"srcA", "srcB"}))
        self.assertEqual(report.after.owners_of("X"), frozenset({"srcB"}))
        self.assertEqual(report.time, Fraction(10))

    def test_deactivate_inactive_source_rejected(self):
        g = build_chain()
        g.deactivate_source("srcA", 10)
        with self.assertRaises(ValueError):
            g.deactivate_source("srcA", 11)
        with self.assertRaises(KeyError):
            g.deactivate_source("nope", 0)


class ZeroWeightPropagationTest(unittest.TestCase):
    def build(self):
        g = Graph()
        g.add_edge("e1", "A", "X", 1)
        g.add_edge("e2", "B", "Y", 1)
        g.add_edge("e0", "X", "Y", 0)
        g.add_edge("e0b", "Y", "Z", 0)
        g.add_source("sA", "A")
        g.add_source("sB", "B")
        return g

    def test_tie_fully_propagates_within_zero_component(self):
        snap = self.build().assignments(0)
        for vid in ("X", "Y", "Z"):
            self.assertEqual(snap.owners_of(vid), frozenset({"sA", "sB"}), vid)
            self.assertEqual(snap.distance_of(vid), Fraction(1))

    def test_zero_component_after_deactivation(self):
        g = self.build()
        report = g.deactivate_source("sA", 5)
        for vid in ("X", "Y", "Z"):
            self.assertEqual(report.after.owners_of(vid), frozenset({"sB"}), vid)


class GraphStructureTest(unittest.TestCase):
    def test_multiedges_kept_and_min_weight_used(self):
        g = Graph()
        g.add_edge("m1", "P", "Q", 2)
        g.add_edge("m2", "P", "Q", 5)
        g.add_source("sP", "P")
        self.assertEqual(set(g.edges), {"m1", "m2"})
        snap = g.assignments(0)
        self.assertEqual(snap.distance_of("Q"), Fraction(2))
        self.assertEqual(snap.owners_of("Q"), frozenset({"sP"}))

    def test_isolated_vertex_kept_unreachable(self):
        g = Graph()
        g.add_vertex("ISO")
        g.add_edge("e", "P", "Q", 1)
        g.add_source("sP", "P")
        snap = g.assignments(0)
        self.assertIsNone(snap.distance_of("ISO"))
        self.assertEqual(snap.owners_of("ISO"), frozenset())
        self.assertIsNone(snap["ISO"].proof)

    def test_duplicate_ids_rejected(self):
        g = Graph()
        g.add_edge("e", "P", "Q", 1)
        g.add_source("s", "P")
        with self.assertRaises(ValueError):
            g.add_edge("e", "P", "Q", 2)
        with self.assertRaises(ValueError):
            g.add_source("s", "Q")
        with self.assertRaises(KeyError):
            g.add_source("s2", "MISSING")


class WeightValidationTest(unittest.TestCase):
    def test_negative_weight_rejected(self):
        g = Graph()
        with self.assertRaises(ValueError):
            g.add_edge("e", "P", "Q", Fraction(-1, 3))

    def test_non_rational_weight_rejected(self):
        g = Graph()
        with self.assertRaises(TypeError):
            g.add_edge("e", "P", "Q", 0.5)

    def test_zero_weight_allowed(self):
        g = Graph()
        g.add_edge("e", "P", "Q", 0)
        g.add_source("s", "P")
        self.assertEqual(g.assignments(0).distance_of("Q"), Fraction(0))


class IntervalTest(unittest.TestCase):
    def build(self):
        g = Graph()
        g.add_edge("e", "A", "B", 1)
        g.add_source("s1", "A", start=2, end=5)
        g.add_source("s2", "B")
        return g

    def test_interval_left_closed_right_open(self):
        g = self.build()
        self.assertEqual(g.assignments(1).owners_of("A"), frozenset({"s2"}))
        self.assertEqual(g.assignments(2).owners_of("A"), frozenset({"s1"}))
        self.assertEqual(g.assignments(4).owners_of("A"), frozenset({"s1"}))
        self.assertEqual(g.assignments(5).owners_of("A"), frozenset({"s2"}))

    def test_fractional_time(self):
        g = self.build()
        self.assertEqual(g.assignments(Fraction(9, 2)).owners_of("A"), frozenset({"s1"}))
        self.assertEqual(g.assignments(Fraction(5, 1)).owners_of("A"), frozenset({"s2"}))

    def test_invalid_interval_rejected(self):
        g = Graph()
        g.add_vertex("A")
        with self.assertRaises(ValueError):
            g.add_source("s", "A", start=5, end=5)
        with self.assertRaises(ValueError):
            g.add_source("s", "A", start=6, end=5)


class RationalDistanceTest(unittest.TestCase):
    def test_fractional_weights_exact(self):
        g = Graph()
        g.add_edge("e1", "S", "M", Fraction(1, 3))
        g.add_edge("e2", "M", "T", Fraction(1, 6))
        g.add_source("s", "S")
        snap = g.assignments(0)
        self.assertEqual(snap.distance_of("T"), Fraction(1, 2))
        check_proof(self, g, snap["T"].proof, Fraction(1, 2))


if __name__ == "__main__":
    unittest.main()
