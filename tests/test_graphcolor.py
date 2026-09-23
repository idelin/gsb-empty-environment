import random
import unittest

from graphcolor import (
    BUDGET_EXHAUSTED,
    FEASIBLE,
    INFEASIBLE,
    Graph,
    Solver,
    canonical_set,
    enumerate_all_colorings,
    find_conflict_core,
    interval_overlap_graph,
    solve_lex_min,
    verify_coloring,
)


def triangle():
    g = Graph()
    g.add_edge("e_ab", "a", "b")
    g.add_edge("e_bc", "b", "c")
    g.add_edge("e_ca", "c", "a")
    return g


def clique(n, prefix="v"):
    g = Graph()
    names = [f"{prefix}{i}" for i in range(n)]
    for name in names:
        g.add_vertex(name)
    for i in range(n):
        for j in range(i + 1, n):
            g.add_edge(f"e_{names[i]}_{names[j]}", names[i], names[j])
    return g


class TestTriangle(unittest.TestCase):
    def test_two_colors_proven_infeasible(self):
        res = Solver(triangle(), 2, trace=False).solve()
        self.assertEqual(res.status, INFEASIBLE)
        self.assertIsNone(res.coloring)

    def test_three_colors_feasible_and_verified(self):
        g = triangle()
        res = Solver(g, 3, trace=False).solve()
        self.assertEqual(res.status, FEASIBLE)
        self.assertEqual(verify_coloring(g, res.coloring, 3), [])

    def test_lex_min_triangle(self):
        res = solve_lex_min(triangle(), 3)
        self.assertEqual(res.status, FEASIBLE)
        self.assertEqual(res.coloring, {"a": 0, "b": 1, "c": 2})


class TestPrecolorConflict(unittest.TestCase):
    def test_conflict_points_at_edge(self):
        g = triangle()
        res = Solver(g, 3, precoloring={"a": 0, "b": 0}, trace=False).solve()
        self.assertEqual(res.status, INFEASIBLE)
        self.assertIsNotNone(res.conflict)
        self.assertEqual(res.conflict.vertex, "b")
        reason = res.conflict.color_reasons[0]
        self.assertEqual(reason.kind, "edge")
        self.assertEqual(reason.edge_id, "e_ab")

    def test_precolor_respected_in_solution(self):
        g = triangle()
        res = Solver(g, 3, precoloring={"a": 2}, trace=False).solve()
        self.assertEqual(res.status, FEASIBLE)
        self.assertEqual(res.coloring["a"], 2)
        self.assertEqual(
            verify_coloring(g, res.coloring, 3, precoloring={"a": 2}), []
        )


class TestIsolatedVerticesAndCapacity(unittest.TestCase):
    def test_isolated_vertex_kept_and_capacity_zero_infeasible(self):
        g = Graph()
        g.add_vertex("lonely")
        self.assertEqual(g.vertices, ["lonely"])
        res = Solver(g, 1, capacities={0: 0}, trace=False).solve()
        self.assertEqual(res.status, INFEASIBLE)
        self.assertEqual(res.conflict.color_reasons[0].kind, "capacity")

    def test_isolated_vertices_share_capacity(self):
        g = Graph()
        g.add_vertex("x")
        g.add_vertex("y")
        res = Solver(g, 1, capacities={0: 1}, trace=False).solve()
        self.assertEqual(res.status, INFEASIBLE)

    def test_isolated_vertex_within_capacity_feasible(self):
        g = Graph()
        g.add_vertex("x")
        res = Solver(g, 1, capacities={0: 1}, trace=False).solve()
        self.assertEqual(res.status, FEASIBLE)
        self.assertEqual(verify_coloring(g, res.coloring, 1, {0: 1}), [])

    def test_capacity_limits_color_usage(self):
        g = clique(2)
        g.add_vertex("free1")
        g.add_vertex("free2")
        res = Solver(g, 2, capacities={0: 2, 1: 2}, trace=False).solve()
        self.assertEqual(res.status, FEASIBLE)
        self.assertEqual(verify_coloring(g, res.coloring, 2, {0: 2, 1: 2}), [])
        tight = Solver(g, 2, capacities={0: 1, 1: 1}, trace=False).solve()
        self.assertEqual(tight.status, INFEASIBLE)


class TestMultiedgesAndSelfLoops(unittest.TestCase):
    def test_multiedges_preserved(self):
        g = Graph()
        g.add_edge("m1", "a", "b")
        g.add_edge("m2", "a", "b")
        self.assertEqual(len(g.edges), 2)
        self.assertEqual({e.id for e in g.edges}, {"m1", "m2"})
        res = Solver(g, 2, trace=False).solve()
        self.assertEqual(res.status, FEASIBLE)
        self.assertEqual(verify_coloring(g, res.coloring, 2), [])
        self.assertEqual(Solver(g, 1, trace=False).solve().status, INFEASIBLE)

    def test_self_loop_infeasible_and_located(self):
        g = Graph()
        g.add_edge("loop", "a", "a")
        res = Solver(g, 3, trace=False).solve()
        self.assertEqual(res.status, INFEASIBLE)
        self.assertEqual(res.conflict.edge_ids(), ["loop"])
        self.assertTrue(verify_coloring(g, {"a": 0}, 3))


class TestBudget(unittest.TestCase):
    def test_budget_exhausted_is_not_infeasible(self):
        res = Solver(clique(5), 4, budget=1, trace=False).solve()
        self.assertEqual(res.status, BUDGET_EXHAUSTED)
        self.assertIsNone(res.coloring)

    def test_sufficient_budget_proves_infeasible(self):
        res = Solver(clique(5), 4, budget=10_000, trace=False).solve()
        self.assertEqual(res.status, INFEASIBLE)


class TestTrailRestoration(unittest.TestCase):
    def check_pristine(self, solver, k):
        for v in solver.vertices:
            self.assertEqual(solver.domains[v], set(range(k)))
        self.assertEqual(solver.assignment, {})
        self.assertEqual(solver.used, {c: 0 for c in range(k)})
        self.assertEqual(solver.reasons, {})

    def test_state_restored_after_infeasible(self):
        s = Solver(triangle(), 2, trace=False)
        self.assertEqual(s.solve().status, INFEASIBLE)
        self.check_pristine(s, 2)

    def test_state_restored_after_feasible(self):
        s = Solver(triangle(), 3, trace=False)
        self.assertEqual(s.solve().status, FEASIBLE)
        self.check_pristine(s, 3)

    def test_state_restored_after_budget_exhausted(self):
        s = Solver(clique(5), 4, budget=1, trace=False)
        self.assertEqual(s.solve().status, BUDGET_EXHAUSTED)
        self.check_pristine(s, 4)


class TestAssumptions(unittest.TestCase):
    def test_push_pop_restores_state(self):
        g = Graph()
        g.add_edge("e1", "a", "b")
        s = Solver(g, 2, trace=False)
        s.push_assumption("a", 0)
        res = s.solve()
        self.assertEqual(res.status, FEASIBLE)
        self.assertEqual(res.coloring["a"], 0)
        s.pop_assumption()
        self.assertEqual(s.domains["b"], {0, 1})
        self.assertEqual(s.assignment, {})
        s.push_assumption("a", 1)
        res2 = s.solve()
        self.assertEqual(res2.status, FEASIBLE)
        self.assertEqual(res2.coloring["a"], 1)

    def test_conflicting_assumption_leaves_state_intact(self):
        g = Graph()
        g.add_edge("e1", "a", "b")
        s = Solver(g, 1, trace=False)
        with self.assertRaises(Exception):
            s.push_assumption("a", 0)
        self.assertEqual(s.domains["b"], {0})
        self.assertEqual(s.assignment, {})


class TestTraceReproducible(unittest.TestCase):
    def test_identical_traces_across_runs(self):
        r1 = Solver(triangle(), 2).solve()
        r2 = Solver(triangle(), 2).solve()
        self.assertEqual(r1.trace, r2.trace)
        self.assertTrue(any(line.startswith("decide") for line in r1.trace))
        self.assertTrue(any("conflict" in line for line in r1.trace))


class TestVerifier(unittest.TestCase):
    def setUp(self):
        self.g = triangle()

    def test_accepts_valid(self):
        self.assertEqual(
            verify_coloring(self.g, {"a": 0, "b": 1, "c": 2}, 3), []
        )

    def test_rejects_edge_violation(self):
        problems = verify_coloring(self.g, {"a": 0, "b": 0, "c": 1}, 3)
        self.assertTrue(any("e_ab" in p for p in problems))

    def test_rejects_missing_and_out_of_range(self):
        problems = verify_coloring(self.g, {"a": 0, "b": 7}, 3)
        self.assertTrue(any("no color" in p for p in problems))
        self.assertTrue(any("outside" in p for p in problems))

    def test_rejects_capacity_and_precolor_violation(self):
        problems = verify_coloring(
            self.g, {"a": 0, "b": 1, "c": 2}, 3,
            capacities={0: 0}, precoloring={"a": 1},
        )
        self.assertTrue(any("capacity" in p for p in problems))
        self.assertTrue(any("precolor" in p for p in problems))


class TestEnumeratorCrossCheck(unittest.TestCase):
    def random_graph(self, seed, n=6, p=0.4):
        rng = random.Random(seed)
        g = Graph()
        for i in range(n):
            g.add_vertex(f"v{i}")
        eid = 0
        for i in range(n):
            for j in range(i + 1, n):
                if rng.random() < p:
                    g.add_edge(f"e{eid}", f"v{i}", f"v{j}")
                    eid += 1
        return g

    def test_pruning_keeps_all_legal_solutions(self):
        for seed in range(5):
            g = self.random_graph(seed)
            vertices = sorted(g.vertices)
            expected, _ = enumerate_all_colorings(g, 3)
            expected_set = {
                tuple(s[v] for v in vertices) for s in expected
            }
            solver_solutions, truncated = Solver(
                g, 3, symmetry_breaking=False, trace=False
            ).enumerate_solutions()
            self.assertFalse(truncated)
            got_set = {tuple(s[v] for v in vertices) for s in solver_solutions}
            self.assertEqual(got_set, expected_set)

    def test_symmetry_breaking_matches_canonical_classes(self):
        for seed in range(5):
            g = self.random_graph(seed)
            vertices = sorted(g.vertices)
            expected, _ = enumerate_all_colorings(g, 3)
            expected_classes = canonical_set(expected, vertices)
            on, truncated = Solver(
                g, 3, symmetry_breaking=True, trace=False
            ).enumerate_solutions()
            self.assertFalse(truncated)
            self.assertEqual(canonical_set(on, vertices), expected_classes)
            off, _ = Solver(g, 3, symmetry_breaking=False,
                            trace=False).enumerate_solutions()
            self.assertEqual(canonical_set(off, vertices), expected_classes)
            self.assertLessEqual(len(on), len(off))

    def test_cross_check_with_uniform_capacities(self):
        g = self.random_graph(7, n=5, p=0.5)
        caps = {0: 2, 1: 2, 2: 2}
        vertices = sorted(g.vertices)
        expected, _ = enumerate_all_colorings(g, 3, capacities=caps)
        got, _ = Solver(g, 3, capacities=caps, trace=False).enumerate_solutions()
        self.assertEqual(
            {tuple(s[v] for v in vertices) for s in got},
            {tuple(s[v] for v in vertices) for s in expected},
        )

    def test_lex_min_matches_brute_force_minimum(self):
        for seed in range(5):
            g = self.random_graph(seed)
            vertices = sorted(g.vertices)
            expected, _ = enumerate_all_colorings(g, 3)
            res = solve_lex_min(g, 3)
            if not expected:
                self.assertEqual(res.status, INFEASIBLE)
                continue
            best = min(tuple(s[v] for v in vertices) for s in expected)
            self.assertEqual(res.status, FEASIBLE)
            self.assertEqual(tuple(res.coloring[v] for v in vertices), best)


class TestConflictCore(unittest.TestCase):
    def test_k4_core_is_all_six_edges(self):
        core = find_conflict_core(clique(4), 3)
        self.assertIsNotNone(core)
        self.assertTrue(core.minimal_proven)
        self.assertEqual(len(core.edge_ids), 6)
        self.assertEqual(core.precolor_vertices, [])

    def test_core_excludes_irrelevant_constraints(self):
        g = clique(4)
        g.add_edge("e_extra", "x", "y")
        core = find_conflict_core(g, 3)
        self.assertTrue(core.minimal_proven)
        self.assertNotIn("e_extra", core.edge_ids)
        self.assertEqual(len(core.edge_ids), 6)

    def test_precolor_conflict_core(self):
        core = find_conflict_core(triangle(), 3, precoloring={"a": 0, "b": 0})
        self.assertTrue(core.minimal_proven)
        self.assertEqual(core.edge_ids, ["e_ab"])
        self.assertEqual(core.precolor_vertices, ["a", "b"])

    def test_capacity_conflict_core(self):
        g = Graph()
        g.add_vertex("x")
        g.add_vertex("y")
        core = find_conflict_core(g, 1, capacities={0: 1})
        self.assertTrue(core.minimal_proven)
        self.assertEqual(core.capacity_colors, [0])
        self.assertEqual(core.edge_ids, [])

    def test_budget_shortfall_not_claimed_minimal(self):
        core = find_conflict_core(
            triangle(), 3, precoloring={"a": 0, "b": 0}, budget=0
        )
        self.assertIsNotNone(core)
        self.assertFalse(core.minimal_proven)

    def test_feasible_instance_has_no_core(self):
        self.assertIsNone(find_conflict_core(triangle(), 3))


class TestIntervals(unittest.TestCase):
    def test_half_open_touching_no_conflict(self):
        g = interval_overlap_graph({"i1": (0, 1), "i2": (1, 2)})
        self.assertEqual(g.edges, [])

    def test_proper_overlap_conflicts(self):
        g = interval_overlap_graph(
            {"i1": (0, 1), "i2": (1, 2), "i3": (0.5, 1.5)}
        )
        self.assertEqual(
            {e.id for e in g.edges}, {"overlap[i1|i3]", "overlap[i2|i3]"}
        )
        self.assertEqual(Solver(g, 2, trace=False).solve().status, FEASIBLE)

    def test_mutual_overlap_triangle(self):
        g = interval_overlap_graph(
            {"a": (0, 3), "b": (1, 2), "c": (1.5, 4)}
        )
        self.assertEqual(Solver(g, 2, trace=False).solve().status, INFEASIBLE)
        self.assertEqual(Solver(g, 3, trace=False).solve().status, FEASIBLE)

    def test_invalid_interval_rejected(self):
        with self.assertRaises(ValueError):
            interval_overlap_graph({"bad": (2, 2)})


if __name__ == "__main__":
    unittest.main()
