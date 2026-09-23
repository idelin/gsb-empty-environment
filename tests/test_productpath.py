import random
import unittest

from productpath import (
    ForbiddenRule,
    Graph,
    RuleSet,
    StaleCursorError,
    brute_force_paths,
    find_paths,
    shortest_path,
)


def make_graph():
    g = Graph()
    return g


class TestGraph(unittest.TestCase):
    def test_string_ids_isolated_vertices_and_parallel_edges(self):
        g = make_graph()
        g.add_vertex("lonely")
        g.add_edge("e1", "a", "b", 1.0, "x", "x")
        g.add_edge("e2", "a", "b", 2.0, "x", "x")  # parallel edge
        self.assertTrue(g.has_vertex("lonely"))
        self.assertEqual(g.out_edges("lonely"), ())
        self.assertEqual({e.eid for e in g.out_edges("a")}, {"e1", "e2"})
        self.assertEqual(g.edge("e1").dst, "b")

    def test_invalid_edges_rejected(self):
        g = make_graph()
        with self.assertRaises(ValueError):
            g.add_edge("neg", "a", "b", -1.0, "x", "x")
        g.add_edge("dup", "a", "b", 1.0, "x", "x")
        with self.assertRaises(ValueError):
            g.add_edge("dup", "a", "c", 1.0, "x", "x")
        with self.assertRaises(TypeError):
            g.add_edge(7, "a", "d", 1.0, "x", "x")


class TestShortestPath(unittest.TestCase):
    def test_three_level_tiebreak(self):
        # Equal cost 2: 1-hop "e9"/"e4" beat 2-hop "e1"+"e2" on hop count;
        # "e4" beats "e9" on edge-id order.
        g = make_graph()
        g.add_edge("e1", "s", "a", 1.0, "x", "x")
        g.add_edge("e2", "a", "t", 1.0, "x", "x")
        g.add_edge("e9", "s", "t", 2.0, "x", "x")
        g.add_edge("e4", "s", "t", 2.0, "x", "x")
        res = shortest_path(g, RuleSet(), "s", "x", "t")
        self.assertEqual(res.paths[0].edges, ("e4",))
        self.assertEqual(res.paths[0].cost, 2.0)

    def test_state_trajectory_output(self):
        g = make_graph()
        g.add_edge("e1", "s", "m", 1.0, "walk", "ride")
        g.add_edge("e2", "m", "t", 1.0, "ride", "walk")
        g.add_edge("e3", "m", "t", 0.5, "fly", "walk")  # unusable: state mismatch
        res = shortest_path(g, RuleSet(), "s", "walk", "t")
        path = res.paths[0]
        self.assertEqual(path.edges, ("e1", "e2"))
        self.assertEqual(path.states, ("walk", "ride", "walk"))
        self.assertEqual(
            path.product_states, (("s", "walk"), ("m", "ride"), ("t", "walk"))
        )

    def test_goal_state_filter(self):
        g = make_graph()
        g.add_edge("e1", "s", "t", 1.0, "x", "y")
        g.add_edge("e2", "s", "t", 2.0, "x", "x")
        res = shortest_path(g, RuleSet(), "s", "x", "t", goal_state="x")
        self.assertEqual(res.paths[0].edges, ("e2",))

    def test_start_equals_goal_is_zero_length_path(self):
        g = make_graph()
        g.add_vertex("s")
        res = shortest_path(g, RuleSet(), "s", "x", "s")
        self.assertEqual(len(res.paths), 1)
        self.assertEqual(res.paths[0].edges, ())
        self.assertEqual(res.paths[0].cost, 0.0)

    def test_unreachable_and_isolated_goal(self):
        g = make_graph()
        g.add_vertex("isolated")
        g.add_edge("e1", "s", "a", 1.0, "x", "x")
        res = shortest_path(g, RuleSet(), "s", "x", "isolated")
        self.assertEqual(res.paths, [])
        self.assertTrue(res.exhausted)


class TestForbiddenRules(unittest.TestCase):
    def test_shorter_illegal_path_yields_to_longer_legal_one(self):
        g = make_graph()
        g.add_edge("e1", "s", "t", 1.0, "x", "y")   # short but lands in forbidden state
        g.add_edge("e2", "s", "a", 1.0, "x", "x")
        g.add_edge("e3", "a", "t", 5.0, "x", "x")   # longer legal route
        rules = RuleSet()
        self.assertEqual(shortest_path(g, rules, "s", "x", "t").paths[0].cost, 1.0)
        rules.add_rule(ForbiddenRule("t", "y"))
        res = shortest_path(g, rules, "s", "x", "t")
        self.assertEqual(res.paths[0].edges, ("e2", "e3"))
        self.assertEqual(res.paths[0].cost, 6.0)

    def test_time_interval_is_left_closed_right_open(self):
        def build():
            g = make_graph()
            g.add_edge("e1", "s", "v", 1.0, "x", "y")  # arrive at v at t=1
            g.add_edge("e2", "v", "t", 1.0, "y", "x")
            return g

        rules = RuleSet()
        rules.add_rule(ForbiddenRule("v", "y", start=1.0, end=3.0))  # t=1 inside
        self.assertEqual(shortest_path(build(), rules, "s", "x", "t").paths, [])

        rules = RuleSet()
        rules.add_rule(ForbiddenRule("v", "y", start=0.0, end=1.0))  # t=1 == end: open
        self.assertEqual(len(shortest_path(build(), rules, "s", "x", "t").paths), 1)

        rules = RuleSet()
        rules.add_rule(ForbiddenRule("v", "y", start=2.0))  # t=1 < start
        self.assertEqual(len(shortest_path(build(), rules, "s", "x", "t").paths), 1)

    def test_start_forbidden_means_no_paths(self):
        g = make_graph()
        g.add_edge("e1", "s", "t", 1.0, "x", "x")
        rules = RuleSet()
        rules.add_rule(ForbiddenRule("s", "x"))
        res = shortest_path(g, rules, "s", "x", "t")
        self.assertEqual(res.paths, [])
        self.assertEqual(res.expansions, 0)

    def test_version_bumps_on_mutation(self):
        rules = RuleSet()
        v0 = rules.version
        rule = ForbiddenRule("a", "x")
        rules.add_rule(rule)
        self.assertEqual(rules.version, v0 + 1)
        rules.remove_rule(rule)
        self.assertEqual(rules.version, v0 + 2)


class TestProductSpaceSemantics(unittest.TestCase):
    def test_same_vertex_different_states_not_pruned_and_zero_cycle_terminates(self):
        # Zero-weight cycle s->k->s flips state x->y; t is only reachable
        # from s in state y, so the search must revisit s in a new state.
        g = make_graph()
        g.add_edge("e1", "s", "k", 0.0, "x", "x")
        g.add_edge("e2", "k", "s", 0.0, "x", "y")
        g.add_edge("e3", "s", "t", 1.0, "y", "y")
        res = shortest_path(g, RuleSet(), "s", "x", "t")
        path = res.paths[0]
        self.assertEqual(path.edges, ("e1", "e2", "e3"))
        self.assertEqual(path.vertices, ("s", "k", "s", "t"))
        self.assertEqual(path.states, ("x", "x", "y", "y"))
        self.assertEqual(path.cost, 1.0)

    def test_pure_zero_weight_cycle_terminates_without_path(self):
        g = make_graph()
        g.add_vertex("t")
        g.add_edge("e1", "a", "b", 0.0, "x", "y")
        g.add_edge("e2", "b", "a", 0.0, "y", "x")
        res = shortest_path(g, RuleSet(), "a", "x", "t")
        self.assertEqual(res.paths, [])
        self.assertTrue(res.exhausted)
        self.assertEqual(res.expansions, 2)


def multi_path_graph():
    g = make_graph()
    g.add_edge("e1", "s", "a", 1.0, "x", "x")
    g.add_edge("e2", "a", "t", 1.0, "x", "x")
    g.add_edge("e3", "s", "b", 1.0, "x", "x")
    g.add_edge("e4", "b", "t", 2.0, "x", "x")
    g.add_edge("e5", "s", "t", 5.0, "x", "x")
    g.add_edge("e6", "a", "b", 1.0, "x", "x")
    g.add_edge("e7", "b", "a", 1.0, "x", "x")
    return g


class TestTopK(unittest.TestCase):
    def test_topk_order_and_cursor_pagination(self):
        g = multi_path_graph()
        rules = RuleSet()
        page1 = find_paths(g, rules, "s", "x", "t", k=2)
        self.assertEqual([p.cost for p in page1.paths], [2.0, 3.0])
        page2 = find_paths(g, rules, "s", "x", "t", k=2, cursor=page1.cursor)
        all_at_once = find_paths(g, rules, "s", "x", "t", k=4)
        self.assertEqual(
            [p.key for p in page1.paths + page2.paths],
            [p.key for p in all_at_once.paths],
        )
        self.assertEqual(page1.cursor.version, rules.version)

    def test_cursor_rejected_after_rule_version_change(self):
        g = multi_path_graph()
        rules = RuleSet()
        page1 = find_paths(g, rules, "s", "x", "t", k=2)
        rules.add_rule(ForbiddenRule("a", "x"))
        with self.assertRaises(StaleCursorError):
            find_paths(g, rules, "s", "x", "t", k=2, cursor=page1.cursor)

    def test_rule_update_changes_results(self):
        g = multi_path_graph()
        rules = RuleSet()
        self.assertEqual(shortest_path(g, rules, "s", "x", "t").paths[0].edges, ("e1", "e2"))
        rules.add_rule(ForbiddenRule("a", "x"))
        res = shortest_path(g, rules, "s", "x", "t")
        self.assertEqual(res.paths[0].edges, ("e3", "e4"))  # cost 3, fewest hops

    def test_topk_enumerates_legal_paths_not_filtered_unconstrained(self):
        # Nine short paths all pass through forbidden (m, y); the two legal
        # paths are longer.  Filtering an unconstrained top-2 would find none.
        g = make_graph()
        for i in range(3):
            g.add_edge(f"p{i}", "s", "m", 1.0, "x", "y")
            g.add_edge(f"q{i}", "m", "t", 1.0, "y", "x")
        g.add_edge("a1", "s", "a", 2.0, "x", "x")
        g.add_edge("a2", "a", "t", 2.0, "x", "x")
        g.add_edge("b1", "s", "b", 3.0, "x", "x")
        g.add_edge("b2", "b", "t", 3.0, "x", "x")
        rules = RuleSet()
        rules.add_rule(ForbiddenRule("m", "y"))
        res = find_paths(g, rules, "s", "x", "t", k=2)
        self.assertEqual([p.edges for p in res.paths], [("a1", "a2"), ("b1", "b2")])

    def test_k_larger_than_available_paths(self):
        g = multi_path_graph()
        res = find_paths(g, RuleSet(), "s", "x", "t", k=100)
        self.assertEqual(len(res.paths), len(brute_force_paths(g, RuleSet(), "s", "x", "t")))
        self.assertTrue(res.exhausted)
        self.assertIsNotNone(res.cursor)


class TestBudget(unittest.TestCase):
    def test_budget_records_expansions_and_pending_states(self):
        g = multi_path_graph()
        res = find_paths(g, RuleSet(), "s", "x", "t", k=5, budget=1)
        self.assertEqual(res.expansions, 1)
        self.assertFalse(res.exhausted)
        self.assertGreater(len(res.pending), 0)
        for vertex, state in res.pending_states:
            self.assertIn(vertex, g.vertices)
            self.assertEqual(state, "x")
        full = find_paths(g, RuleSet(), "s", "x", "t", k=6)
        self.assertTrue(full.exhausted)
        self.assertEqual(full.pending, [])


class TestBruteForceCompleteness(unittest.TestCase):
    def test_fuzz_small_product_graphs_against_brute_force(self):
        rng = random.Random(20260923)
        for _ in range(60):
            g = make_graph()
            verts = [f"v{i}" for i in range(4)]
            states = ["s0", "s1"]
            for v in verts:
                g.add_vertex(v)
            for i in range(rng.randint(3, 8)):
                g.add_edge(
                    f"e{i}",
                    rng.choice(verts),
                    rng.choice(verts),
                    rng.choice([0.0, 1.0, 2.0, 3.0]),
                    rng.choice(states),
                    rng.choice(states),
                )
            rules = RuleSet()
            for _ in range(rng.randint(0, 3)):
                rules.add_rule(
                    ForbiddenRule(
                        rng.choice(verts),
                        rng.choice(states),
                        start=rng.choice([None, 0.0, 1.0, 2.0]),
                        end=rng.choice([None, 2.0, 3.0, 4.0]),
                    )
                )
            start = rng.choice(verts)
            goal = rng.choice(verts)
            start_state = rng.choice(states)
            goal_state = rng.choice([None] + states)
            res = find_paths(
                g, rules, start, start_state, goal, goal_state, k=64
            )
            brute = brute_force_paths(g, rules, start, start_state, goal, goal_state)
            self.assertEqual(
                [p.key for p in res.paths],
                [p.key for p in brute[:64]],
                msg=f"start={start} goal={goal} states={start_state}/{goal_state}",
            )
            if len(brute) <= 64:
                self.assertTrue(res.exhausted)


if __name__ == "__main__":
    unittest.main()
