import math
import random
import unittest
from fractions import Fraction

from riskpath import Graph, Status, dominates, pareto_frontier, run_search
from riskpath.enumerate import enumerate_simple_paths
from riskpath.search import Label


def chain_two_edges(shared_group=None):
    g = Graph()
    g.add_edge("s", "a", cost=1, delay=1, success=0.9, group=shared_group)
    g.add_edge("a", "t", cost=1, delay=1, success=0.9, group=shared_group)
    return g


class ProbabilitySemanticsTests(unittest.TestCase):
    def test_two_independent_edges_multiply_to_0_81(self):
        g = chain_two_edges()
        res = run_search(g, "s", "t", cost_budget=2, delay_budget=2)
        self.assertIs(res.status, Status.OPTIMAL)
        self.assertAlmostEqual(res.best.success, 0.81, places=12)
        self.assertAlmostEqual(math.exp(res.best.log_success), 0.81, places=12)

    def test_probabilities_are_never_added(self):
        # Two-edge path 0.9*0.9 = 0.81 must lose to a direct 0.95 edge.
        # An (invalid) additive rule would rank the two-edge path higher.
        g = chain_two_edges()
        g.add_edge("s", "t", cost=2, delay=2, success=0.95)
        res = run_search(g, "s", "t", cost_budget=2, delay_budget=2)
        self.assertEqual(res.best.path, ("s", "t"))
        self.assertAlmostEqual(res.best.success, 0.95, places=12)

    def test_shared_group_event_counted_once(self):
        g = chain_two_edges(shared_group="G")
        res = run_search(g, "s", "t", cost_budget=2, delay_budget=2)
        self.assertEqual(res.best.groups, frozenset({"G"}))
        self.assertAlmostEqual(res.best.success, 0.9, places=12)

    def test_zero_probability_edge_gives_zero_success_and_negative_inf(self):
        g = Graph()
        g.add_edge("s", "t", success=0.0)
        res = run_search(g, "s", "t", cost_budget=1, delay_budget=1)
        self.assertIs(res.status, Status.OPTIMAL)
        self.assertEqual(res.best.success, 0.0)
        self.assertEqual(res.best.log_success, float("-inf"))

        g2 = Graph()
        g2.add_edge("s", "a", success=0.0)
        g2.add_edge("a", "t", success=1.0)
        g2.add_edge("s", "t", success=0.5)
        r2 = run_search(g2, "s", "t", cost_budget=2, delay_budget=2)
        self.assertAlmostEqual(r2.best.success, 0.5)
        self.assertEqual(r2.best.path, ("s", "t"))

    def test_one_probability_edges_give_unit_success(self):
        g = Graph()
        g.add_edge("s", "a", success=1.0)
        g.add_edge("a", "t", success=1.0)
        res = run_search(g, "s", "t", cost_budget=2, delay_budget=2)
        self.assertEqual(res.best.log_success, 0.0)
        self.assertEqual(res.best.success, 1.0)

    def test_invalid_probability_rejected(self):
        g = Graph()
        with self.assertRaises(ValueError):
            g.add_edge("s", "t", success=1.1)

    def test_shared_group_with_conflicting_probability_rejected(self):
        g = Graph()
        g.add_edge("s", "a", success=0.9, group="G")
        with self.assertRaises(ValueError):
            g.add_edge("a", "t", success=0.8, group="G")


class DominanceTests(unittest.TestCase):
    @staticmethod
    def label(node, cost, delay, log_s, groups, visited):
        return Label(node, Fraction(cost), Fraction(delay), log_s,
                     frozenset(groups), frozenset(visited), tuple(visited))

    def test_dominance_requires_group_and_visited_subsets(self):
        a = self.label("n", 2, 3, -0.1, {"g1"}, {"s", "n"})
        b = self.label("n", 3, 4, -0.5, {"g1", "g2"}, {"s", "x", "n"})
        self.assertTrue(dominates(a, b))
        self.assertFalse(dominates(b, a))

    def test_extra_group_blocks_dominance(self):
        a = self.label("n", 2, 3, -0.1, {"g1", "g3"}, {"s", "n"})
        b = self.label("n", 3, 4, -0.5, {"g1", "g2"}, {"s", "x", "n"})
        self.assertFalse(dominates(a, b))
        self.assertFalse(dominates(b, a))

    def test_extra_visited_node_blocks_dominance(self):
        # a used an extra node: it may block a simple completion of b.
        a = self.label("n", 2, 3, -0.1, {"g1"}, {"s", "x", "n"})
        b = self.label("n", 3, 4, -0.5, {"g1", "g2"}, {"s", "n"})
        self.assertFalse(dominates(a, b))


class SearchStatusTests(unittest.TestCase):
    def test_infeasible_distinct_from_undecided(self):
        g = Graph()
        g.add_edge("s", "t", cost=3, delay=1, success=0.9)
        res = run_search(g, "s", "t", cost_budget=2, delay_budget=10)
        self.assertIs(res.status, Status.INFEASIBLE)
        self.assertIsNone(res.best)

    def test_delay_budget_alone_makes_infeasible(self):
        g = Graph()
        g.add_edge("s", "t", cost=1, delay=5, success=0.9)
        res = run_search(g, "s", "t", cost_budget=10, delay_budget=4)
        self.assertIs(res.status, Status.INFEASIBLE)

    def test_undecided_then_resume_to_optimal(self):
        g = chain_two_edges()
        stopped = run_search(g, "s", "t", cost_budget=2, delay_budget=2,
                             max_expansions=0)
        self.assertIs(stopped.status, Status.UNDECIDED)
        resumed = run_search(g, "s", "t", cost_budget=2, delay_budget=2,
                             max_expansions=100, state=stopped.state)
        self.assertIs(resumed.status, Status.OPTIMAL)
        self.assertAlmostEqual(resumed.best.success, 0.81, places=12)

    def test_resume_after_budget_relaxation(self):
        g = Graph()
        g.add_edge("s", "t", cost=2, delay=1, success=0.9)
        tight = run_search(g, "s", "t", cost_budget=1, delay_budget=10)
        self.assertIs(tight.status, Status.INFEASIBLE)
        resumed = run_search(g, "s", "t", cost_budget=2, delay_budget=10,
                             state=tight.state)
        self.assertIs(resumed.status, Status.OPTIMAL)
        self.assertAlmostEqual(resumed.best.success, 0.9)

    def test_source_equals_target_is_unit_success(self):
        res = run_search(Graph(), "s", "s", cost_budget=0, delay_budget=0)
        self.assertIs(res.status, Status.OPTIMAL)
        self.assertEqual(res.best.success, 1.0)
        self.assertEqual(res.best.path, ("s",))


class SimplePathAndRationalsTests(unittest.TestCase):
    def test_zero_cost_cycle_terminates_and_path_is_simple(self):
        g = Graph()
        g.add_edge("s", "a", cost=0, delay=0, success=1.0)
        g.add_edge("a", "b", cost=0, delay=0, success=1.0)
        g.add_edge("b", "a", cost=0, delay=0, success=1.0)  # zero-risk loop
        g.add_edge("a", "t", cost=0, delay=0, success=0.5)
        res = run_search(g, "s", "t", cost_budget=0, delay_budget=0)
        self.assertIs(res.status, Status.OPTIMAL)
        self.assertEqual(len(res.best.path), len(set(res.best.path)))
        self.assertAlmostEqual(res.best.success, 0.5)

    def test_cycle_cannot_increase_probability(self):
        g = Graph()
        g.add_edge("s", "a", success=0.8)
        g.add_edge("a", "t", success=0.8)
        g.add_edge("a", "b", success=1.0)
        g.add_edge("b", "a", success=1.0)
        res = run_search(g, "s", "t", cost_budget=10, delay_budget=10)
        self.assertEqual(res.best.path, ("s", "a", "t"))
        self.assertAlmostEqual(res.best.success, 0.64, places=12)

    def test_rational_budget_is_exact(self):
        g = Graph()
        g.add_edge("s", "a", cost=Fraction(1, 3), delay=0, success=1.0)
        g.add_edge("a", "t", cost=Fraction(1, 3), delay=0, success=1.0)
        tight = run_search(g, "s", "t",
                           cost_budget=Fraction(1, 2), delay_budget=1)
        self.assertIs(tight.status, Status.INFEASIBLE)
        fit = run_search(g, "s", "t",
                         cost_budget=Fraction(2, 3), delay_budget=1)
        self.assertIs(fit.status, Status.OPTIMAL)
        self.assertEqual(fit.best.cost, Fraction(2, 3))


class FrontierTests(unittest.TestCase):
    def test_pareto_frontier_keeps_incomparable_tradeoffs(self):
        g = Graph()
        g.add_edge("s", "t", cost=2, delay=6, success=0.80)   # cheap/slow
        g.add_edge("s", "t", cost=4, delay=4, success=0.90)   # balanced
        g.add_edge("s", "t", cost=6, delay=2, success=0.95)   # pricey/fast
        g.add_edge("s", "t", cost=5, delay=5, success=0.85)   # dominated
        res = run_search(g, "s", "t", cost_budget=10, delay_budget=10,
                         mode="frontier")
        self.assertIs(res.status, Status.OPTIMAL)
        self.assertEqual(len(res.frontier), 3)
        self.assertAlmostEqual(res.best.success, 0.95)
        from riskpath.search import frontier_dominates
        for i, a in enumerate(res.frontier):
            for j, b in enumerate(res.frontier):
                if i != j:
                    self.assertFalse(frontier_dominates(a, b))
                    self.assertFalse(frontier_dominates(b, a))


def _random_case(rng, n_nodes):
    g = Graph()
    group_names = [f"g{i}" for i in range(4)]
    group_p = {name: rng.choice([0.4, 0.6, 0.8, 0.9, 0.95, 1.0])
               for name in group_names}
    for u in range(n_nodes):
        for v in range(n_nodes):
            if u == v or rng.random() >= 0.35:
                continue
            if rng.random() < 0.5:
                g.add_edge(u, v, rng.randint(1, 5), rng.randint(1, 5),
                           success=rng.choice([0.5, 0.8, 0.99]))
            else:
                name = rng.choice(group_names)
                g.add_edge(u, v, rng.randint(1, 5), rng.randint(1, 5),
                           success=group_p[name], group=name)
    return g


def _frontier_keys(frontier):
    return sorted((l.cost, l.delay, round(l.log_success, 9))
                  for l in frontier)


class EnumerationCrossCheckTests(unittest.TestCase):
    def test_search_matches_full_enumeration_on_random_graphs(self):
        rng = random.Random(20260926)
        checked = 0
        for _ in range(120):
            n = rng.randint(2, 6)
            g = _random_case(rng, n)
            cost_budget = rng.randint(3, 12)
            delay_budget = rng.randint(3, 12)
            res = run_search(g, 0, n - 1,
                             cost_budget=cost_budget,
                             delay_budget=delay_budget,
                             mode="frontier")
            all_paths = enumerate_simple_paths(
                g, 0, n - 1,
                cost_budget=cost_budget, delay_budget=delay_budget)
            expected = pareto_frontier(all_paths)
            if expected:
                self.assertIs(res.status, Status.OPTIMAL)
                self.assertEqual(_frontier_keys(res.frontier),
                                 _frontier_keys(expected))
                best = max(expected, key=lambda l: l.log_success)
                self.assertTrue(math.isclose(res.best.log_success,
                                             best.log_success,
                                             rel_tol=1e-12, abs_tol=1e-12))
                opt = run_search(g, 0, n - 1,
                                 cost_budget=cost_budget,
                                 delay_budget=delay_budget)
                self.assertTrue(math.isclose(opt.best.log_success,
                                             best.log_success,
                                             rel_tol=1e-12, abs_tol=1e-12))
                checked += 1
            else:
                self.assertIs(res.status, Status.INFEASIBLE)
                self.assertEqual(res.frontier, [])
        self.assertGreater(checked, 30)


if __name__ == "__main__":
    unittest.main(verbosity=2)
