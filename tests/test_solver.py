import unittest

from graphcolor import (
    EdgeReason, Graph, Problem, Solver, Status, coloring_key, legal_colorings,
    trace_digest, verify,
)


def triangle(colors, **kw):
    g = Graph()
    g.add_edge("e-ab", "a", "b")
    g.add_edge("e-bc", "b", "c")
    g.add_edge("e-ac", "a", "c")
    return Problem(g, tuple(colors), **kw)


class TestBasicSolving(unittest.TestCase):
    def test_triangle_two_colors_infeasible(self):
        r = Solver(triangle(["r", "g"])).solve()
        self.assertIs(r.status, Status.UNSAT)
        self.assertIsNone(r.coloring)

    def test_triangle_three_colors_feasible(self):
        p = triangle(["r", "g", "b"])
        r = Solver(p).solve()
        self.assertIs(r.status, Status.SAT)
        self.assertEqual(verify(p, r.coloring), [])

    def test_lexicographically_minimal(self):
        p = triangle(["r", "g", "b"])
        for use_symmetry in (True, False):
            r = Solver(p, use_symmetry=use_symmetry).solve()
            best = min(legal_colorings(p), key=lambda c: coloring_key(p, c))
            self.assertEqual(r.coloring, best)

    def test_budget_exhaustion_distinct_from_unsat(self):
        p = triangle(["r", "g"])
        r = Solver(p, max_nodes=1).solve()
        self.assertIs(r.status, Status.BUDGET_EXHAUSTED)
        self.assertIsNot(r.status, Status.UNSAT)
        # same problem, enough budget -> proven unsat
        self.assertIs(Solver(p).solve().status, Status.UNSAT)

    def test_self_loop_infeasible(self):
        g = Graph()
        g.add_edge("loop", "a", "a")
        r = Solver(Problem(g, ("r", "g"))).solve()
        self.assertIs(r.status, Status.UNSAT)


class TestPrecoloring(unittest.TestCase):
    def test_precoloring_conflict_locates_edge(self):
        p = triangle(["r", "g"], precoloring={"a": "r", "c": "r"})
        r = Solver(p).solve()
        self.assertIs(r.status, Status.UNSAT)
        self.assertIn("e-ac", r.conflict.note)
        self.assertEqual(r.nodes, 0)  # detected before search

    def test_precoloring_respected(self):
        p = triangle(["r", "g", "b"], precoloring={"b": "b"})
        r = Solver(p).solve()
        self.assertIs(r.status, Status.SAT)
        self.assertEqual(r.coloring["b"], "b")


class TestCapacities(unittest.TestCase):
    def test_isolated_vertex_counts_against_capacity(self):
        g = Graph()
        g.add_vertex("iso")
        p = Problem(g, ("r",), capacities={"r": 0})
        self.assertIs(Solver(p).solve().status, Status.UNSAT)

    def test_capacity_forces_spread(self):
        g = Graph()
        for v in ("v1", "v2", "v3", "v4"):
            g.add_vertex(v)
        p = Problem(g, ("r", "g"), capacities={"r": 2, "g": 2})
        r = Solver(p).solve()
        self.assertIs(r.status, Status.SAT)
        self.assertEqual(sorted(r.coloring.values()), ["g", "g", "r", "r"])

    def test_capacity_too_tight(self):
        g = Graph()
        for v in ("v1", "v2", "v3"):
            g.add_vertex(v)
        p = Problem(g, ("r", "g"), capacities={"r": 1, "g": 1})
        self.assertIs(Solver(p).solve().status, Status.UNSAT)


class TestReasonsAndBacktrack(unittest.TestCase):
    def test_reason_recorded_for_each_deletion(self):
        p = triangle(["r", "g", "b"])
        s = Solver(p)
        s.push_assumption("a", "r")
        # b lost "r" because of edge e-ab
        self.assertIsInstance(s._reasons["b"]["r"], EdgeReason)
        self.assertEqual(s._reasons["b"]["r"].edge_id, "e-ab")

    def test_backtrack_restores_domains_capacities_reasons(self):
        g = Graph()
        g.add_edge("e1", "a", "b")
        g.add_edge("e2", "b", "c")
        p = Problem(g, ("r", "g"), capacities={"r": 2, "g": 2})
        s = Solver(p)
        s.check_invariants()
        r = s.find_all()
        self.assertTrue(r.solutions)
        s.check_invariants()  # trail fully unwound after search
        self.assertEqual(s._assignment, {})
        self.assertEqual(s._remaining, {"r": 2, "g": 2})
        self.assertTrue(all(s._domains[v] == {"r", "g"} for v in "abc"))
        self.assertTrue(all(not s._reasons[v] for v in "abc"))


class TestAssumptions(unittest.TestCase):
    def test_push_solve_retract_solve(self):
        p = triangle(["r", "g", "b"])
        s = Solver(p)
        self.assertTrue(s.push_assumption("a", "b"))
        r = s.solve()
        self.assertEqual(r.coloring["a"], "b")
        s.pop_assumption()
        s.check_invariants()
        r = s.solve()
        self.assertEqual(r.coloring["a"], "r")  # lex-min again

    def test_conflicting_assumption_rejected_and_rolled_back(self):
        p = triangle(["r", "g", "b"], precoloring={"a": "r"})
        s = Solver(p)
        self.assertFalse(s.push_assumption("a", "g"))
        self.assertEqual(s.assumption_level, 0)
        s.check_invariants()

    def test_negative_assumption(self):
        p = triangle(["r", "g", "b"])
        s = Solver(p)
        self.assertTrue(s.push_assumption("a", "r", sign=False))
        r = s.solve()
        self.assertNotEqual(r.coloring["a"], "r")


class TestTrace(unittest.TestCase):
    def test_trace_reproducible(self):
        p = triangle(["r", "g", "b"])
        d1 = trace_digest(Solver(p).solve().trace)
        d2 = trace_digest(Solver(p).solve().trace)
        self.assertEqual(d1, d2)

    def test_trace_intervals_left_closed_right_open(self):
        p = triangle(["r", "g", "b"])
        r = Solver(p).solve()
        for e in r.trace:
            self.assertEqual(e.interval.start, e.step)
            self.assertIn(e.step, e.interval)
            self.assertNotIn(e.step + 1, e.interval)

    def test_trace_records_decisions_and_backtracks(self):
        r = Solver(triangle(["r", "g"])).solve()
        kinds = {e.kind for e in r.trace}
        self.assertIn("decide", kinds)
        self.assertIn("conflict", kinds)
        self.assertIn("backtrack", kinds)


class TestSymmetryBreaking(unittest.TestCase):
    def test_fewer_solutions_with_breaking(self):
        g = Graph()
        g.add_edge("e1", "a", "b")
        p = Problem(g, ("r", "g", "b"))
        on = Solver(p, use_symmetry=True).find_all()
        off = Solver(p, use_symmetry=False).find_all()
        self.assertEqual(len(off.solutions), 6)
        self.assertEqual(len(on.solutions), 1)  # all 6 form one symmetry class
        for sol in on.solutions:
            self.assertEqual(verify(p, sol), [])

    def test_colors_outside_classes_still_assignable(self):
        # "r" has a unique capacity, so only (g, b) forms a class;
        # "r" must remain a candidate everywhere.
        g = Graph()
        g.add_edge("e1", "a", "b")
        p = Problem(g, ("r", "g", "b"), capacities={"r": 1, "g": 2, "b": 2})
        r = Solver(p).solve()
        self.assertIs(r.status, Status.SAT)
        self.assertEqual(r.coloring["a"], "r")


if __name__ == "__main__":
    unittest.main()
