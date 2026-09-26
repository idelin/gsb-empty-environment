import itertools
import random
import unittest

from audit_coverage import solve


def brute_force(scenarios, checks, budget, hours_budget=None):
    """独立实现的纯暴力枚举，用于交叉验证最优性。"""
    k_req = {s["id"]: s.get("k", 1) for s in scenarios}
    weight = {s["id"]: s.get("weight", 1) for s in scenarios}
    best = None
    for r in range(len(checks) + 1):
        for combo in itertools.combinations(checks, r):
            total_cost = sum(c.get("cost", 0) for c in combo)
            total_hours = sum(c.get("hours", 0) for c in combo)
            if total_cost > budget:
                continue
            if hours_budget is not None and total_hours > hours_budget:
                continue
            fams = {s["id"]: set() for s in scenarios}
            for c in combo:
                for sid in c.get("covers", []):
                    fams[sid].add(c["family"])
            covered_w = sum(weight[sid] for sid, fs in fams.items()
                            if len(fs) >= k_req[sid])
            ids = tuple(sorted(c["id"] for c in combo))
            key = (covered_w, -total_cost, tuple(chr(255) + i for i in ()))
            cand = (covered_w, total_cost, ids)
            if best is None or (covered_w, -total_cost) > (best[0], -best[1]) \
                    or (covered_w == best[0] and total_cost == best[1]
                        and ids < best[2]):
                best = cand
    return best


class AcceptanceBoundaryTest(unittest.TestCase):
    """验收边界：R 要求 2 个家族；a、b 同家族都覆盖 R 不算充分；
    再加入另一家族的 c 才满足。"""

    def _checks(self):
        return [
            {"id": "a", "family": "F1", "cost": 1, "hours": 1, "covers": ["R"]},
            {"id": "b", "family": "F1", "cost": 1, "hours": 1, "covers": ["R"]},
            {"id": "c", "family": "F2", "cost": 1, "hours": 1, "covers": ["R"]},
        ]

    def test_same_family_not_sufficient(self):
        scenarios = [{"id": "R", "weight": 10, "k": 2}]
        # 只有同家族的 a、b 可用：R 永远不可能充分覆盖
        res = solve(scenarios, self._checks()[:2], budget=100)
        self.assertFalse(res["scenarios"][0]["covered"])
        self.assertEqual(res["objective"]["covered_weight"], 0)

    def test_same_family_no_inflation_when_selected(self):
        # 迫使 a 被选中（覆盖 R2），验证同家族的 a、b 对 R 只计 1 个家族
        scenarios = [
            {"id": "R", "weight": 10, "k": 2},
            {"id": "R2", "weight": 5, "k": 1},
        ]
        checks = [
            {"id": "a", "family": "F1", "cost": 1, "covers": ["R", "R2"]},
            {"id": "b", "family": "F1", "cost": 1, "covers": ["R"]},
        ]
        res = solve(scenarios, checks, budget=100)
        self.assertEqual(res["objective"]["covered_weight"], 5)
        r = res["scenarios"][0]
        self.assertFalse(r["covered"])
        self.assertEqual(r["family_count"], 1)
        self.assertEqual(r["families"], ["F1"])

    def test_second_family_satisfies(self):
        scenarios = [{"id": "R", "weight": 10, "k": 2}]
        res = solve(scenarios, self._checks(), budget=100)
        self.assertTrue(res["scenarios"][0]["covered"])
        self.assertEqual(res["scenarios"][0]["family_count"], 2)
        self.assertEqual(res["objective"]["covered_weight"], 10)
        # 同家族不重复计数：a、b、c 全选也只有 2 个家族
        self.assertEqual(res["scenarios"][0]["families"], ["F1", "F2"])
        # 成本最小化：只需 a、c 两个检查即可，不应选入多余的 b
        self.assertEqual(res["selected_checks"], ["a", "c"])
        self.assertEqual(res["objective"]["total_cost"], 2)


class ObjectivePriorityTest(unittest.TestCase):
    def test_weight_beats_cost(self):
        # 方案1：贵但覆盖高权重场景；方案2：便宜但只覆盖低权重场景
        scenarios = [
            {"id": "HIGH", "weight": 100, "k": 1},
            {"id": "LOW", "weight": 1, "k": 1},
        ]
        checks = [
            {"id": "x", "family": "F1", "cost": 50, "covers": ["HIGH"]},
            {"id": "y", "family": "F2", "cost": 1, "covers": ["LOW"]},
        ]
        res = solve(scenarios, checks, budget=50)
        self.assertEqual(res["selected_checks"], ["x"])
        self.assertEqual(res["objective"]["covered_weight"], 100)

    def test_cost_tiebreak(self):
        # 两种等权覆盖方式，选更便宜的
        scenarios = [{"id": "R", "weight": 5, "k": 1}]
        checks = [
            {"id": "cheap", "family": "F1", "cost": 2, "covers": ["R"]},
            {"id": "pricey", "family": "F2", "cost": 9, "covers": ["R"]},
        ]
        res = solve(scenarios, checks, budget=100)
        self.assertEqual(res["selected_checks"], ["cheap"])
        self.assertEqual(res["objective"]["total_cost"], 2)

    def test_lexicographic_tiebreak(self):
        # 权重、成本完全相同，选 ID 字典序更小者
        scenarios = [{"id": "R", "weight": 5, "k": 1}]
        checks = [
            {"id": "m", "family": "F1", "cost": 3, "covers": ["R"]},
            {"id": "b", "family": "F2", "cost": 3, "covers": ["R"]},
        ]
        res = solve(scenarios, checks, budget=100)
        self.assertEqual(res["selected_checks"], ["b"])

    def test_budget_excludes(self):
        scenarios = [{"id": "R", "weight": 5, "k": 1}]
        checks = [{"id": "a", "family": "F1", "cost": 10, "covers": ["R"]}]
        res = solve(scenarios, checks, budget=5)
        self.assertEqual(res["selected_checks"], [])
        self.assertEqual(res["objective"]["covered_weight"], 0)

    def test_hours_budget(self):
        scenarios = [{"id": "R", "weight": 5, "k": 1}]
        checks = [
            {"id": "slow", "family": "F1", "cost": 1, "hours": 100,
             "covers": ["R"]},
            {"id": "fast", "family": "F2", "cost": 2, "hours": 3,
             "covers": ["R"]},
        ]
        res = solve(scenarios, checks, budget=100, hours_budget=10)
        self.assertEqual(res["selected_checks"], ["fast"])
        self.assertEqual(res["objective"]["total_hours"], 3)

    def test_multi_scenario_tradeoff(self):
        # 预算 10：要么覆盖 S1(w=8)，要么同时覆盖 S2(w=5)+S3(w=4)
        scenarios = [
            {"id": "S1", "weight": 8, "k": 1},
            {"id": "S2", "weight": 5, "k": 1},
            {"id": "S3", "weight": 4, "k": 1},
        ]
        checks = [
            {"id": "p", "family": "F1", "cost": 10, "covers": ["S1"]},
            {"id": "q", "family": "F2", "cost": 5, "covers": ["S2"]},
            {"id": "r", "family": "F3", "cost": 5, "covers": ["S3"]},
        ]
        res = solve(scenarios, checks, budget=10)
        self.assertEqual(res["selected_checks"], ["q", "r"])
        self.assertEqual(res["objective"]["covered_weight"], 9)

    def test_default_k_is_one(self):
        scenarios = [{"id": "R", "weight": 3}]
        checks = [{"id": "a", "family": "F1", "cost": 1, "covers": ["R"]}]
        res = solve(scenarios, checks, budget=1)
        self.assertTrue(res["scenarios"][0]["covered"])


class OptimalityCrossCheckTest(unittest.TestCase):
    """随机小规模实例上与独立暴力枚举逐一比对最优目标值。"""

    def test_random_instances_match_brute_force(self):
        rng = random.Random(20260927)
        for trial in range(60):
            n_scen = rng.randint(1, 4)
            n_check = rng.randint(1, 9)
            scenarios = [
                {"id": f"S{s}", "weight": rng.randint(1, 9),
                 "k": rng.randint(1, 3)}
                for s in range(n_scen)
            ]
            checks = []
            for c in range(n_check):
                covers = [f"S{s}" for s in range(n_scen)
                          if rng.random() < 0.5]
                checks.append({
                    "id": f"c{c}",
                    "family": f"F{rng.randint(0, 2)}",
                    "cost": rng.randint(0, 8),
                    "hours": rng.randint(0, 5),
                    "covers": covers,
                })
            budget = rng.randint(0, 20)
            hours_budget = rng.choice([None, rng.randint(0, 15)])
            res = solve(scenarios, checks, budget, hours_budget)
            bw, bc, bids = brute_force(scenarios, checks, budget, hours_budget)
            self.assertEqual(res["objective"]["covered_weight"], bw,
                             f"trial {trial}: weight")
            self.assertEqual(res["objective"]["total_cost"], bc,
                             f"trial {trial}: cost")
            self.assertEqual(tuple(res["selected_checks"]), bids,
                             f"trial {trial}: ids")


class ValidationTest(unittest.TestCase):
    def test_unknown_scenario_rejected(self):
        with self.assertRaises(ValueError):
            solve([{"id": "R"}],
                  [{"id": "a", "family": "F", "covers": ["NOPE"]}], 10)

    def test_duplicate_check_id_rejected(self):
        with self.assertRaises(ValueError):
            solve([{"id": "R"}],
                  [{"id": "a", "family": "F", "covers": ["R"]},
                   {"id": "a", "family": "G", "covers": ["R"]}], 10)


if __name__ == "__main__":
    unittest.main()
