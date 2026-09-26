#!/usr/bin/env python3
"""retention.py 的单元测试。所有数据均为合成数据。"""

import unittest

from retention import RetentionError, RetentionStore


def make_acceptance_store() -> RetentionStore:
    """验收场景：父目录下界 20，文件下界 10，created_at=0。"""
    s = RetentionStore()
    s.add_directory("R20", None, retention_days=20)
    s.add_directory("R5", None, retention_days=5)
    s.add_directory("S", "R20")  # 自身无下界，继承 R20
    s.add_file("f1", "S", created_at=0, retention_days=10)
    return s


class AcceptanceBoundaryTests(unittest.TestCase):
    def test_parent20_file10_time15_not_deletable(self):
        s = make_acceptance_store()
        self.assertEqual(s.effective_bound_days("f1"), 20)  # max(20, 10)
        self.assertEqual(s.deletable_at("f1"), 20)
        plan = s.build_plan(now=15)
        self.assertEqual(plan["entries"], [])  # 时间 15 不可删

    def test_after_move_to_bound5_time15_deletable(self):
        s = make_acceptance_store()
        s.move_dir("S", "R5")  # 目录移动后重新计算继承约束
        self.assertEqual(s.effective_bound_days("f1"), 10)  # max(5, 10)
        self.assertEqual(s.deletable_at("f1"), 10)
        plan = s.build_plan(now=15)
        self.assertEqual([e["file_id"] for e in plan["entries"]], ["f1"])

    def test_move_file_to_bound5_dir(self):
        s = make_acceptance_store()
        s.move_file("f1", "R5")
        self.assertEqual(s.deletable_at("f1"), 10)
        self.assertEqual(len(s.build_plan(now=15)["entries"]), 1)

    def test_file_legal_hold_never_deletable(self):
        s = make_acceptance_store()
        s.set_legal_hold("f1", True)
        self.assertIsNone(s.deletable_at("f1"))  # 无限
        s.move_dir("S", "R5")
        self.assertEqual(s.build_plan(now=10**9)["entries"], [])

    def test_dir_legal_hold_blocks_subtree(self):
        s = make_acceptance_store()
        s.set_dir_legal_hold("R20", True)  # 祖先目录任一标记存在均不可删
        self.assertIsNone(s.deletable_at("f1"))
        s.set_dir_legal_hold("R20", False)
        self.assertEqual(s.deletable_at("f1"), 20)

    def test_legal_hold_release_reindexes(self):
        s = make_acceptance_store()
        s.set_legal_hold("f1", True)
        self.assertEqual(s.due_files(now=100), [])
        s.set_legal_hold("f1", False)
        self.assertEqual([f.id for f in s.due_files(now=100)], ["f1"])


class PlanExecuteTests(unittest.TestCase):
    def test_execute_success(self):
        s = make_acceptance_store()
        plan = s.build_plan(now=20)
        result = s.execute_plan(plan, now=20)
        self.assertEqual([d["file_id"] for d in result["deleted"]], ["f1"])
        self.assertEqual(result["skipped"], [])

    def test_version_change_skips_with_reason(self):
        s = make_acceptance_store()
        plan = s.build_plan(now=20)  # 快照 version=1
        s.modify_file("f1")          # 计划生成后版本变化
        result = s.execute_plan(plan, now=20)
        self.assertEqual(result["deleted"], [])
        skip = result["skipped"][0]
        self.assertEqual(skip["file_id"], "f1")
        self.assertEqual(skip["planned_version"], 1)
        self.assertEqual(skip["current_version"], 2)
        self.assertIn("version changed", skip["reason"])

    def test_legal_hold_after_plan_skips(self):
        s = make_acceptance_store()
        plan = s.build_plan(now=20)
        s.set_legal_hold("f1", True)
        result = s.execute_plan(plan, now=20)
        self.assertEqual(result["deleted"], [])
        self.assertIn("legal hold", result["skipped"][0]["reason"])

    def test_missing_file_skips(self):
        s = make_acceptance_store()
        plan = s.build_plan(now=20)
        del s.files["f1"]
        result = s.execute_plan(plan, now=20)
        self.assertIn("no longer exists", result["skipped"][0]["reason"])


class IndexTests(unittest.TestCase):
    def test_time_advance_no_full_scan(self):
        s = RetentionStore()
        s.add_directory("D", None, retention_days=0)
        n = 1000
        for i in range(n):  # 可删除时间分别为 0..999
            s.add_file(f"f{i:04d}", "D", created_at=0, retention_days=i)
        s.stats["heap_pops"] = 0
        due = s.due_files(now=10)
        self.assertEqual(len(due), 11)
        # 只弹出到期项（11 次），远小于全量 1000
        self.assertEqual(s.stats["heap_pops"], 11)
        due = s.due_files(now=500)
        self.assertEqual(len(due), 501)
        self.assertEqual(s.stats["heap_pops"], 11 + 501)

    def test_stale_entries_skipped_after_move(self):
        s = make_acceptance_store()
        s.move_dir("S", "R5")  # 旧堆项 (20, ...) 过期
        s.stats["stale_skips"] = 0
        due = s.due_files(now=15)
        self.assertEqual([f.id for f in due], ["f1"])
        s.due_files(now=25)  # 触发旧项弹出
        self.assertGreaterEqual(s.stats["stale_skips"], 1)

    def test_due_files_non_destructive(self):
        s = make_acceptance_store()
        self.assertEqual(len(s.due_files(now=20)), 1)
        self.assertEqual(len(s.due_files(now=20)), 1)  # 重复查询结果一致


class StructureTests(unittest.TestCase):
    def test_nearest_ancestor_wins(self):
        s = RetentionStore()
        s.add_directory("A", None, retention_days=30)
        s.add_directory("B", "A", retention_days=7)
        s.add_directory("C", "B")  # 继承最近的 B=7，而非 A=30
        s.add_file("f", "C", created_at=0, retention_days=0)
        self.assertEqual(s.deletable_at("f"), 7)

    def test_move_dir_recomputes_whole_subtree(self):
        s = RetentionStore()
        s.add_directory("P1", None, retention_days=50)
        s.add_directory("P2", None, retention_days=3)
        s.add_directory("M", "P1")
        s.add_directory("Msub", "M")
        s.add_file("a", "M", created_at=0, retention_days=0)
        s.add_file("b", "Msub", created_at=0, retention_days=0)
        self.assertEqual(s.deletable_at("a"), 50)
        self.assertEqual(s.deletable_at("b"), 50)
        s.move_dir("M", "P2")
        self.assertEqual(s.deletable_at("a"), 3)
        self.assertEqual(s.deletable_at("b"), 3)

    def test_move_dir_cycle_rejected(self):
        s = make_acceptance_store()
        with self.assertRaises(RetentionError):
            s.move_dir("R20", "S")  # S 是 R20 的后代
        with self.assertRaises(RetentionError):
            s.move_dir("S", "S")

    def test_unknown_references_rejected(self):
        s = make_acceptance_store()
        with self.assertRaises(RetentionError):
            s.add_file("x", "NOPE", created_at=0)
        with self.assertRaises(RetentionError):
            s.move_dir("S", "NOPE")


if __name__ == "__main__":
    unittest.main()
