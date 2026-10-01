"""perishable_flow 的 unittest 测试。

包含：验收边界、最优性、容量守恒、解核验、与独立暴力枚举的交叉验证、CLI 端到端。
所有数据均为合成数据。
"""
import itertools
import json
import random
import subprocess
import sys
import tempfile
import unittest
from collections import defaultdict
from pathlib import Path

import perishable_flow as pf

MODULE = Path(__file__).resolve().parent / "perishable_flow.py"


def make_inst(edges, batches, horizon=None):
    return pf.instance_from_dict({"edges": edges, "batches": batches,
                                  "horizon": horizon})


def brute_force(inst):
    """独立的暴力枚举：对每批次的每个单位货物枚举路径组合（可重复组合），
    跨批次取笛卡尔积，逐一检查容量并取最小成本。返回 None 表示不可行。"""
    edges_by_src = defaultdict(list)
    for i, e in enumerate(inst.edges):
        edges_by_src[e.src].append(i)
    paths = [pf.enumerate_paths(inst, b, edges_by_src, 100_000) for b in inst.batches]
    if any(b.quantity > 0 and not ps for b, ps in zip(inst.batches, paths)):
        return None
    choices = [list(itertools.combinations_with_replacement(range(len(ps)), b.quantity))
               for b, ps in zip(inst.batches, paths)]
    best = None
    for combo in itertools.product(*choices):
        usage, cost, ok = defaultdict(int), 0, True
        for bi, idxs in enumerate(combo):
            for idx in idxs:
                p = paths[bi][idx]
                cost += sum(inst.edges[ei].cost for ei, _ in p)
                for ei, t in p:
                    usage[(ei, t)] += 1
                    if usage[(ei, t)] > inst.edges[ei].capacity:
                        ok = False
        if ok and (best is None or cost < best):
            best = cost
    return best


class AcceptanceTests(unittest.TestCase):
    """题目给定的验收边界。"""

    def test_direct_edge_equal_expiry_unusable(self):
        # 批次 0 出发、失效 3，直达时长 3（到达 == 失效，过期）=> 不可行
        inst = make_inst(
            [{"id": "e0", "from": "A", "to": "C", "duration": 3,
              "capacity": 10, "cost": 1}],
            [{"id": "b0", "origin": "A", "destination": "C", "quantity": 1,
              "departure": 0, "expiry": 3}])
        sol = pf.solve(inst)
        self.assertEqual(sol["status"], "infeasible")

    def test_two_hops_arrival_before_expiry_ok(self):
        # 两段各 1 时长，时刻 2 到达（< 3）=> 可行；直达边不可用
        inst = make_inst(
            [{"id": "direct", "from": "A", "to": "C", "duration": 3,
              "capacity": 10, "cost": 1},
             {"id": "e1", "from": "A", "to": "B", "duration": 1,
              "capacity": 1, "cost": 1},
             {"id": "e2", "from": "B", "to": "C", "duration": 1,
              "capacity": 1, "cost": 1}],
            [{"id": "b0", "origin": "A", "destination": "C", "quantity": 1,
              "departure": 0, "expiry": 3}])
        sol = pf.solve(inst)
        self.assertEqual(sol["status"], "optimal")
        self.assertEqual(sol["total_cost"], 2)
        route = sol["batches"][0]["routes"][0]
        self.assertEqual([s["edge"] for s in route["steps"]], ["e1", "e2"])
        self.assertEqual(route["arrival"], 2)
        ok, errs = pf.verify(inst, sol)
        self.assertTrue(ok, errs)

    def test_capacity_one_cannot_carry_two_units_same_slot(self):
        # 容量 1、失效 2 => 只有 t=0 一个时间槽，2 单位不可行
        inst = make_inst(
            [{"id": "e0", "from": "A", "to": "B", "duration": 1,
              "capacity": 1, "cost": 5}],
            [{"id": "b0", "origin": "A", "destination": "B", "quantity": 2,
              "departure": 0, "expiry": 2}])
        self.assertEqual(pf.solve(inst)["status"], "infeasible")

    def test_capacity_one_with_waiting_feasible(self):
        # 同样容量 1，失效放宽到 3：第二单位在仓库等待到 t=1 出发
        inst = make_inst(
            [{"id": "e0", "from": "A", "to": "B", "duration": 1,
              "capacity": 1, "cost": 5}],
            [{"id": "b0", "origin": "A", "destination": "B", "quantity": 2,
              "departure": 0, "expiry": 3}])
        sol = pf.solve(inst)
        self.assertEqual(sol["status"], "optimal")
        self.assertEqual(sol["total_cost"], 10)
        departs = sorted(u["depart"] for u in sol["edge_usage"])
        self.assertEqual(departs, [0, 1])
        for u in sol["edge_usage"]:
            self.assertLessEqual(u["used"], 1)


class OptimalityTests(unittest.TestCase):

    def test_prefers_cheaper_route(self):
        inst = make_inst(
            [{"id": "expensive", "from": "A", "to": "C", "duration": 1,
              "capacity": 10, "cost": 10},
             {"id": "c1", "from": "A", "to": "B", "duration": 1,
              "capacity": 10, "cost": 2},
             {"id": "c2", "from": "B", "to": "C", "duration": 1,
              "capacity": 10, "cost": 2}],
            [{"id": "b0", "origin": "A", "destination": "C", "quantity": 3,
              "departure": 0, "expiry": 5}])
        sol = pf.solve(inst)
        self.assertEqual(sol["total_cost"], 12)  # 3 * (2+2)
        used_edges = {u["edge"] for u in sol["edge_usage"]}
        self.assertNotIn("expensive", used_edges)

    def test_capacity_forces_split_between_routes(self):
        # 便宜边容量 1，第二单位被迫走贵边
        inst = make_inst(
            [{"id": "cheap", "from": "A", "to": "B", "duration": 1,
              "capacity": 1, "cost": 1},
             {"id": "dear", "from": "A", "to": "B", "duration": 1,
              "capacity": 1, "cost": 9}],
            [{"id": "b0", "origin": "A", "destination": "B", "quantity": 2,
              "departure": 0, "expiry": 2}])
        sol = pf.solve(inst)
        self.assertEqual(sol["status"], "optimal")
        self.assertEqual(sol["total_cost"], 10)

    def test_multi_batch_shared_capacity_distinct_expiry(self):
        # 两批共享容量 1 的边：失效 2 的批必须 t=0 出发，失效 4 的批等待到 t=1
        inst = make_inst(
            [{"id": "e0", "from": "A", "to": "B", "duration": 1,
              "capacity": 1, "cost": 3}],
            [{"id": "urgent", "origin": "A", "destination": "B", "quantity": 1,
              "departure": 0, "expiry": 2},
             {"id": "relaxed", "origin": "A", "destination": "B", "quantity": 1,
              "departure": 0, "expiry": 4}])
        sol = pf.solve(inst)
        self.assertEqual(sol["status"], "optimal")
        self.assertEqual(sol["total_cost"], 6)
        by_batch = {b["id"]: b for b in sol["batches"]}
        self.assertEqual(by_batch["urgent"]["routes"][0]["arrival"], 1)
        self.assertEqual(by_batch["relaxed"]["routes"][0]["arrival"], 2)

    def test_zero_quantity_batch(self):
        inst = make_inst(
            [{"id": "e0", "from": "A", "to": "B", "duration": 1,
              "capacity": 1, "cost": 1}],
            [{"id": "b0", "origin": "A", "destination": "B", "quantity": 0,
              "departure": 0, "expiry": 2}])
        sol = pf.solve(inst)
        self.assertEqual(sol["status"], "optimal")
        self.assertEqual(sol["total_cost"], 0)


class VerifyTests(unittest.TestCase):

    def _inst(self):
        return make_inst(
            [{"id": "e0", "from": "A", "to": "B", "duration": 1,
              "capacity": 1, "cost": 5}],
            [{"id": "b0", "origin": "A", "destination": "B", "quantity": 1,
              "departure": 0, "expiry": 3}])

    def test_verify_accepts_solver_output(self):
        inst = self._inst()
        ok, errs = pf.verify(inst, pf.solve(inst))
        self.assertTrue(ok, errs)

    def test_verify_detects_capacity_violation(self):
        inst = self._inst()
        bad = {"total_cost": 10, "batches": [{"id": "b0", "routes": [
            {"amount": 2, "arrival": 1, "cost": 5, "steps": [
                {"edge": "e0", "from": "A", "to": "B", "depart": 0, "arrive": 1}]}]}]}
        ok, errs = pf.verify(inst, bad)
        self.assertFalse(ok)
        self.assertTrue(any("容量" in e for e in errs))

    def test_verify_detects_expired_arrival(self):
        inst = self._inst()
        bad = {"total_cost": 5, "batches": [{"id": "b0", "routes": [
            {"amount": 1, "arrival": 3, "cost": 5, "steps": [
                {"edge": "e0", "from": "A", "to": "B", "depart": 2, "arrive": 3}]}]}]}
        ok, errs = pf.verify(inst, bad)
        self.assertFalse(ok)
        self.assertTrue(any("过期" in e for e in errs))

    def test_verify_detects_flow_imbalance(self):
        inst = self._inst()
        bad = {"total_cost": 0, "batches": [{"id": "b0", "routes": []}]}
        ok, errs = pf.verify(inst, bad)
        self.assertFalse(ok)
        self.assertTrue(any("不守恒" in e or "需求" in e for e in errs))


class CrossCheckTests(unittest.TestCase):
    """随机小例：求解器结果与独立暴力枚举逐一比对。"""

    def _random_instance(self, rng):
        nodes = ["A", "B", "C", "D"]
        edges = []
        for i in range(rng.randint(3, 6)):
            src, dst = rng.sample(nodes, 2)
            edges.append({"id": f"e{i}", "from": src, "to": dst,
                          "duration": rng.randint(1, 2),
                          "capacity": rng.randint(1, 2),
                          "cost": rng.randint(0, 9)})
        batches = []
        for j in range(rng.randint(1, 2)):
            origin, dest = rng.sample(nodes, 2)
            dep = rng.randint(0, 1)
            batches.append({"id": f"b{j}", "origin": origin, "destination": dest,
                            "quantity": rng.randint(1, 2), "departure": dep,
                            "expiry": dep + rng.randint(2, 5)})
        return make_inst(edges, batches)

    def test_bruteforce_crosscheck(self):
        rng = random.Random(20261001)
        checked = 0
        for _ in range(60):
            inst = self._random_instance(rng)
            expected = brute_force(inst)
            sol = pf.solve(inst)
            if expected is None:
                self.assertEqual(sol["status"], "infeasible")
            else:
                self.assertEqual(sol["status"], "optimal")
                self.assertEqual(sol["total_cost"], expected)
                ok, errs = pf.verify(inst, sol)
                self.assertTrue(ok, errs)
            checked += 1
        self.assertEqual(checked, 60)


class CliTests(unittest.TestCase):

    def test_cli_solve_then_verify(self):
        inst_data = {
            "edges": [{"id": "direct", "from": "A", "to": "C", "duration": 3,
                       "capacity": 10, "cost": 1},
                      {"id": "e1", "from": "A", "to": "B", "duration": 1,
                       "capacity": 1, "cost": 1},
                      {"id": "e2", "from": "B", "to": "C", "duration": 1,
                       "capacity": 1, "cost": 1}],
            "batches": [{"id": "b0", "origin": "A", "destination": "C",
                         "quantity": 1, "departure": 0, "expiry": 3}]}
        with tempfile.TemporaryDirectory() as td:
            inp = Path(td) / "in.json"
            out = Path(td) / "out.json"
            inp.write_text(json.dumps(inst_data))
            r = subprocess.run([sys.executable, str(MODULE), "solve", str(inp),
                                "-o", str(out)], capture_output=True, text=True)
            self.assertEqual(r.returncode, 0, r.stderr)
            sol = json.loads(out.read_text())
            self.assertEqual(sol["status"], "optimal")
            self.assertTrue(sol["optimal"])
            self.assertTrue(sol["verified"])
            self.assertEqual(sol["total_cost"], 2)
            r2 = subprocess.run([sys.executable, str(MODULE), "verify",
                                  str(inp), str(out)], capture_output=True, text=True)
            self.assertEqual(r2.returncode, 0, r2.stderr)
            self.assertTrue(json.loads(r2.stdout)["verified"])

    def test_cli_infeasible_exit_code(self):
        inst_data = {
            "edges": [{"id": "e0", "from": "A", "to": "B", "duration": 3,
                       "capacity": 1, "cost": 1}],
            "batches": [{"id": "b0", "origin": "A", "destination": "B",
                         "quantity": 1, "departure": 0, "expiry": 3}]}
        with tempfile.TemporaryDirectory() as td:
            inp = Path(td) / "in.json"
            inp.write_text(json.dumps(inst_data))
            r = subprocess.run([sys.executable, str(MODULE), "solve", str(inp)],
                               capture_output=True, text=True)
            self.assertEqual(r.returncode, 1)
            self.assertEqual(json.loads(r.stdout)["status"], "infeasible")


if __name__ == "__main__":
    unittest.main()
