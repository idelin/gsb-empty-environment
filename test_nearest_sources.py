import unittest
from fractions import Fraction

from nearest_sources import Network, to_rational


def chain_network():
    """A--X--B, both edges weight 2, sources SA@A and SB@B."""
    net = Network()
    net.add_edge("e1", "A", "X", 2)
    net.add_edge("e2", "X", "B", 2)
    net.add_source("SA", "A")
    net.add_source("SB", "B")
    return net


class TestTieAssignment(unittest.TestCase):
    def test_equal_chain_midpoint_is_tied(self):
        assignment = chain_network().assign(10)
        self.assertEqual(assignment.owners_of("X"), frozenset({"SA", "SB"}))
        self.assertEqual(assignment.distance("X"), Fraction(2))
        self.assertEqual(assignment.owners_of("A"), frozenset({"SA"}))
        self.assertEqual(assignment.owners_of("B"), frozenset({"SB"}))
        self.assertEqual(assignment.distance("A"), Fraction(0))

    def test_unequal_chain_picks_nearest(self):
        net = Network()
        net.add_edge("e1", "A", "X", 2)
        net.add_edge("e2", "X", "B", 5)
        net.add_source("SA", "A")
        net.add_source("SB", "B")
        assignment = net.assign(0)
        self.assertEqual(assignment.owners_of("X"), frozenset({"SA"}))

    def test_rational_weights_are_exact(self):
        # 1/3 + 1/6 ties exactly with 1/2; float arithmetic would be fragile.
        net = Network()
        net.add_edge("p1", "S1v", "m", "1/3")
        net.add_edge("p2", "m", "T", "1/6")
        net.add_edge("q1", "S2v", "T", "1/2")
        net.add_source("S1", "S1v")
        net.add_source("S2", "S2v")
        assignment = net.assign(0)
        self.assertEqual(assignment.distance("T"), Fraction(1, 2))
        self.assertEqual(assignment.owners_of("T"), frozenset({"S1", "S2"}))


class TestDeactivation(unittest.TestCase):
    def test_deactivate_reassigns_and_reports_boundary(self):
        net = chain_network()
        diff = net.deactivate_and_diff("SA", 10)

        after = diff.after
        self.assertEqual(after.owners_of("X"), frozenset({"SB"}))
        self.assertEqual(after.distance("X"), Fraction(2))
        # A is still reachable through B: it joins SB's region at dist 4.
        self.assertEqual(after.owners_of("A"), frozenset({"SB"}))
        self.assertEqual(after.distance("A"), Fraction(4))

        changes = {c.vertex: (c.before, c.after) for c in diff.changes}
        self.assertEqual(
            changes["X"], (frozenset({"SA", "SB"}), frozenset({"SB"}))
        )
        self.assertEqual(changes["A"], (frozenset({"SA"}), frozenset({"SB"})))
        self.assertNotIn("B", changes)

        # X changed, B did not: e2 is the boundary of the changed region.
        boundary_ids = {e.id for e in diff.boundary_edges}
        self.assertEqual(boundary_ids, {"e2"})
        # Everything belongs to SB now, so no region borders remain.
        self.assertEqual(diff.owner_borders, [])

        proof = diff.proofs["X"]["SB"]
        self.assertEqual([h.edge_id for h in proof], ["e2"])
        self.assertEqual(proof[-1].cumulative, Fraction(2))
        proof_a = diff.proofs["A"]["SB"]  # source-to-vertex order: B->X->A
        self.assertEqual([h.edge_id for h in proof_a], ["e2", "e1"])
        self.assertEqual(proof_a[-1].cumulative, Fraction(4))

    def test_boundary_and_borders_with_three_sources(self):
        # P--Q--R--S--T (unit edges), sources at P, R, T; drop SR.
        net = Network()
        for eid, u, v in [("f1", "P", "Q"), ("f2", "Q", "R"),
                          ("f3", "R", "S"), ("f4", "S", "T")]:
            net.add_edge(eid, u, v, 1)
        net.add_source("SP", "P")
        net.add_source("SR", "R")
        net.add_source("ST", "T")
        before = net.assign(1)
        self.assertEqual(before.owners_of("Q"), frozenset({"SP", "SR"}))
        self.assertEqual(before.owners_of("S"), frozenset({"SR", "ST"}))

        diff = net.deactivate_and_diff("SR", 1)
        after = diff.after
        self.assertEqual(after.owners_of("Q"), frozenset({"SP"}))
        self.assertEqual(after.owners_of("R"), frozenset({"SP", "ST"}))
        self.assertEqual(after.owners_of("S"), frozenset({"ST"}))

        changed = {c.vertex for c in diff.changes}
        self.assertEqual(changed, {"Q", "R", "S"})
        # Exactly one endpoint changed: f1 (P stable, Q changed) and
        # f4 (S changed, T stable).
        self.assertEqual({e.id for e in diff.boundary_edges}, {"f1", "f4"})
        # New region borders: Q {SP} | R {SP, ST} and R {SP, ST} | S {ST}.
        self.assertEqual({e.id for e in diff.owner_borders}, {"f2", "f3"})
        for vertex in ("Q", "R", "S"):
            for sid, hops in diff.proofs[vertex].items():
                self.assertTrue(after.verify_proof(vertex, sid))

    def test_deactivation_is_right_open(self):
        net = chain_network()
        net.deactivate("SA", 10)
        self.assertEqual(net.assign(10).owners_of("X"), frozenset({"SB"}))
        self.assertEqual(
            net.assign(9).owners_of("X"), frozenset({"SA", "SB"})
        )

    def test_recompute_only_after_deactivation(self):
        net = chain_network()
        before = net.assign(3)
        net.deactivate("SB", 5)
        self.assertEqual(net.assign(3).owners, before.owners)  # history intact
        self.assertEqual(net.assign(7).owners_of("X"), frozenset({"SA"}))


class TestZeroWeightComponent(unittest.TestCase):
    def test_ties_fully_propagate_inside_zero_component(self):
        # S1 --3-- c1 --0-- c2 --3-- S2 : both component members tie.
        net = Network()
        net.add_edge("z1", "S1v", "c1", 3)
        net.add_edge("z0", "c1", "c2", 0)
        net.add_edge("z2", "c2", "S2v", 3)
        net.add_source("S1", "S1v")
        net.add_source("S2", "S2v")
        assignment = net.assign(0)
        for vertex in ("c1", "c2"):
            self.assertEqual(assignment.owners_of(vertex), frozenset({"S1", "S2"}))
            self.assertEqual(assignment.distance(vertex), Fraction(3))
            for sid in ("S1", "S2"):
                self.assertTrue(assignment.verify_proof(vertex, sid))

    def test_zero_component_with_internal_source(self):
        # Source sits inside the zero component: everyone is distance 0.
        net = Network()
        net.add_edge("z0", "c1", "c2", 0)
        net.add_edge("z1", "c2", "c3", 0)
        net.add_source("S", "c2")
        assignment = net.assign(0)
        for vertex in ("c1", "c2", "c3"):
            self.assertEqual(assignment.owners_of(vertex), frozenset({"S"}))
            self.assertEqual(assignment.distance(vertex), Fraction(0))
            self.assertTrue(assignment.verify_proof(vertex, "S"))

    def test_zero_chain_between_unequal_sources(self):
        # Zero component reached at different distances per side: the
        # nearer source wins for the whole component.
        net = Network()
        net.add_edge("a", "S1v", "c1", 1)
        net.add_edge("z", "c1", "c2", 0)
        net.add_edge("b", "c2", "S2v", 5)
        net.add_source("S1", "S1v")
        net.add_source("S2", "S2v")
        assignment = net.assign(0)
        self.assertEqual(assignment.owners_of("c2"), frozenset({"S1"}))
        self.assertEqual(assignment.distance("c2"), Fraction(1))


class TestGraphStructure(unittest.TestCase):
    def test_multiedges_kept_and_min_weight_used(self):
        net = Network()
        net.add_edge("p1", "S", "T", 5)
        net.add_edge("p2", "S", "T", 2)
        net.add_edge("p3", "S", "T", 9)
        net.add_source("src", "S")
        self.assertEqual(set(net.edges), {"p1", "p2", "p3"})  # all kept
        assignment = net.assign(0)
        self.assertEqual(assignment.distance("T"), Fraction(2))
        self.assertEqual([h.edge_id for h in assignment.proof("T", "src")], ["p2"])

    def test_isolated_vertices_preserved(self):
        net = Network()
        net.add_vertex("iso")
        net.add_vertex("hosted")
        net.add_source("src", "hosted")
        assignment = net.assign(0)
        self.assertIn("iso", assignment.owners)
        self.assertEqual(assignment.owners_of("iso"), frozenset())
        self.assertIsNone(assignment.distance("iso"))
        self.assertEqual(assignment.owners_of("hosted"), frozenset({"src"}))
        self.assertEqual(assignment.distance("hosted"), Fraction(0))

    def test_no_sources_means_no_owners(self):
        net = Network()
        net.add_edge("e", "u", "v", 1)
        assignment = net.assign(0)
        self.assertEqual(assignment.owners_of("u"), frozenset())
        self.assertIsNone(assignment.distance("v"))

    def test_duplicate_ids_rejected(self):
        net = Network()
        net.add_edge("e", "u", "v", 1)
        with self.assertRaises(ValueError):
            net.add_edge("e", "u", "v", 2)
        net.add_source("s", "u")
        with self.assertRaises(ValueError):
            net.add_source("s", "v")
        with self.assertRaises(ValueError):
            net.add_vertex("u")


class TestWeightsAndIntervals(unittest.TestCase):
    def test_negative_weight_rejected(self):
        net = Network()
        with self.assertRaises(ValueError):
            net.add_edge("e", "u", "v", -1)
        with self.assertRaises(ValueError):
            net.add_edge("e", "u", "v", "-1/3")

    def test_float_weight_rejected(self):
        net = Network()
        with self.assertRaises(TypeError):
            net.add_edge("e", "u", "v", 0.1)

    def test_to_rational(self):
        self.assertEqual(to_rational(3), Fraction(3))
        self.assertEqual(to_rational("3/2"), Fraction(3, 2))
        self.assertEqual(to_rational(Fraction(7, 5)), Fraction(7, 5))
        with self.assertRaises(TypeError):
            to_rational(1.5)
        with self.assertRaises(ValueError):
            to_rational("abc")

    def test_interval_left_closed_right_open(self):
        net = Network()
        net.add_edge("e", "S", "T", 1)
        net.add_source("src", "S", intervals=[(2, 5)])
        self.assertEqual(net.assign(2).owners_of("T"), frozenset({"src"}))
        self.assertEqual(net.assign(Fraction(9, 2)).owners_of("T"), frozenset({"src"}))
        self.assertEqual(net.assign(5).owners_of("T"), frozenset())
        self.assertEqual(net.assign(1).owners_of("T"), frozenset())

    def test_multiple_intervals_and_open_end(self):
        net = Network()
        net.add_edge("e", "S", "T", 1)
        net.add_source("src", "S", intervals=[(0, 2), (4, None)])
        self.assertEqual(net.assign(1).owners_of("T"), frozenset({"src"}))
        self.assertEqual(net.assign(3).owners_of("T"), frozenset())
        self.assertEqual(net.assign(100).owners_of("T"), frozenset({"src"}))

    def test_empty_interval_rejected(self):
        net = Network()
        with self.assertRaises(ValueError):
            net.add_source("s", "v", intervals=[(3, 3)])
        with self.assertRaises(ValueError):
            net.add_source("s", "v", intervals=[(4, 2)])


class TestProofs(unittest.TestCase):
    def test_all_proofs_verify_on_a_mixed_graph(self):
        net = Network()
        net.add_edge("e1", "SAv", "a", "3/2")
        net.add_edge("e2", "a", "b", "1/2")
        net.add_edge("e3", "SBv", "b", 2)
        net.add_edge("e4", "b", "c", 0)
        net.add_edge("e5", "c", "d", "7/4")
        net.add_edge("e6", "SAv", "d", 4)
        net.add_edge("e7", "d", "d", 1)  # self-loop must not break anything
        net.add_source("SA", "SAv")
        net.add_source("SB", "SBv")
        assignment = net.assign(0)
        for vertex, owners in assignment.owners.items():
            for sid in owners:
                self.assertTrue(
                    assignment.verify_proof(vertex, sid),
                    f"proof failed for {vertex} <- {sid}",
                )
        # b is tied (3/2 + 1/2 == 2), and the tie crosses the zero edge to c.
        self.assertEqual(assignment.owners_of("b"), frozenset({"SA", "SB"}))
        self.assertEqual(assignment.owners_of("c"), frozenset({"SA", "SB"}))

    def test_proof_rejects_non_owner(self):
        assignment = chain_network().assign(0)
        with self.assertRaises(KeyError):
            assignment.proof("A", "SB")
        with self.assertRaises(KeyError):
            assignment.proof("nope", "SA")


if __name__ == "__main__":
    unittest.main(verbosity=2)
