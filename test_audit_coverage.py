"""audit_coverage 的单元测试。所有数据均为合成数据。"""

import json
import subprocess
import sys
import unittest
from pathlib import Path

from audit_coverage import solve

PY = sys.executable
CLI = str(Path(__file__).with_name("audit_coverage.py"))


def scen(sid, k=1, weight=1):
    return {"id": sid, "k": k, "weight": weight}


def check(cid, family, cost, hours=0, covers=()):
    return {"id": cid, "family": family, "cost": cost,
            "hours": hours, "covers": list(covers)}


class AcceptanceBoundaryTest(unittest.TestCase):
    """验收边界：R 要求 2 个家族，a、b 同家族不算充分覆盖，加入 c 才满足。"""

    def test_same_family_not_sufficient(self):
        # R2 强制检查 a 被选中，从而可观察 R 实际获得的证据家族。
        scenarios = [scen("R", k=2, weight=10), scen("R2", k=1, weight=1)]
        checks = [
            check("a", "F1", 1, covers=["R", "R2"]),
            check("b", "F1", 1, covers=["R"]),
        ]
        result = solve(scenarios, checks, budget=100)
        self.assertEqual(result["covered_weight"], 1)  # 仅 R2 被覆盖
        self.assertFalse(result["scenarios"]["R"]["covered"])
        # 同家族多个检查不能虚增覆盖次数：证据家族只有 F1。
        self.assertEqual(result["scenarios"]["R"]["evidence_families"], ["F1"])

    def test_second_family_satisfies(self):
        scenarios = [scen("R", k=2, weight=10)]
        checks = [
            check("a", "F1", 1, covers=["R"]),
            check("b", "F1", 1, covers=["R"]),
            check("c", "F2", 1, covers=["R"]),
        ]
        result = solve(scenarios, checks, budget=100)
        self.assertEqual(result["covered_weight"], 10)
        self.assertTrue(result["scenarios"]["R"]["covered"])
        self.assertEqual(
            set(result["scenarios"]["R"]["evidence_families"]), {"F1", "F2"})
        # 成本最小化：a、b 同家族冗余，只选其中一个。
        self.assertEqual(result["total_cost"], 2)
        self.assertEqual(len(result["selected_checks"]), 2)


class ObjectiveTest(unittest.TestCase):
    def test_budget_blocks_coverage(self):
        scenarios = [scen("R", k=1, weight=5)]
        checks = [check("a", "F1", 10, covers=["R"])]
        result = solve(scenarios, checks, budget=5)
        self.assertEqual(result["covered_weight"], 0)
        self.assertEqual(result["selected_checks"], [])

    def test_maximize_weight_first(self):
        # 预算只够二选一，应选权重更高的场景。
        scenarios = [scen("R1", weight=1), scen("R2", weight=9)]
        checks = [
            check("a", "F1", 5, covers=["R1"]),
            check("b", "F2", 5, covers=["R2"]),
        ]
        result = solve(scenarios, checks, budget=5)
        self.assertEqual(result["selected_checks"], ["b"])
        self.assertEqual(result["covered_weight"], 9)

    def test_min_cost_tiebreak(self):
        # 两种方案覆盖权重相同，选总成本更低者。
        scenarios = [scen("R", k=1, weight=7)]
        checks = [
            check("a", "F1", 3, covers=["R"]),
            check("b", "F2", 8, covers=["R"]),
        ]
        result = solve(scenarios, checks, budget=100)
        self.assertEqual(result["selected_checks"], ["a"])
        self.assertEqual(result["total_cost"], 3)

    def test_lexicographic_tiebreak(self):
        # 权重与成本都相同，选 ID 字典序最小者。
        scenarios = [scen("R", k=1, weight=7)]
        checks = [
            check("b", "F1", 3, covers=["R"]),
            check("a", "F2", 3, covers=["R"]),
        ]
        result = solve(scenarios, checks, budget=100)
        self.assertEqual(result["selected_checks"], ["a"])

    def test_lexicographic_tiebreak_tuple(self):
        # 组合级字典序：("a","z") 与 ("b","z") 等权等价（a、b 同家族），
        # 应选 ("a","z")。
        scenarios = [scen("R", k=2, weight=7)]
        checks = [
            check("z", "F1", 1, covers=["R"]),
            check("b", "F2", 1, covers=["R"]),
            check("a", "F2", 1, covers=["R"]),
        ]
        result = solve(scenarios, checks, budget=100)
        self.assertEqual(result["selected_checks"], ["a", "z"])

    def test_hours_budget(self):
        scenarios = [scen("R", k=1, weight=5)]
        checks = [check("a", "F1", 1, hours=10, covers=["R"])]
        result = solve(scenarios, checks, budget=100, hours_budget=5)
        self.assertEqual(result["covered_weight"], 0)
        result = solve(scenarios, checks, budget=100, hours_budget=10)
        self.assertEqual(result["covered_weight"], 5)
        self.assertEqual(result["total_hours"], 10)

    def test_redundant_check_not_bought(self):
        # 已充分覆盖后不应多买冗余检查（成本最小化）。
        scenarios = [scen("R", k=1, weight=5)]
        checks = [
            check("a", "F1", 2, covers=["R"]),
            check("b", "F2", 2, covers=["R"]),
        ]
        result = solve(scenarios, checks, budget=100)
        self.assertEqual(len(result["selected_checks"]), 1)
        self.assertEqual(result["total_cost"], 2)

    def test_multi_scenario_shared_check(self):
        # 一个检查同时覆盖多个场景，预算迫使做全局权衡。
        scenarios = [scen("R1", weight=4), scen("R2", weight=4), scen("R3", weight=1)]
        checks = [
            check("ab", "F1", 6, covers=["R1", "R2"]),
            check("r1", "F2", 5, covers=["R1"]),
            check("r3", "F3", 1, covers=["R3"]),
        ]
        result = solve(scenarios, checks, budget=6)
        # 选 ab 得权重 8；选 r1+r3 得权重 5。
        self.assertEqual(result["selected_checks"], ["ab"])
        self.assertEqual(result["covered_weight"], 8)


class ValidationTest(unittest.TestCase):
    def test_duplicate_check_id(self):
        with self.assertRaises(ValueError):
            solve([scen("R")],
                  [check("a", "F1", 1), check("a", "F2", 1)], budget=10)

    def test_unknown_scenario_in_covers(self):
        with self.assertRaises(ValueError):
            solve([scen("R")], [check("a", "F1", 1, covers=["X"])], budget=10)


class CliTest(unittest.TestCase):
    PAYLOAD = {
        "budget": 2,
        "scenarios": [{"id": "R", "k": 2, "weight": 10}],
        "checks": [
            {"id": "a", "family": "F1", "cost": 1, "hours": 1, "covers": ["R"]},
            {"id": "b", "family": "F1", "cost": 1, "hours": 1, "covers": ["R"]},
            {"id": "c", "family": "F2", "cost": 1, "hours": 1, "covers": ["R"]},
        ],
    }

    def _run_cli(self, args, stdin_text):
        proc = subprocess.run(
            [PY, CLI, *args], input=stdin_text,
            capture_output=True, text=True, timeout=60)
        self.assertEqual(proc.returncode, 0, proc.stderr)
        return json.loads(proc.stdout)

    def test_cli_stdin(self):
        result = self._run_cli(["-"], json.dumps(self.PAYLOAD))
        self.assertTrue(result["scenarios"]["R"]["covered"])
        self.assertEqual(result["covered_weight"], 10)
        self.assertEqual(result["total_cost"], 2)

    def test_cli_file(self, ):
        path = Path(__file__).with_name("_tmp_cli_input.json")
        try:
            path.write_text(json.dumps(self.PAYLOAD), encoding="utf-8")
            result = self._run_cli([str(path)], "")
            self.assertEqual(result["selected_checks"], ["a", "c"])
        finally:
            path.unlink(missing_ok=True)


class OptimalityCrossCheckTest(unittest.TestCase):
    """与暴力枚举交叉验证小规模随机实例的最优性。"""

    def test_bruteforce_crosscheck(self):
        import itertools
        import random

        rng = random.Random(20260927)
        for trial in range(30):
            n_checks = rng.randint(1, 9)
            scenarios = [
                {"id": f"S{s}", "k": rng.randint(1, 3),
                 "weight": rng.randint(1, 9)}
                for s in range(rng.randint(1, 4))
            ]
            sids = [s["id"] for s in scenarios]
            checks = [
                {"id": f"c{i}", "family": f"F{rng.randint(1, 3)}",
                 "cost": rng.randint(1, 6), "hours": rng.randint(0, 4),
                 "covers": [s for s in sids if rng.random() < 0.5]}
                for i in range(n_checks)
            ]
            budget = rng.randint(0, 12)
            hours_budget = rng.choice([None, rng.randint(0, 10)])

            def key_of(sub):
                fam = {s["id"]: set() for s in scenarios}
                cost = hours = 0
                for i in sub:
                    c = checks[i]
                    cost += c["cost"]
                    hours += c["hours"]
                    for sid in c["covers"]:
                        fam[sid].add(c["family"])
                if cost > budget or (hours_budget is not None
                                     and hours > hours_budget):
                    return None
                w = sum(s["weight"] for s in scenarios
                        if len(fam[s["id"]]) >= s["k"])
                ids = tuple(sorted(checks[i]["id"] for i in sub))
                return (w, -cost, ids, cost)

            best = None
            for r in range(n_checks + 1):
                for sub in itertools.combinations(range(n_checks), r):
                    k = key_of(sub)
                    if k is None:
                        continue
                    w, neg_cost, ids, cost = k
                    cand = (w, neg_cost, ids)
                    if best is None or (cand[0], cand[1]) > (best[0], best[1]) \
                            or ((cand[0], cand[1]) == (best[0], best[1])
                                and cand[2] < best[2]):
                        best = cand
            exp_w, exp_neg_cost, exp_ids = best

            result = solve(scenarios, checks, budget,
                           hours_budget=hours_budget)
            self.assertEqual(result["covered_weight"], exp_w,
                             msg=f"trial {trial}")
            self.assertEqual(result["total_cost"], -exp_neg_cost,
                             msg=f"trial {trial}")
            self.assertEqual(tuple(result["selected_checks"]), exp_ids,
                             msg=f"trial {trial}")


if __name__ == "__main__":
    unittest.main()
