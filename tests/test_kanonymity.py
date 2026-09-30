"""k-匿名工具的标准库 unittest 测试（仅合成数据）。"""
from __future__ import annotations

import math
import unittest

from kanonymity import (
    NULL,
    Hierarchy,
    IncrementalAnonymizer,
    Record,
    brute_force_best_loss,
    check_optimal,
    compute_classes,
    evaluate,
    optimize,
)


def make_hierarchies():
    age = Hierarchy("age", {
        "*": None,
        "young": "*", "senior": "*",
        "18-24": "young", "25-34": "young",
        "55-64": "senior", "65+": "senior",
    })
    city = Hierarchy("city", {
        "*": None,
        "north": "*", "south": "*",
        "beijing": "north", "tianjin": "north",
        "shanghai": "south",
    })
    return (age, city)


def make_records():
    rows = [
        ("r1", "s1", ("18-24", "beijing")),
        ("r2", "s2", ("25-34", "beijing")),
        ("r3", "s3", ("18-24", "tianjin")),
        ("r4", "s4", ("55-64", "shanghai")),
        ("r5", "s5", ("65+", "shanghai")),
        ("r6", "s6", (None, "tianjin")),  # 空值
    ]
    return [Record(rid, sub, attrs) for rid, sub, attrs in rows]


class TestHierarchy(unittest.TestCase):
    def test_null_is_separate_category(self):
        age, _ = make_hierarchies()
        self.assertEqual(age.generalize(None, 0), NULL)
        self.assertEqual(age.generalize("", 0), NULL)
        # 空值类别不与任何非空叶子在低层合并
        self.assertNotEqual(age.generalize(None, 0), age.generalize("18-24", 0))
        self.assertNotEqual(age.generalize(None, 1), age.generalize("18-24", 1))
        # 仅在根层汇合
        self.assertEqual(age.generalize(None, age.max_level), "*")

    def test_ncp_bounds_and_monotonicity(self):
        age, _ = make_hierarchies()
        prev = -1.0
        for level in range(age.max_level + 1):
            val = age.ncp("18-24", level)
            self.assertGreaterEqual(val, prev)
            self.assertTrue(0.0 <= val <= 1.0)
            prev = val
        self.assertEqual(age.ncp("18-24", 0), 0.0)
        self.assertEqual(age.ncp("18-24", age.max_level), 1.0)

    def test_unknown_value_rejected(self):
        age, _ = make_hierarchies()
        with self.assertRaises(KeyError):
            age.generalize("unknown-value", 0)


class TestEquivalenceClasses(unittest.TestCase):
    def test_classes_and_deficient_records(self):
        hiers = make_hierarchies()
        records = make_records()
        classes = compute_classes(records, (0, 0), hiers)
        self.assertEqual(len(classes), 6)  # 全部互不相同
        deficient = [k for k, ec in classes.items() if ec.count < 2]
        self.assertEqual(len(deficient), 6)

    def test_generalization_regroups_and_recounts(self):
        """泛化后必须重新分组、重新计数，不沿用旧计数。"""
        hiers = make_hierarchies()
        records = make_records()
        level0 = compute_classes(records, (0, 0), hiers)
        level1 = compute_classes(records, (1, 1), hiers)
        # 旧分组计数不能直接沿用：键与计数都变了
        self.assertNotEqual(set(level0), set(level1))
        young_north = level1[("young", "north")]
        self.assertEqual(young_north.size, 3)
        self.assertEqual(young_north.count, 3)
        # 计数与从原始记录重算一致
        self.assertEqual(
            young_north.size,
            sum(1 for r in records
                if hiers[0].generalize(r.attrs[0], 1) == "young"
                and hiers[1].generalize(r.attrs[1], 1) == "north"),
        )

    def test_subject_count_mode(self):
        """同主体多条记录按主体计数时只计 1。"""
        hiers = make_hierarchies()
        records = [
            Record("a1", "same-subject", ("18-24", "beijing")),
            Record("a2", "same-subject", ("18-24", "beijing")),
        ]
        by_record = compute_classes(records, (0, 0), hiers, "record")
        by_subject = compute_classes(records, (0, 0), hiers, "subject")
        key = ("18-24", "beijing")
        self.assertEqual(by_record[key].count, 2)   # 记录模式达 k=2
        self.assertEqual(by_subject[key].count, 1)  # 主体模式不足 k=2

    def test_null_grouped_separately_in_classes(self):
        hiers = make_hierarchies()
        records = make_records()
        classes = compute_classes(records, (0, 0), hiers)
        self.assertIn((NULL, "tianjin"), classes)
        self.assertEqual(classes[(NULL, "tianjin")].member_rids, ("r6",))


class TestEvaluate(unittest.TestCase):
    def test_min_suppression_is_deficient_members(self):
        hiers = make_hierarchies()
        records = make_records()
        ev = evaluate(records, (0, 0), hiers, k=2)
        self.assertEqual(ev.min_suppression, 6)
        self.assertFalse(ev.feasible(budget=5))
        self.assertTrue(ev.feasible(budget=6))

    def test_suppression_penalty_in_loss(self):
        hiers = make_hierarchies()
        records = make_records()
        ev = evaluate(records, (0, 0), hiers, k=2, suppression_penalty=2.0)
        self.assertAlmostEqual(ev.suppression_loss, 12.0)
        self.assertAlmostEqual(ev.total_loss, ev.gen_loss + ev.suppression_loss)


class TestOptimize(unittest.TestCase):
    def test_optimal_matches_brute_force(self):
        """小域精确枚举核验最优性。"""
        hiers = make_hierarchies()
        records = make_records()
        result = optimize(records, hiers, k=2, budget=2)
        self.assertTrue(result.optimal)
        self.assertIsNotNone(result.best)
        exact = brute_force_best_loss(records, hiers, 2, 2)
        self.assertAlmostEqual(result.best.loss, exact)
        self.assertTrue(check_optimal(result, records, hiers, 2, 2))

    def test_suppression_budget_respected(self):
        hiers = make_hierarchies()
        records = make_records()
        result = optimize(records, hiers, k=2, budget=2)
        self.assertLessEqual(len(result.best.suppressed_rids), 2)

    def test_cheaper_to_suppress_outlier(self):
        """孤立记录应被抑制而非把全部记录泛化到根。"""
        hiers = make_hierarchies()
        records = [
            Record("p1", "s1", ("18-24", "beijing")),
            Record("p2", "s2", ("18-24", "beijing")),
            Record("x1", "s3", ("65+", "shanghai")),  # 孤立点
        ]
        result = optimize(records, hiers, k=2, budget=1)
        self.assertTrue(result.optimal)
        self.assertEqual(result.best.suppressed_rids, frozenset({"x1"}))
        self.assertEqual(result.best.levels, (0, 0))  # 其余记录零泛化
        self.assertAlmostEqual(result.best.loss, 1.0)  # 仅抑制惩罚

    def test_infeasible_when_budget_too_small(self):
        hiers = make_hierarchies()
        records = make_records()
        result = optimize(records, hiers, k=2, budget=0)
        # 预算 0 时只能纯泛化；存在零抑制的可行方案
        self.assertIsNotNone(result.best)
        self.assertEqual(len(result.best.suppressed_rids), 0)
        self.assertTrue(check_optimal(result, records, hiers, 2, 0))
        # 记录数 < k 时必然不可行
        few = records[:1]
        result2 = optimize(few, hiers, k=2, budget=0)
        self.assertIsNone(result2.best)
        self.assertTrue(result2.optimal)  # 完整枚举证明不可行

    def test_interruption_gives_valid_bounds(self):
        """预算中断：返回上下界且包住精确最优。"""
        hiers = make_hierarchies()
        records = make_records()
        exact = brute_force_best_loss(records, hiers, 2, 2)
        result = optimize(records, hiers, k=2, budget=2, max_evals=1)
        self.assertFalse(result.optimal)
        self.assertTrue(result.interrupted)
        self.assertLessEqual(result.lower_bound, exact + 1e-9)
        self.assertLessEqual(exact, result.upper_bound)  # ub 可能为 inf
        self.assertTrue(check_optimal(result, records, hiers, 2, 2))

    def test_evidence_covers_every_record(self):
        hiers = make_hierarchies()
        records = make_records()
        result = optimize(records, hiers, k=2, budget=2)
        ev = {e.rid: e for e in result.best.evidence}
        self.assertEqual(set(ev), {r.rid for r in records})
        for e in ev.values():
            if e.action == "kept":
                self.assertTrue(e.k_satisfied)
                self.assertGreaterEqual(e.class_count, 2)
            else:
                self.assertEqual(e.action, "suppressed")
        self.assertEqual(
            sum(1 for e in ev.values() if e.action == "suppressed"),
            len(result.best.suppressed_rids),
        )


class TestIncremental(unittest.TestCase):
    def test_remove_triggers_global_repair(self):
        """撤回记录后所有组都被重查，不能因局部组达 k 忽略其他组。"""
        hiers = make_hierarchies()
        records = [
            Record("a1", "s1", ("18-24", "beijing")),
            Record("a2", "s2", ("18-24", "beijing")),
            Record("b1", "s3", ("55-64", "shanghai")),
            Record("b2", "s4", ("55-64", "shanghai")),
        ]
        inc = IncrementalAnonymizer(hiers, k=2, budget=1)
        r0 = inc.load(records)
        self.assertEqual(r0.best.levels, (0, 0))  # 两组各自达 k
        # 撤回 b2：b 组跌破 k，修复必须全局生效（a 组也不许被漏查）
        r1 = inc.remove("b2")
        self.assertTrue(check_optimal(r1, records[:3], hiers, 2, 1))
        kept = [e for e in r1.best.evidence if e.action == "kept"]
        self.assertTrue(all(e.k_satisfied for e in kept))
        self.assertEqual(len(r1.best.evidence), 3)

    def test_add_then_remove_roundtrip(self):
        hiers = make_hierarchies()
        records = make_records()
        inc = IncrementalAnonymizer(hiers, k=2, budget=2)
        inc.load(records)
        inc.add(Record("r7", "s7", ("25-34", "beijing")))
        after = inc.remove("r7")
        base = optimize(records, hiers, k=2, budget=2)
        self.assertAlmostEqual(after.best.loss, base.best.loss)

    def test_remove_missing_raises(self):
        hiers = make_hierarchies()
        inc = IncrementalAnonymizer(hiers, k=2, budget=1)
        inc.load(make_records())
        with self.assertRaises(KeyError):
            inc.remove("no-such-record")


if __name__ == "__main__":
    unittest.main()
