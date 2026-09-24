import unittest

from graphcolor import (
    Graph, Problem, Solver, canonical_form, compare_with_solver, legal_colorings,
)


def small_problem():
    g = Graph()
    g.add_edge("m1", "u", "v")
    g.add_edge("m2", "u", "v")  # multi-edge
    g.add_vertex("w")           # isolated vertex
    return Problem(g, ("c1", "c2"), capacities={"c1": 2, "c2": 2})


class TestEnumerationCrossCheck(unittest.TestCase):
    def test_pruning_keeps_all_legal_solutions(self):
        p = small_problem()
        legal = {tuple(sorted(c.items())) for c in legal_colorings(p)}
        off = Solver(p, use_symmetry=False).find_all()
        found = {tuple(sorted(c.items())) for c in off.solutions}
        self.assertEqual(found, legal)

    def test_solution_classes_match(self):
        p = small_problem()
        rep = compare_with_solver(p)
        self.assertTrue(rep.ok, rep.notes)
        self.assertEqual(rep.num_legal, 4)
        self.assertEqual(rep.num_classes, 2)
        self.assertEqual(rep.broken_not_canonical, 0)

    def test_canonical_form(self):
        p = small_problem()
        # c2-first coloring canonicalizes to c1-first
        coloring = {"u": "c2", "v": "c1", "w": "c2"}
        self.assertEqual(canonical_form(p, coloring), ("c1", "c2", "c1"))

    def test_precoloring_distinguishes_colors(self):
        g = Graph()
        g.add_edge("e1", "a", "b")
        p = Problem(g, ("r", "g"), precoloring={"a": "r"})
        rep = compare_with_solver(p)
        self.assertTrue(rep.ok, rep.notes)
        self.assertEqual(rep.num_legal, 1)  # b must be g


if __name__ == "__main__":
    unittest.main()
