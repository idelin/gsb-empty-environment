import unittest

from influence import Condition, InfluenceGraph


class PropagationTest(unittest.TestCase):
    def test_seed_without_facts_propagates_nowhere(self):
        g = InfluenceGraph()
        g.add_edge("e1", "a", "b", Condition(all_of={"f1"}))
        g.mark_changed("a")
        self.assertEqual(set(g.affected()), {"a"})

    def test_fact_activates_edge_incrementally(self):
        g = InfluenceGraph()
        g.add_edge("e1", "a", "b", Condition(all_of={"f1"}))
        g.mark_changed("a")
        self.assertNotIn("b", g.affected())
        g.add_fact("f1")
        self.assertIn("b", g.affected())

    def test_transitive_chain(self):
        g = InfluenceGraph()
        g.add_edge("e1", "a", "b", Condition(all_of={"f1"}))
        g.add_edge("e2", "b", "c", Condition(all_of={"f2"}))
        g.add_fact("f1", "f2")
        g.mark_changed("a")
        affected = g.affected()
        self.assertEqual(set(affected), {"a", "b", "c"})
        self.assertEqual(affected["c"].render(), "a -[e1]-> b -[e2]-> c")
        self.assertEqual(affected["c"].facts, frozenset({"f1", "f2"}))

    def test_shortest_explanation_chain_wins(self):
        g = InfluenceGraph()
        g.add_edge("long1", "a", "b")
        g.add_edge("long2", "b", "d")
        g.add_edge("short", "a", "d")
        g.mark_changed("a")
        explanation = g.affected()["d"]
        self.assertEqual(explanation.depth, 1)
        self.assertEqual(explanation.render(), "a -[short]-> d")

    def test_any_of_condition(self):
        g = InfluenceGraph()
        g.add_edge("e1", "a", "b", Condition(any_of={"x", "y"}))
        g.mark_changed("a")
        self.assertNotIn("b", g.affected())
        g.add_fact("y")
        self.assertEqual(g.affected()["b"].facts, frozenset({"y"}))


class FactRevocationTest(unittest.TestCase):
    def test_revoking_sole_evidence_removes_node_and_descendants(self):
        g = InfluenceGraph()
        g.add_edge("e1", "a", "b", Condition(all_of={"f1"}))
        g.add_edge("e2", "b", "c")
        g.add_fact("f1")
        g.mark_changed("a")
        self.assertEqual(set(g.affected()), {"a", "b", "c"})
        g.remove_fact("f1")
        self.assertEqual(set(g.affected()), {"a"})

    def test_alternative_evidence_keeps_node(self):
        g = InfluenceGraph()
        g.add_edge("e1", "a", "b", Condition(all_of={"f1"}))
        g.add_edge("e2", "a", "b", Condition(all_of={"f2"}))
        g.add_fact("f1", "f2")
        g.mark_changed("a")
        g.remove_fact("f1")
        affected = g.affected()
        self.assertIn("b", affected)
        self.assertEqual(affected["b"].render(), "a -[e2]-> b")
        self.assertEqual(affected["b"].facts, frozenset({"f2"}))

    def test_explanation_is_updated_not_appended(self):
        g = InfluenceGraph()
        g.add_edge("e1", "a", "b", Condition(all_of={"f1"}))
        g.add_edge("e2", "a", "b", Condition(all_of={"f2"}))
        g.add_fact("f1")
        g.mark_changed("a")
        self.assertEqual(g.affected()["b"].steps[0].edge, "e1")
        g.remove_fact("f1")
        g.add_fact("f2")
        explanation = g.affected()["b"]
        self.assertEqual(len(explanation.steps), 1)
        self.assertEqual(explanation.steps[0].edge, "e2")


class CycleTest(unittest.TestCase):
    def test_cycle_cannot_self_prove(self):
        g = InfluenceGraph()
        g.add_edge("ab", "a", "b")
        g.add_edge("ba", "b", "a")
        self.assertEqual(g.affected(), {})

    def test_cycle_grounded_by_seed(self):
        g = InfluenceGraph()
        g.add_edge("ab", "a", "b")
        g.add_edge("ba", "b", "a")
        g.mark_changed("a")
        affected = g.affected()
        self.assertEqual(set(affected), {"a", "b"})
        self.assertEqual(affected["a"].depth, 0)
        self.assertEqual(affected["b"].render(), "a -[ab]-> b")

    def test_detached_cycle_stays_unaffected(self):
        g = InfluenceGraph()
        g.add_edge("s1", "seed", "x")
        g.add_edge("c1", "p", "q")
        g.add_edge("c2", "q", "p")
        g.mark_changed("seed")
        self.assertEqual(set(g.affected()), {"seed", "x"})


class IntervalTest(unittest.TestCase):
    def test_interval_is_left_closed_right_open(self):
        g = InfluenceGraph()
        g.add_edge("e1", "a", "b", start=10, end=20)
        g.mark_changed("a")
        self.assertNotIn("b", g.affected(at=9))
        self.assertIn("b", g.affected(at=10))
        self.assertIn("b", g.affected(at=19))
        self.assertNotIn("b", g.affected(at=20))

    def test_unbounded_sides(self):
        g = InfluenceGraph()
        g.add_edge("e1", "a", "b", end=5)
        g.add_edge("e2", "a", "c", start=5)
        g.mark_changed("a")
        self.assertIn("b", g.affected(at=-100))
        self.assertNotIn("b", g.affected(at=5))
        self.assertIn("c", g.affected(at=5))

    def test_empty_interval_rejected(self):
        g = InfluenceGraph()
        with self.assertRaises(ValueError):
            g.add_edge("bad", "a", "b", start=5, end=5)


class StructureTest(unittest.TestCase):
    def test_isolated_vertices_preserved(self):
        g = InfluenceGraph()
        g.add_vertex("lonely")
        g.add_edge("e1", "a", "b")
        g.mark_changed("a")
        self.assertIn("lonely", g.vertices)
        self.assertEqual(g.isolated, frozenset({"lonely"}))
        self.assertNotIn("lonely", g.affected())

    def test_parallel_edges_preserved(self):
        g = InfluenceGraph()
        g.add_edge("p1", "a", "b", Condition(all_of={"f1"}))
        g.add_edge("p2", "a", "b", Condition(all_of={"f2"}))
        g.mark_changed("a")
        self.assertNotIn("b", g.affected())
        g.add_fact("f2")
        self.assertEqual(g.affected()["b"].steps[0].edge, "p2")

    def test_duplicate_edge_id_rejected(self):
        g = InfluenceGraph()
        g.add_edge("e1", "a", "b")
        with self.assertRaises(ValueError):
            g.add_edge("e1", "a", "c")

    def test_unmark_changed_shrinks_affected_set(self):
        g = InfluenceGraph()
        g.add_edge("e1", "a", "b")
        g.mark_changed("a")
        self.assertIn("b", g.affected())
        g.unmark_changed("a")
        self.assertEqual(g.affected(), {})


if __name__ == "__main__":
    unittest.main()
