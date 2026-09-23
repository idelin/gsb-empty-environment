import unittest

from impact_graph import ImpactGraph


class ImpactGraphTest(unittest.TestCase):
    def test_conditional_propagation_and_explanation(self):
        graph = ImpactGraph()
        for vertex in ("A", "B", "C", "isolated"):
            graph.add_vertex(vertex)
        graph.add_edge("e-a-b", "A", "B", "deployed")
        graph.add_edge("e-b-c", "B", "C", "tested")
        graph.add_change("A")

        self.assertEqual(graph.affected_vertices(), ("A", "B", "C")[:1])
        self.assertIsNone(graph.explain("isolated"))

        graph.update_facts(add=("deployed",))
        self.assertEqual(graph.affected_vertices(), ("A", "B"))

        graph.update_facts(add=("tested",))
        explanation = graph.explain("C")
        self.assertEqual(explanation.chain, ("A", "B", "C"))
        self.assertEqual(explanation.edges, ("e-a-b", "e-b-c"))
        self.assertEqual(explanation.facts, ("deployed", "tested"))

    def test_lost_evidence_removes_affected_vertex(self):
        graph = ImpactGraph()
        graph.add_vertex("A")
        graph.add_vertex("B")
        graph.add_vertex("C")
        graph.add_edge("ab", "A", "B", "enabled")
        graph.add_edge("bc", "B", "C")
        graph.add_change("A")
        graph.update_facts(add=("enabled",))

        self.assertEqual(graph.affected_vertices(), ("A", "B", "C"))
        graph.update_facts(remove=("enabled",))
        self.assertEqual(graph.affected_vertices(), ("A",))
        self.assertIsNone(graph.explain("B"))
        self.assertIsNone(graph.explain("C"))

    def test_alternate_evidence_preserves_vertex(self):
        graph = ImpactGraph()
        for vertex in ("A", "B", "X", "D"):
            graph.add_vertex(vertex)
        graph.add_change("A")
        graph.add_change("B")
        graph.add_edge("a-x", "A", "X", "first")
        graph.add_edge("b-x", "B", "X", "second")
        graph.add_edge("x-d", "X", "D")
        graph.update_facts(add=("first", "second"))

        self.assertEqual(graph.explain("X").chain, ("A", "X"))
        graph.update_facts(remove=("first",))
        explanation = graph.explain("X")
        self.assertEqual(explanation.chain, ("B", "X"))
        self.assertEqual(explanation.facts, ("second",))
        self.assertEqual(graph.affected_vertices(), ("A", "B", "D", "X"))

    def test_reachable_node_can_become_a_change_root(self):
        graph = ImpactGraph()
        for vertex in ("A", "B", "C"):
            graph.add_vertex(vertex)
        graph.add_edge("ab", "A", "B")
        graph.add_edge("bc", "B", "C")
        graph.add_change("A")
        self.assertEqual(graph.explain("B").chain, ("A", "B"))

        graph.add_change("B")
        self.assertEqual(graph.explain("B").chain, ("B",))
        self.assertEqual(graph.explain("C").chain, ("B", "C"))
        graph.remove_change("A")
        self.assertEqual(graph.affected_vertices(), ("B", "C"))

    def test_cycle_does_not_prove_itself(self):
        graph = ImpactGraph()
        graph.add_vertex("A")
        graph.add_vertex("B")
        graph.add_edge("ab", "A", "B", "enabled")
        graph.add_edge("ba", "B", "A")
        graph.add_change("A")
        graph.update_facts(add=("enabled",))
        self.assertEqual(graph.affected_vertices(), ("A", "B"))

        graph.remove_change("A")
        self.assertEqual(graph.affected_vertices(), ())
        self.assertIsNone(graph.explain("A"))
        self.assertIsNone(graph.explain("B"))

        graph.add_change("A")
        self.assertEqual(graph.affected_vertices(), ("A", "B"))
        graph.update_facts(remove=("enabled",))
        self.assertEqual(graph.affected_vertices(), ("A",))

    def test_half_open_time_interval(self):
        graph = ImpactGraph()
        graph.add_vertex("A")
        graph.add_vertex("B")
        graph.add_edge("timed", "A", "B", start=5, end=10)
        graph.add_change("A")

        for time_value, expected in ((4, ("A",)), (5, ("A", "B")), (9, ("A", "B")), (10, ("A",))):
            graph.set_time(time_value)
            with self.subTest(time=time_value):
                self.assertEqual(graph.affected_vertices(), expected)

    def test_compound_conditions_update_witness(self):
        graph = ImpactGraph()
        graph.add_vertex("A")
        graph.add_vertex("B")
        graph.add_vertex("C")
        graph.add_edge("choice", "A", "B", ("any", "x", ("all", "y", "z")))
        graph.add_edge("not-blocked", "A", "C", ("not", "blocked"))
        graph.add_change("A")

        self.assertEqual(graph.affected_vertices(), ("A", "C"))
        graph.update_facts(add=("x",))
        self.assertEqual(graph.explain("B").facts, ("x",))
        graph.update_facts(add=("y", "z"), remove=("x",))
        self.assertEqual(graph.explain("B").facts, ("y", "z"))
        graph.update_facts(add=("blocked",))
        self.assertEqual(graph.affected_vertices(), ("A", "B"))

    def test_multiple_edges_are_retained_and_tie_break_is_stable(self):
        graph = ImpactGraph()
        graph.add_vertex("A")
        graph.add_vertex("B")
        graph.add_edge("first", "A", "B", "one")
        graph.add_edge("second", "A", "B", "two")

        self.assertEqual(len(graph.edges), 2)
        with self.assertRaises(ValueError):
            graph.add_edge("first", "A", "B")

        graph.add_change("A")
        graph.update_facts(add=("two",))
        self.assertEqual(graph.explain("B").edges, ("second",))
        graph.update_facts(add=("one",))
        self.assertEqual(graph.explain("B").edges, ("first",))
        graph.update_facts(remove=("one",))
        self.assertEqual(graph.explain("B").edges, ("second",))

    def test_shortest_chain_switches_incrementally(self):
        graph = ImpactGraph()
        for vertex in ("A", "B", "C"):
            graph.add_vertex(vertex)
        graph.add_edge("via-b", "B", "C")
        graph.add_edge("a-b", "A", "B")
        graph.add_edge("direct", "A", "C", "shortcut")
        graph.add_change("A")

        self.assertEqual(graph.explain("C").chain, ("A", "B", "C"))
        graph.update_facts(add=("shortcut",))
        self.assertEqual(graph.explain("C").chain, ("A", "C"))
        graph.update_facts(remove=("shortcut",))
        self.assertEqual(graph.explain("C").chain, ("A", "B", "C"))


if __name__ == "__main__":
    unittest.main()
