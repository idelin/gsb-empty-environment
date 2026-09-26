"""matching.py 的单元测试（python3.11 -m unittest -v）。"""

from fractions import Fraction
import unittest

from matching import (
    BUDGET_EXCEEDED,
    BUDGET_UNDECIDED,
    BUDGET_WITHIN,
    D_FORBIDDEN,
    D_SHARED_PERSON,
    D_SHARED_RESOURCE,
    D_UNKNOWN_EDGE,
    D_UNKNOWN_VERTEX,
    MandatoryConflictError,
    solve,
)


def assert_valid_matching(test_case, people, resources, result,
                          allowed_edges, mandatory):
    """通用不变量：匹配合法、资源不被两人占用、必选边全保留。"""
    test_case.assertEqual(len(result.matching), result.matched_count)
    test_case.assertEqual(set(result.matching), set(result.matching)
                          & set(people))
    # 一个资源最多匹配一个人
    used_resources = list(result.matching.values())
    test_case.assertEqual(len(used_resources), len(set(used_resources)))
    for pair in result.matching.items():
        test_case.assertIn(pair, allowed_edges)
    # 必选边绝不能被悄悄丢弃
    for person, resource in mandatory:
        test_case.assertEqual(result.matching[person], resource)
    test_case.assertEqual(result.deficit,
                          result.expected_count - result.matched_count)
    test_case.assertGreaterEqual(result.deficit, 0)


class MatchingTests(unittest.TestCase):

    # ---------- 基础：最大基数 ----------

    def test_simple_perfect_matching(self):
        r = solve(["a", "b"], ["x", "y"],
                  [("a", "x", 1), ("a", "y", 2), ("b", "y", 3)])
        self.assertEqual(r.matching, {"a": "x", "b": "y"})
        self.assertEqual(r.matched_count, 2)
        self.assertTrue(r.is_perfect)
        self.assertEqual(r.deficit, 0)
        self.assertEqual(r.total_cost, 4)

    def test_resource_never_matched_twice(self):
        # x 是唯一资源；无论多少人连它，只能匹配一个人
        r = solve(["a", "b", "c"], ["x"],
                  [("a", "x", 1), ("b", "x", 2), ("c", "x", 3)])
        self.assertEqual(r.matched_count, 1)
        self.assertEqual(r.deficit, 0)  # 较小侧只有 1 个资源
        self.assertEqual(r.unmatched_people, ["b", "c"])

    # ---------- 最小成本：在最大匹配中选最便宜的 ----------

    def test_minimum_cost_among_maximum_matchings(self):
        # 两个满匹配：{a-x,b-y}=10 与 {a-y,b-x}=4，必须选 4
        edges = [("a", "x", 1), ("a", "y", 3), ("b", "x", 1), ("b", "y", 9)]
        r = solve(["a", "b"], ["x", "y"], edges)
        self.assertEqual(r.matching, {"a": "y", "b": "x"})
        self.assertEqual(r.total_cost, 4)

    def test_cheaper_non_perfect_is_not_chosen(self):
        # 基数优先：便宜的单条边不能盖过更贵的满匹配
        edges = [("a", "x", 0), ("a", "y", 100), ("b", "x", 100)]
        r = solve(["a", "b"], ["x", "y"], edges)
        self.assertEqual(r.matched_count, 2)
        self.assertEqual(r.total_cost, 200)

    def test_rational_costs_exact(self):
        edges = [("a", "x", Fraction(1, 3)), ("a", "y", Fraction(1, 2)),
                 ("b", "x", Fraction(1, 6)), ("b", "y", Fraction(2, 3))]
        r = solve(["a", "b"], ["x", "y"], edges)
        self.assertEqual(r.matching, {"a": "y", "b": "x"})
        self.assertEqual(r.total_cost, Fraction(2, 3))
        self.assertNotIsInstance(r.total_cost, float)

    def test_rational_cost_from_string(self):
        r = solve(["a"], ["x"], [("a", "x", "1/3")])
        self.assertEqual(r.total_cost, Fraction(1, 3))

    def test_negative_preference_costs(self):
        edges = [("a", "x", -5), ("a", "y", -1), ("b", "x", -2),
                 ("b", "y", -3)]
        r = solve(["a", "b"], ["x", "y"], edges)
        self.assertEqual(r.matched_count, 2)
        # 最小费用：a-x(-5)+b-y(-3)=-8 优于 a-y(-1)+b-x(-2)=-3
        self.assertEqual(r.total_cost, -8)
        self.assertEqual(r.matching, {"a": "x", "b": "y"})

    # ---------- 禁止边 ----------

    def test_forbidden_edge_never_used(self):
        edges = [("a", "x", 1), ("a", "y", 10), ("b", "x", 1),
                 ("b", "y", 10)]
        r = solve(["a", "b"], ["x", "y"], edges,
                  forbidden={("a", "x"), ("b", "x")})
        self.assertEqual(set(r.matching.values()), {"y"})
        self.assertEqual(r.matched_count, 1)
        self.assertEqual(r.total_cost, 10)
        self.assertNotIn(("a", "x"), r.matching.items())

    # ---------- 必选边 ----------

    def test_mandatory_edge_forced_even_if_expensive(self):
        # 无约束最优 a-x+b-y=4；强制 b-x 后只能 a-y+b-x=4? 调整：
        edges = [("a", "x", 1), ("a", "y", 5), ("b", "x", 2),
                 ("b", "y", 3)]
        # 自由最优 a-x(1)+b-y(3)=4；强制 b-x 后为 a-y(5)+b-x(2)=7
        r = solve(["a", "b"], ["x", "y"], edges, mandatory={("b", "x")})
        self.assertEqual(r.mandatory_matching, {"b": "x"})
        self.assertEqual(r.matching, {"a": "y", "b": "x"})
        self.assertEqual(r.total_cost, 7)
        assert_valid_matching(self, {"a", "b"}, {"x", "y"}, r,
                              {("a", "x"), ("a", "y"), ("b", "x"),
                               ("b", "y")},
                              {("b", "x")})

    def test_mandatory_conflict_shared_person_diagnosed(self):
        edges = [("a", "x", 1), ("a", "y", 1), ("b", "x", 1)]
        with self.assertRaises(MandatoryConflictError) as ctx:
            solve(["a", "b"], ["x", "y"], edges,
                  mandatory={("a", "x"), ("a", "y")})
        kinds = {d.kind for d in ctx.exception.diagnostics}
        self.assertIn(D_SHARED_PERSON, kinds)

    def test_mandatory_conflict_shared_resource_diagnosed(self):
        edges = [("a", "x", 1), ("b", "x", 1)]
        with self.assertRaises(MandatoryConflictError) as ctx:
            solve(["a", "b"], ["x", "y"], edges,
                  mandatory={("a", "x"), ("b", "x")})
        kinds = {d.kind for d in ctx.exception.diagnostics}
        self.assertIn(D_SHARED_RESOURCE, kinds)

    def test_all_conflicts_reported_at_once(self):
        # 三条必选边制造两类冲突 + 一条禁止边，全部诊断必须同时给出
        edges = [("a", "x", 1), ("a", "y", 1), ("b", "x", 1),
                 ("b", "z", 1)]
        forbidden = {("b", "z")}
        with self.assertRaises(MandatoryConflictError) as ctx:
            solve(["a", "b"], ["x", "y", "z"], edges,
                  mandatory={("a", "x"), ("b", "x"), ("a", "y"),
                             ("b", "z")},
                  forbidden=forbidden)
        kinds = {d.kind for d in ctx.exception.diagnostics}
        self.assertIn(D_SHARED_PERSON, kinds)
        self.assertIn(D_SHARED_RESOURCE, kinds)
        self.assertIn(D_FORBIDDEN, kinds)

    def test_mandatory_forbidden_edge(self):
        with self.assertRaises(MandatoryConflictError) as ctx:
            solve(["a"], ["x"], [("a", "x", 1)],
                  mandatory={("a", "x")}, forbidden={("a", "x")})
        self.assertEqual(ctx.exception.diagnostics[0].kind, D_FORBIDDEN)

    def test_mandatory_unknown_edge(self):
        with self.assertRaises(MandatoryConflictError) as ctx:
            solve(["a"], ["x"], [], mandatory={("a", "x")})
        self.assertEqual(ctx.exception.diagnostics[0].kind, D_UNKNOWN_EDGE)

    def test_mandatory_unknown_vertex(self):
        with self.assertRaises(MandatoryConflictError) as ctx:
            solve(["a"], ["x"], [("a", "x", 1)],
                  mandatory={("a", "zzz")})
        self.assertEqual(ctx.exception.diagnostics[0].kind, D_UNKNOWN_VERTEX)

    # ---------- 部分匹配与缺额 ----------

    def test_partial_matching_with_deficit(self):
        # 两侧各 3 个但 c 完全无边、q 无人可连
        edges = [("a", "m", 1), ("a", "n", 10), ("b", "m", 2),
                 ("b", "n", 3)]
        r = solve(["a", "b", "c"], ["m", "n", "q"], edges)
        self.assertEqual(r.matched_count, 2)
        self.assertEqual(r.expected_count, 3)
        self.assertEqual(r.deficit, 1)
        self.assertFalse(r.is_perfect)
        self.assertEqual(r.unmatched_people, ["c"])
        self.assertEqual(r.unmatched_resources, ["q"])
        self.assertEqual(r.total_cost, 4)  # a-m(1) + b-n(3)

    def test_more_people_than_resources(self):
        edges = [("p1", "r", 5), ("p2", "r", 1), ("p3", "r", 9)]
        r = solve(["p1", "p2", "p3"], ["r"], edges)
        self.assertEqual(r.matched_count, 1)
        self.assertEqual(r.expected_count, 1)
        self.assertEqual(r.deficit, 0)
        self.assertEqual(r.total_cost, 1)  # 选最便宜的 p2
        self.assertEqual(r.matching, {"p2": "r"})

    def test_empty_graph(self):
        r = solve([], [], [])
        self.assertEqual(r.matching, {})
        self.assertEqual(r.matched_count, 0)
        self.assertEqual(r.deficit, 0)
        self.assertEqual(r.total_cost, 0)
        self.assertEqual(r.budget_status, BUDGET_UNDECIDED)

    # ---------- 预算三态 ----------

    def test_budget_within(self):
        r = solve(["a"], ["x"], [("a", "x", 3)], budget=3)
        self.assertEqual(r.budget_status, BUDGET_WITHIN)
        self.assertEqual(r.budget, Fraction(3))

    def test_budget_exceeded(self):
        r = solve(["a"], ["x"], [("a", "x", 3)], budget=2)
        self.assertEqual(r.budget_status, BUDGET_EXCEEDED)

    def test_budget_undecided_when_absent(self):
        r = solve(["a"], ["x"], [("a", "x", 3)])
        self.assertIsNone(r.budget)
        self.assertEqual(r.budget_status, BUDGET_UNDECIDED)

    def test_rational_budget_comparison_exact(self):
        r = solve(["a"], ["x"], [("a", "x", Fraction(1, 3))],
                  budget=Fraction(1, 2))
        self.assertEqual(r.budget_status, BUDGET_WITHIN)

    # ---------- 其他边界 ----------

    def test_duplicate_edge_takes_minimum_cost(self):
        r = solve(["a"], ["x"], [("a", "x", 9), ("a", "x", 2)])
        self.assertEqual(r.total_cost, 2)

    def test_unknown_vertex_in_edge_rejected(self):
        with self.assertRaises(ValueError):
            solve(["a"], ["x"], [("a", "yy", 1)])

    def test_mandatory_edge_blocks_cheaper_global_assignment(self):
        # 必选边可能降低总体基数：强制 a-x，b 只有 x 边 => b 无法匹配，
        # 但必选约束必须如实反映在结果中（缺额 1）。
        edges = [("a", "x", 1), ("a", "y", 100), ("b", "x", 1)]
        r = solve(["a", "b"], ["x", "y"], edges, mandatory={("a", "x")})
        self.assertEqual(r.matching.get("a"), "x")
        self.assertNotIn("b", r.matching)
        self.assertEqual(r.matched_count, 1)
        self.assertEqual(r.deficit, 1)


if __name__ == "__main__":
    unittest.main()
