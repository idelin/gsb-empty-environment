import math
import random
import unittest
from fractions import Fraction

from riskpath import (Graph, Label, SearchState, Status, dominates, evaluate,
                      solve)


def chain_graph(probs):
    """s -> n1 -> ... -> t 的链，每条边独立风险组，成功概率为 probs。"""
    g = Graph()
    for i, p in enumerate(probs):
        g.add_group(f"g{i}", p)
    nodes = ["s"] + [f"n{i}" for i in range(len(probs) - 1)] + ["t"]
    for i in range(len(probs)):
        g.add_edge(nodes[i], nodes[i + 1], 1, 1, f"g{i}")
    return g


class TestProbabilitySemantics(unittest.TestCase):
    def test_two_independent_edges_give_081(self):
        # 两条独立边各 0.9 -> 0.81（精确有理数）
        g = chain_graph([Fraction(9, 10), Fraction(9, 10)])
        res = solve(g, "s", "t", 10, 10)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.best_success, Fraction(81, 100))

    def test_probabilities_are_never_added(self):
        # 三条 0.9 边：0.9+0.9+0.9=2.7 显然非法；正确值是乘积 0.729
        g = chain_graph([Fraction(9, 10)] * 3)
        res = solve(g, "s", "t", 10, 10)
        self.assertEqual(res.best_success, Fraction(729, 1000))
        self.assertLess(res.best_success, 1)

    def test_shared_group_counted_once(self):
        # 同组两条边由同一失效事件控制：成功率是 0.9 而不是 0.81
        g = Graph()
        g.add_group("shared", Fraction(9, 10))
        g.add_edge("s", "a", 1, 1, "shared")
        g.add_edge("a", "t", 1, 1, "shared")
        res = solve(g, "s", "t", 10, 10)
        self.assertEqual(res.best_success, Fraction(9, 10))

    def test_shared_group_preferred_over_independent(self):
        # 上路共享组(0.9) vs 下路两个独立组(0.9*0.9)：同成本时应选上路
        g = Graph()
        g.add_group("shared", Fraction(9, 10))
        g.add_group("u1", Fraction(9, 10))
        g.add_group("u2", Fraction(9, 10))
        g.add_edge("s", "a", 1, 1, "shared")
        g.add_edge("a", "t", 1, 1, "shared")
        g.add_edge("s", "b", 1, 1, "u1")
        g.add_edge("b", "t", 1, 1, "u2")
        res = solve(g, "s", "t", 10, 10)
        self.assertEqual(res.best_success, Fraction(9, 10))
        self.assertEqual(res.path_nodes(res.best), ["s", "a", "t"])
        # 前沿应同时保留下路（组集合互不包含，不互相支配）
        self.assertEqual(len(res.frontier), 2)

    def test_zero_probability_boundary(self):
        # p=0：唯一路径必失败 -> INFEASIBLE
        g = chain_graph([Fraction(0), Fraction(1, 2)])
        res = solve(g, "s", "t", 10, 10)
        self.assertEqual(res.status, Status.INFEASIBLE)
        self.assertIsNone(res.best)
        self.assertGreaterEqual(res.state.pruned_zero_prob, 1)

    def test_zero_probability_avoided_when_alternative_exists(self):
        g = Graph()
        g.add_group("dead", 0)
        g.add_group("ok", Fraction(4, 5))
        g.add_edge("s", "a", 1, 1, "dead")
        g.add_edge("a", "t", 1, 1, "ok")
        g.add_edge("s", "t", 5, 5, "ok")
        res = solve(g, "s", "t", 10, 10)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.best_success, Fraction(4, 5))
        self.assertEqual(res.path_nodes(res.best), ["s", "t"])

    def test_one_probability_is_neutral(self):
        # p=1：nll 贡献为 0，不改变成功率
        g = chain_graph([Fraction(1), Fraction(3, 4), Fraction(1)])
        res = solve(g, "s", "t", 10, 10)
        self.assertEqual(res.best_success, Fraction(3, 4))
        self.assertEqual(res.best.nll, -math.log(0.75))

    def test_stable_representation_deep_product(self):
        # 1000 个 1/2 组：乘积 2^-1000 远超 float 下限，对数表示稳定
        g = chain_graph([Fraction(1, 2)] * 1000)
        res = solve(g, "s", "t", 2000, 2000)
        self.assertEqual(res.best_success, Fraction(1, 2 ** 1000))
        self.assertAlmostEqual(res.best.nll, 1000 * math.log(2), places=9)
        self.assertTrue(math.isfinite(res.best.nll))


class TestBudgetsAndStatus(unittest.TestCase):
    def make_two_path_graph(self):
        # 上路：便宜但慢；下路：快但贵
        g = Graph()
        g.add_group("g1", Fraction(9, 10))
        g.add_group("g2", Fraction(4, 5))
        g.add_edge("s", "a", 2, 9, "g1")
        g.add_edge("a", "t", 2, 9, "g1")
        g.add_edge("s", "t", 9, 2, "g2")
        return g

    def test_dual_budget_selects_feasible_path(self):
        g = self.make_two_path_graph()
        # 时延预算 10：只有下路可行
        res = solve(g, "s", "t", 10, 10)
        self.assertEqual(res.best_success, Fraction(4, 5))
        self.assertEqual(res.path_nodes(res.best), ["s", "t"])
        # 时延预算 20：上路成功率更高
        res = solve(g, "s", "t", 10, 20)
        self.assertEqual(res.best_success, Fraction(9, 10))

    def test_infeasible_when_budget_too_tight(self):
        g = self.make_two_path_graph()
        res = solve(g, "s", "t", 3, 3)  # 两条路都超预算
        self.assertEqual(res.status, Status.INFEASIBLE)
        self.assertEqual(res.frontier, [])
        self.assertIsNone(res.best)

    def test_undecided_then_resume(self):
        g = self.make_two_path_graph()
        state = SearchState(g, "s", "t", 10, 20)
        status = state.run(max_expansions=0)  # 立即截断
        self.assertEqual(status, Status.UNDECIDED)
        self.assertFalse(state.exhausted)
        # 恢复搜索直至耗尽
        status = state.run()
        self.assertEqual(status, Status.OPTIMAL)
        self.assertTrue(state.exhausted)
        result = state.result(status)
        self.assertEqual(result.best_success, Fraction(9, 10))
        # 与一次性求解一致
        self.assertEqual(solve(g, "s", "t", 10, 20).best_success, Fraction(9, 10))

    def test_undecided_keeps_best_so_far(self):
        g = self.make_two_path_graph()
        state = SearchState(g, "s", "t", 10, 20)
        status = state.run(max_expansions=1)
        self.assertEqual(status, Status.UNDECIDED)
        # 截断时仍返回当前已知前沿（可能为空或部分），状态可恢复
        self.assertIsInstance(state.frontier(), list)
        status = state.run()
        self.assertEqual(status, Status.OPTIMAL)

    def test_rational_costs_and_budgets(self):
        g = Graph()
        g.add_group("g1", Fraction(9, 10))
        g.add_edge("s", "a", Fraction(1, 3), Fraction(1, 2), "g1")
        g.add_edge("a", "t", Fraction(1, 3), Fraction(1, 2), "g1")
        res = solve(g, "s", "t", Fraction(2, 3), Fraction(1))
        self.assertEqual(res.status, Status.OPTIMAL)
        res = solve(g, "s", "t", Fraction(1, 2), Fraction(1))
        self.assertEqual(res.status, Status.INFEASIBLE)


class TestDominance(unittest.TestCase):
    def label(self, cost, delay, groups, visited, node="x"):
        return Label(node=node, cost=cost, delay=delay,
                     groups=frozenset(groups), visited=frozenset(visited),
                     nll=0.0, edges=())

    def test_subset_groups_dominates(self):
        a = self.label(1, 1, {"g1"}, {"s", "x"})
        b = self.label(1, 1, {"g1", "g2"}, {"s", "x"})
        self.assertTrue(dominates(a, b))
        self.assertFalse(dominates(b, a))

    def test_incomparable_group_sets_do_not_dominate(self):
        a = self.label(1, 1, {"g1"}, {"s", "x"})
        b = self.label(1, 1, {"g2"}, {"s", "x"})
        self.assertFalse(dominates(a, b))
        self.assertFalse(dominates(b, a))

    def test_visited_superset_not_dominated(self):
        # b 多访问了节点，未来可选延拓更少 -> 不能支配
        a = self.label(2, 2, {"g1"}, {"s", "x"})
        b = self.label(1, 1, {"g1"}, {"s", "x", "y"})
        self.assertFalse(dominates(b, a))

    def test_equal_labels_not_strict_dominance(self):
        a = self.label(1, 1, {"g1"}, {"s", "x"})
        self.assertFalse(dominates(a, a))


class TestEnumerationCrossCheck(unittest.TestCase):
    def make_random_dag(self, rng, n=7, group_pool=4):
        g = Graph()
        for k in range(group_pool):
            g.add_group(f"g{k}", Fraction(rng.randint(5, 10), 10))
        for i in range(n - 1):  # 主干链，保证所有节点存在且可达
            g.add_edge(f"n{i}", f"n{i + 1}", rng.randint(1, 9),
                       rng.randint(1, 9), f"g{rng.randrange(group_pool)}")
        for i in range(n):
            for j in range(i + 2, n):
                if rng.random() < 0.45:
                    g.add_edge(f"n{i}", f"n{j}", rng.randint(1, 9),
                               rng.randint(1, 9), f"g{rng.randrange(group_pool)}")
        return g

    def assert_search_matches_enumeration(self, g, max_cost, max_delay):
        res = solve(g, "n0", f"n{len(g.nodes) - 1}", max_cost, max_delay)
        enum = evaluate(g, "n0", f"n{len(g.nodes) - 1}", max_cost, max_delay)
        search_frontier = {x.signature() for x in res.frontier}
        enum_frontier = {(p.cost, p.delay, p.groups) for p in enum.frontier}
        self.assertEqual(search_frontier, enum_frontier)
        if enum.best is None:
            self.assertEqual(res.status, Status.INFEASIBLE)
            self.assertIsNone(res.best)
        else:
            self.assertEqual(res.status, Status.OPTIMAL)
            self.assertEqual(res.best_success, enum.best.success)
            self.assertTrue(math.isclose(res.best.nll, enum.best.nll))

    def test_random_dags_match_enumeration(self):
        rng = random.Random(20260926)
        for _ in range(30):
            g = self.make_random_dag(rng)
            self.assert_search_matches_enumeration(g, max_cost=25, max_delay=25)

    def test_random_dags_tight_budgets_match(self):
        rng = random.Random(7)
        for _ in range(30):
            g = self.make_random_dag(rng)
            self.assert_search_matches_enumeration(g, max_cost=8, max_delay=8)

    def test_cyclic_graph_simple_paths_match(self):
        # 含环图：简单路径语义下搜索与枚举一致
        g = Graph()
        for k in range(4):
            g.add_group(f"g{k}", Fraction(9 - k, 10))
        g.add_edge("n0", "n1", 1, 1, "g0")
        g.add_edge("n1", "n2", 1, 1, "g1")
        g.add_edge("n2", "n0", 1, 1, "g2")  # 回边成环
        g.add_edge("n1", "n3", 4, 1, "g1")
        g.add_edge("n2", "n3", 1, 4, "g3")
        g.add_edge("n0", "n3", 6, 6, "g2")
        res = solve(g, "n0", "n3", 10, 10)
        enum = evaluate(g, "n0", "n3", 10, 10)
        self.assertEqual({x.signature() for x in res.frontier},
                         {(p.cost, p.delay, p.groups) for p in enum.frontier})
        self.assertEqual(res.best_success, enum.best.success)
        # 所有枚举路径都是简单路径（无重复节点）
        for p in enum.all_paths:
            nodes = ["n0"] + [g.edges[i].dst for i in p.edges]
            self.assertEqual(len(nodes), len(set(nodes)))


if __name__ == "__main__":
    unittest.main()
