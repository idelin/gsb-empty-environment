import unittest
import random
from fractions import Fraction

from bipartite_matching import Status, diagnose_mandatory, solve


class TestBasicMatching(unittest.TestCase):
    def test_perfect_min_cost(self):
        edges = [
            ("p1", "r1", 5), ("p1", "r2", 2),
            ("p2", "r1", 3), ("p2", "r2", 4),
        ]
        res = solve(["p1", "p2"], ["r1", "r2"], edges)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.cardinality, 2)
        self.assertEqual(res.cost, Fraction(5))  # p1-r2(2) + p2-r1(3)
        self.assertEqual(res.matching, {"p1": "r2", "p2": "r1"})
        self.assertEqual(res.deficit, 0)

    def test_cardinality_beats_cost(self):
        # 便宜的单边匹配(成本1) vs 昂贵的满匹配(成本100+100)：必须先满匹配。
        edges = [("p1", "r1", 1), ("p1", "r2", 100), ("p2", "r1", 100)]
        res = solve(["p1", "p2"], ["r1", "r2"], edges)
        self.assertEqual(res.cardinality, 2)
        self.assertEqual(res.cost, Fraction(200))
        self.assertEqual(res.status, Status.OPTIMAL)

    def test_negative_costs(self):
        edges = [("p1", "r1", -3), ("p1", "r2", 1), ("p2", "r1", 0), ("p2", "r2", 2)]
        res = solve(["p1", "p2"], ["r1", "r2"], edges)
        self.assertEqual(res.cardinality, 2)
        self.assertEqual(res.cost, Fraction(-1))  # p1-r1(-3) + p2-r2(2)

    def test_rational_costs(self):
        edges = [
            ("p1", "r1", Fraction(1, 3)), ("p1", "r2", "1/2"),
            ("p2", "r1", "2/3"), ("p2", "r2", Fraction(1, 6)),
        ]
        res = solve(["p1", "p2"], ["r1", "r2"], edges)
        self.assertEqual(res.cost, Fraction(1, 3) + Fraction(1, 6))
        self.assertEqual(res.matching, {"p1": "r1", "p2": "r2"})

    def test_resource_never_matched_twice(self):
        edges = [("p1", "r1", 1), ("p2", "r1", 1), ("p3", "r1", 1)]
        res = solve(["p1", "p2", "p3"], ["r1"], edges)
        self.assertEqual(res.cardinality, 1)
        self.assertEqual(len(set(res.matching.values())), len(res.matching))


class TestForbiddenEdges(unittest.TestCase):
    def test_forbidden_redirects(self):
        edges = [
            ("p1", "r1", 1), ("p1", "r2", 9),
            ("p2", "r1", 1), ("p2", "r2", 1),
        ]
        res = solve(["p1", "p2"], ["r1", "r2"], edges, forbidden=[("p1", "r1"), ("p2", "r2")])
        self.assertEqual(res.matching, {"p1": "r2", "p2": "r1"})
        self.assertEqual(res.cost, Fraction(10))

    def test_forbidden_blocks_perfect(self):
        edges = [("p1", "r1", 1), ("p2", "r1", 1)]
        res = solve(["p1", "p2"], ["r1", "r2"], edges, forbidden=[("p1", "r1")])
        self.assertEqual(res.status, Status.FEASIBLE)
        self.assertEqual(res.cardinality, 1)
        self.assertEqual(res.deficit, 1)


class TestMandatoryEdges(unittest.TestCase):
    def test_mandatory_forces_expensive_edge(self):
        edges = [
            ("p1", "r1", 1), ("p1", "r2", 100),
            ("p2", "r1", 1), ("p2", "r2", 1),
        ]
        res = solve(["p1", "p2"], ["r1", "r2"], edges, mandatory=[("p1", "r2")])
        self.assertEqual(res.matching["p1"], "r2")
        self.assertEqual(res.cost, Fraction(101))
        self.assertEqual(res.status, Status.OPTIMAL)

    def test_mandatory_not_listed_gets_zero_cost(self):
        edges = [("p1", "r1", 1), ("p2", "r1", 1), ("p2", "r2", 2)]
        res = solve(["p1", "p2"], ["r1", "r2"], edges, mandatory=[("p1", "r2")])
        self.assertEqual(res.matching["p1"], "r2")
        self.assertEqual(res.matching["p2"], "r1")
        self.assertEqual(res.cost, Fraction(1))

    def test_conflict_conflict_same_resource_not_silently_dropped(self):
        edges = [("p1", "r1", 1), ("p2", "r1", 1)]
        res = solve(["p1", "p2"], ["r1"], edges, mandatory=[("p1", "r1"), ("p2", "r1")])
        self.assertEqual(res.status, Status.INFEASIBLE)
        self.assertEqual(res.matching, {})
        self.assertTrue(any("r1" in d for d in res.diagnostics))
        self.assertTrue(any("不能匹配两人" in d for d in res.diagnostics))

    def test_conflict_same_person(self):
        edges = [("p1", "r1", 1), ("p1", "r2", 1)]
        res = solve(["p1"], ["r1", "r2"], edges, mandatory=[("p1", "r1"), ("p1", "r2")])
        self.assertEqual(res.status, Status.INFEASIBLE)
        self.assertTrue(any("一人只能匹配一个资源" in d for d in res.diagnostics))

    def test_mandatory_forbidden_conflict(self):
        edges = [("p1", "r1", 1)]
        res = solve(["p1"], ["r1"], edges, forbidden=[("p1", "r1")], mandatory=[("p1", "r1")])
        self.assertEqual(res.status, Status.INFEASIBLE)
        self.assertTrue(any("禁止边" in d for d in res.diagnostics))

    def test_mandatory_unknown_vertex(self):
        res = solve(["p1"], ["r1"], [], mandatory=[("p1", "rX")])
        self.assertEqual(res.status, Status.INFEASIBLE)
        self.assertTrue(any("右端点" in d for d in res.diagnostics))

    def test_diagnose_mandatory_clean(self):
        self.assertEqual(diagnose_mandatory(["p1"], ["r1"], [("p1", "r1")], []), [])


class TestPartialAndDeficit(unittest.TestCase):
    def test_more_people_than_resources_no_deficit(self):
        edges = [("p1", "r1", 4), ("p2", "r1", 2), ("p3", "r2", 1)]
        res = solve(["p1", "p2", "p3"], ["r1", "r2"], edges)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.cardinality, 2)
        self.assertEqual(res.full_size, 2)
        self.assertEqual(res.deficit, 0)  # 满匹配 = min(3, 2) = 2

    def test_true_deficit(self):
        edges = [("p1", "r1", 1)]
        res = solve(["p1", "p2", "p3"], ["r1", "r2"], edges)
        self.assertEqual(res.status, Status.FEASIBLE)
        self.assertEqual(res.cardinality, 1)
        self.assertEqual(res.deficit, 1)
        self.assertEqual(sorted(res.unmatched_people), ["p2", "p3"])
        self.assertEqual(res.unmatched_resources, ["r2"])

    def test_no_edges_at_all(self):
        res = solve(["p1", "p2"], ["r1"], [])
        self.assertEqual(res.status, Status.FEASIBLE)
        self.assertEqual(res.cardinality, 0)
        self.assertEqual(res.deficit, 1)

    def test_empty_instance(self):
        res = solve([], [], [])
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.cardinality, 0)


class TestBudget(unittest.TestCase):
    def test_budget_satisfied(self):
        edges = [("p1", "r1", 3)]
        res = solve(["p1"], ["r1"], edges, budget=5)
        self.assertEqual(res.status, Status.OPTIMAL)
        self.assertEqual(res.budget, Fraction(5))

    def test_budget_exceeded_is_undecided(self):
        edges = [("p1", "r1", 10)]
        res = solve(["p1"], ["r1"], edges, budget=5)
        self.assertEqual(res.status, Status.BUDGET_UNDECIDED)
        self.assertEqual(res.cost, Fraction(10))

    def test_budget_equal_is_ok(self):
        edges = [("p1", "r1", "7/2")]
        res = solve(["p1"], ["r1"], edges, budget=Fraction(7, 2))
        self.assertEqual(res.status, Status.OPTIMAL)

    def test_budget_undecided_beats_feasible_label(self):
        edges = [("p1", "r1", 10)]
        res = solve(["p1", "p2"], ["r1"], edges, budget=1)
        self.assertEqual(res.status, Status.BUDGET_UNDECIDED)
        self.assertEqual(res.deficit, 0)  # 满匹配 = min(2,1) = 1 已达到


class TestInputValidation(unittest.TestCase):
    def test_unknown_edge_vertex_raises(self):
        with self.assertRaises(ValueError):
            solve(["p1"], ["r1"], [("p1", "rX", 1)])

    def test_duplicate_edges_take_min_cost(self):
        edges = [("p1", "r1", 5), ("p1", "r1", 2)]
        res = solve(["p1"], ["r1"], edges)
        self.assertEqual(res.cost, Fraction(2))


class TestBruteForceCrossCheck(unittest.TestCase):
    def test_random_small_instances(self):
        rng = random.Random(20260926)
        for trial in range(200):
            n_p = rng.randint(0, 4)
            n_r = rng.randint(0, 4)
            people = [f"p{i}" for i in range(n_p)]
            resources = [f"r{j}" for j in range(n_r)]
            edges = []
            for p in people:
                for r in resources:
                    if rng.random() < 0.6:
                        edges.append((p, r, rng.randint(-5, 9)))
            res = solve(people, resources, edges)
            best_card, best_cost = -1, None
            choices = [
                [None] + [r for r in resources if any(e[0] == p and e[1] == r for e in edges)]
                for p in people
            ]

            def walk(i, used, card, cost):
                nonlocal best_card, best_cost
                if i == len(people):
                    if card > best_card or (card == best_card and (best_cost is None or cost < best_cost)):
                        best_card, best_cost = card, cost
                    return
                for opt in choices[i]:
                    if opt is None:
                        walk(i + 1, used, card, cost)
                    elif opt not in used:
                        c = min(e[2] for e in edges if e[0] == people[i] and e[1] == opt)
                        walk(i + 1, used | {opt}, card + 1, cost + c)

            walk(0, set(), 0, 0)
            self.assertEqual(res.cardinality, best_card, f"trial {trial}")
            self.assertEqual(res.cost, Fraction(best_cost), f"trial {trial}")
            self.assertEqual(len(set(res.matching.values())), len(res.matching))


if __name__ == "__main__":
    unittest.main(verbosity=2)
