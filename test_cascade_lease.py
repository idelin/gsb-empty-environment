"""cascade_lease 的单元测试。所有数据均为合成数据。"""

import io
import json
import unittest
from contextlib import redirect_stdout
from unittest.mock import patch

import cascade_lease
from cascade_lease import LeaseError, LeaseSystem


def make_acceptance_system() -> LeaseSystem:
    """验收场景：父额 5，并发子租约 3（权重 10）与 2（权重 1）。"""
    system = LeaseSystem()
    system.add_root("root", quota=5, start=0, end=100, weight=0)
    system.add_lease("a", "root", quota=3, start=0, end=50, weight=10)
    system.add_lease("b", "root", quota=2, start=0, end=50, weight=1)
    return system


class TestAddLeaseConstraints(unittest.TestCase):
    def test_child_interval_must_be_contained(self):
        system = LeaseSystem()
        system.add_root("root", 5, 0, 100)
        with self.assertRaises(LeaseError):
            system.add_lease("x", "root", 1, 50, 150)
        with self.assertRaises(LeaseError):
            system.add_lease("y", "root", 1, -10, 50)

    def test_concurrent_quota_limit_on_add(self):
        system = LeaseSystem()
        system.add_root("root", 5, 0, 100)
        system.add_lease("a", "root", 3, 0, 50)
        system.add_lease("b", "root", 2, 0, 50)
        # 并发总和 3+2+1=6 > 5，拒绝
        with self.assertRaises(LeaseError):
            system.add_lease("c", "root", 1, 10, 40)
        # 不并发则可以
        system.add_lease("d", "root", 5, 50, 100)

    def test_half_open_interval_boundary(self):
        system = LeaseSystem()
        system.add_root("root", 5, 0, 100)
        system.add_lease("a", "root", 5, 0, 50)
        # [50, 100) 与 [0, 50) 不重叠，允许
        system.add_lease("b", "root", 5, 50, 100)

    def test_add_to_revoked_parent_rejected(self):
        system = make_acceptance_system()
        system.resize("root", quota=3)  # 撤销 b
        with self.assertRaises(LeaseError):
            system.add_lease("c", "b", 1, 0, 10)

    def test_unknown_parent_rejected(self):
        system = LeaseSystem()
        system.add_root("root", 5, 0, 100)
        with self.assertRaises(LeaseError):
            system.add_lease("x", "nope", 1, 0, 10)


class TestAcceptanceBoundary(unittest.TestCase):
    def test_reduce_quota_revokes_min_loss_child(self):
        """父额 5 -> 3：子租约 3(权重10)、2(权重1)，应撤销配额 2 的子租约。"""
        system = make_acceptance_system()
        result = system.resize("root", quota=3)
        self.assertEqual(result["revoked"], ["b"])
        self.assertEqual(result["loss"], 1)
        self.assertFalse(system.leases["a"].revoked)
        self.assertTrue(system.leases["b"].revoked)

    def test_no_overcommit_after_resize(self):
        """撤销后任意时刻活跃子租约配额总和不得超过新父额。"""
        system = make_acceptance_system()
        system.resize("root", quota=3)
        root = system.leases["root"]
        active = [c for c in (system.leases[i] for i in root.children)
                  if not c.revoked]
        for t in range(0, 100):
            load = sum(c.quota for c in active if c.start <= t < c.end)
            self.assertLessEqual(load, 3)

    def test_reduce_to_two_revokes_both(self):
        """父额减到 2：保留任一个都可行，但损失最小为撤销 a(10)+保留 b？
        保留 b(2<=2) 需撤销 a，损失 10；保留 a(3>2) 不可行；
        全撤损失 11。最优为撤销 a。"""
        system = make_acceptance_system()
        result = system.resize("root", quota=2)
        self.assertEqual(result["revoked"], ["a"])
        self.assertEqual(result["loss"], 10)


class TestCascadeRevocation(unittest.TestCase):
    def build_tree(self) -> LeaseSystem:
        system = LeaseSystem()
        system.add_root("root", 10, 0, 100, weight=0)
        system.add_lease("p", "root", 6, 0, 80, weight=2)
        system.add_lease("q", "root", 4, 0, 80, weight=3)
        system.add_lease("p1", "p", 3, 0, 60, weight=5)
        system.add_lease("p2", "p", 3, 0, 60, weight=7)
        system.add_lease("p1a", "p1", 1, 0, 40, weight=11)
        return system

    def test_cascade_revokes_descendants(self):
        """撤销 p 时，子孙 p1、p2、p1a 级联撤销。"""
        system = self.build_tree()
        # root 减额到 4：保留 q(4) 需撤销 p，损失 2+5+7+11=25；
        # 保留 p(6>4) 不可行；全撤损失 28。最优撤销 p 子树。
        result = system.resize("root", quota=4)
        self.assertEqual(result["revoked"], ["p", "p1", "p1a", "p2"])
        self.assertEqual(result["loss"], 25)
        for lid in ("p", "p1", "p2", "p1a"):
            self.assertTrue(system.leases[lid].revoked, lid)
        self.assertFalse(system.leases["q"].revoked)

    def test_loss_not_double_counted(self):
        """子树中每个租约的权重只计一次。"""
        system = self.build_tree()
        result = system.resize("root", quota=4)
        # 2+5+7+11 = 25，若重复计数会更大
        self.assertEqual(result["loss"], 2 + 5 + 7 + 11)

    def test_cascade_cost_prefers_revoking_deep_subtree(self):
        """撤销整棵子树的成本计入子孙权重，影响最优选择。"""
        system = LeaseSystem()
        system.add_root("root", 6, 0, 100, weight=0)
        # x 自身权重 1，但带着权重 50 的子孙
        system.add_lease("x", "root", 3, 0, 50, weight=1)
        system.add_lease("x1", "x", 1, 0, 50, weight=50)
        system.add_lease("y", "root", 3, 0, 50, weight=5)
        # 减额到 3：撤 x 损失 1+50=51，撤 y 损失 5 -> 撤 y
        result = system.resize("root", quota=3)
        self.assertEqual(result["revoked"], ["y"])
        self.assertEqual(result["loss"], 5)

    def test_inner_resize_cascades(self):
        """对中间租约减额，只影响其子树。"""
        system = self.build_tree()
        # p 减额到 3：p1(3,权5+子11=16)、p2(3,权7) 并发和 6>3
        # 撤 p1 损失 16，撤 p2 损失 7 -> 撤 p2
        result = system.resize("p", quota=3)
        self.assertEqual(result["revoked"], ["p2"])
        self.assertEqual(result["loss"], 7)
        self.assertFalse(system.leases["p1"].revoked)
        self.assertFalse(system.leases["p1a"].revoked)


class TestTieBreak(unittest.TestCase):
    def test_lexicographic_tie_break(self):
        """损失相同，选撤销 ID 排序序列字典序最小者。"""
        system = LeaseSystem()
        system.add_root("root", 6, 0, 100, weight=0)
        system.add_lease("m", "root", 3, 0, 50, weight=5)
        system.add_lease("a", "root", 3, 0, 50, weight=5)
        # 减额到 3：撤 m 或撤 a 损失都是 5，字典序 ["a"] < ["m"]
        result = system.resize("root", quota=3)
        self.assertEqual(result["revoked"], ["a"])
        self.assertEqual(result["loss"], 5)


class TestIntervalShrink(unittest.TestCase):
    def test_shrink_forces_revocation_of_outside_children(self):
        """父区间缩短后，不再被包含的子租约被强制撤销（含级联）。"""
        system = LeaseSystem()
        system.add_root("root", 10, 0, 100, weight=0)
        system.add_lease("early", "root", 3, 0, 40, weight=4)
        system.add_lease("late", "root", 3, 60, 100, weight=9)
        system.add_lease("late_child", "late", 1, 70, 90, weight=2)
        result = system.resize("root", start=0, end=50)
        self.assertEqual(result["revoked"], ["late", "late_child"])
        self.assertEqual(result["loss"], 11)
        self.assertFalse(system.leases["early"].revoked)

    def test_resize_escaping_parent_rejected(self):
        system = LeaseSystem()
        system.add_root("root", 10, 0, 100)
        system.add_lease("c", "root", 3, 10, 50)
        with self.assertRaises(LeaseError):
            system.resize("c", start=0, end=150)
        with self.assertRaises(LeaseError):
            system.resize("c", quota=11)

    def test_noop_resize_revokes_nothing(self):
        system = make_acceptance_system()
        result = system.resize("root", quota=5)
        self.assertEqual(result["revoked"], [])
        self.assertEqual(result["loss"], 0)


class TestCli(unittest.TestCase):
    def test_cli_acceptance_scenario(self):
        spec = {
            "operations": [
                {"op": "add_root", "id": "root", "quota": 5,
                 "start": 0, "end": 100, "weight": 0},
                {"op": "add_lease", "id": "a", "parent": "root", "quota": 3,
                 "start": 0, "end": 50, "weight": 10},
                {"op": "add_lease", "id": "b", "parent": "root", "quota": 2,
                 "start": 0, "end": 50, "weight": 1},
                {"op": "resize", "id": "root", "quota": 3},
            ]
        }
        stdin = io.StringIO(json.dumps(spec))
        stdout = io.StringIO()
        with patch("sys.stdin", stdin), redirect_stdout(stdout):
            rc = cascade_lease.main(["cascade_lease.py"])
        self.assertEqual(rc, 0)
        output = json.loads(stdout.getvalue())
        results = output["results"]
        self.assertEqual([r["status"] for r in results], ["ok"] * 4)
        resize_result = results[3]
        self.assertEqual(resize_result["revoked"], ["b"])
        self.assertEqual(resize_result["loss"], 1)

    def test_cli_reports_errors_and_continues(self):
        spec = {
            "operations": [
                {"op": "add_root", "id": "root", "quota": 1,
                 "start": 0, "end": 10},
                {"op": "add_lease", "id": "bad", "parent": "root", "quota": 1,
                 "start": 5, "end": 99},
                {"op": "resize", "id": "root", "quota": 1},
            ]
        }
        output = cascade_lease.run(spec)
        results = output["results"]
        self.assertEqual(results[0]["status"], "ok")
        self.assertEqual(results[1]["status"], "error")
        self.assertIn("not contained", results[1]["error"])
        self.assertEqual(results[2]["status"], "ok")
        self.assertEqual(results[2]["revoked"], [])


if __name__ == "__main__":
    unittest.main()
