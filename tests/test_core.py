import unittest

from graphcolor import (
    ConstraintRef, Graph, Problem, Status, all_constraints, minimal_conflict_core,
)


def triangle2():
    g = Graph()
    g.add_edge("e1", "a", "b")
    g.add_edge("e2", "b", "c")
    g.add_edge("e3", "a", "c")
    return Problem(g, ("r", "g"))


class TestConflictCore(unittest.TestCase):
    def test_triangle_core_is_all_three_edges(self):
        res = minimal_conflict_core(triangle2())
        self.assertIs(res.status, Status.UNSAT)
        self.assertTrue(res.is_subset_minimal)
        self.assertEqual(set(res.core),
                         {ConstraintRef("edge", e) for e in ("e1", "e2", "e3")})

    def test_precolor_conflict_core(self):
        g = Graph()
        g.add_edge("e1", "a", "b")
        g.add_edge("e2", "b", "c")  # irrelevant edge must be excluded
        p = Problem(g, ("r", "g"), precoloring={"a": "r", "b": "r"})
        res = minimal_conflict_core(p)
        self.assertTrue(res.is_subset_minimal)
        self.assertEqual(set(res.core), {
            ConstraintRef("edge", "e1"),
            ConstraintRef("precolor", "a"),
            ConstraintRef("precolor", "b"),
        })

    def test_capacity_core_with_isolated_vertex(self):
        g = Graph()
        g.add_vertex("iso")
        p = Problem(g, ("r",), capacities={"r": 0})
        res = minimal_conflict_core(p)
        self.assertTrue(res.is_subset_minimal)
        self.assertEqual(res.core, [ConstraintRef("capacity", "r")])

    def test_feasible_problem_has_no_core(self):
        g = Graph()
        g.add_edge("e1", "a", "b")
        res = minimal_conflict_core(Problem(g, ("r", "g")))
        self.assertIs(res.status, Status.SAT)
        self.assertIsNone(res.core)

    def test_budget_exhaustion_forfeits_minimality(self):
        res = minimal_conflict_core(triangle2(), max_nodes_per_check=1)
        self.assertIs(res.status, Status.BUDGET_EXHAUSTED)
        self.assertFalse(res.is_subset_minimal)
        self.assertIsNone(res.core)

    def test_constraint_universe(self):
        p = triangle2()
        p.capacities["r"] = 5
        p.precoloring["a"] = "r"
        kinds = {r.kind for r in all_constraints(p)}
        self.assertEqual(kinds, {"edge", "precolor", "capacity"})


if __name__ == "__main__":
    unittest.main()
