import os
import sys
import unittest
from itertools import product

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

from kanonym import (
    Anonymizer,
    Hierarchy,
    NULL_LABEL,
    Record,
    below_k_records,
    build_evidence,
    evaluate,
    group_records,
    record_loss,
    solve_exact,
    subject_count,
)


def make_hierarchies():
    age = Hierarchy("age", {
        "23": "20-29", "28": "20-29",
        "31": "30-39", "38": "30-39",
        "20-29": "*", "30-39": "*", "*": "*",
    })
    city = Hierarchy("city", {
        "北京": "华北", "天津": "华北",
        "上海": "华东", "南京": "华东",
        "华北": "*", "华东": "*", "*": "*",
    })
    return [age, city]


def make_records():
    return [
        Record("a1", "s1", ("23", "北京")),
        Record("a2", "s2", ("28", "天津")),
        Record("a3", "s3", ("31", "上海")),
        Record("a4", "s4", ("38", "南京")),
        Record("a5", "s5", ("23", "北京")),
    ]


class TestHierarchy(unittest.TestCase):
    def setUp(self):
        self.age, self.city = make_hierarchies()

    def test_generalize_levels(self):
        self.assertEqual(self.age.generalize("23", 0), "23")
        self.assertEqual(self.age.generalize("23", 1), "20-29")
        self.assertEqual(self.age.generalize("23", 2), "*")
        self.assertEqual(self.age.generalize("23", 99), "*")  # 到根为止

    def test_null_is_own_category(self):
        for level in (0, 1, 2, 5):
            self.assertEqual(self.city.generalize(None, level), NULL_LABEL)

    def test_null_weight_singleton(self):
        # 空值为单例类别：1 / (叶子数 + 1)
        self.assertAlmostEqual(self.city.weight(None, 0), 1 / 5)
        self.assertAlmostEqual(self.city.weight(None, 2), 1 / 5)

    def test_ncp_weight(self):
        # "华北" 覆盖 2 个叶子，总类别数 = 4 叶子 + 1 空值
        self.assertAlmostEqual(self.city.weight("北京", 1), 2 / 5)
        self.assertAlmostEqual(self.city.weight("北京", 2), 4 / 5)

    def test_unknown_value_rejected(self):
        with self.assertRaises(KeyError):
            self.age.generalize("99", 0)

    def test_cycle_rejected(self):
        with self.assertRaises(ValueError):
            Hierarchy("bad", {"a": "b", "b": "a"})


class TestGroupingAndCounting(unittest.TestCase):
    def setUp(self):
        self.hiers = make_hierarchies()

    def test_subject_count_distinct(self):
        recs = [
            Record("x1", "s1", ("23", "北京")),
            Record("x2", "s1", ("23", "北京")),  # 同主体
            Record("x3", "s2", ("23", "北京")),
        ]
        self.assertEqual(subject_count(recs), 2)

    def test_same_subject_counts_once_for_k(self):
        # 同一主体 3 条记录，k=2：主体数只有 1，仍不足 k
        recs = [Record(f"x{i}", "s1", ("23", "北京")) for i in range(3)]
        bad = below_k_records(recs, self.hiers, (0, 0), k=2)
        self.assertEqual(len(bad), 3)

    def test_regrouping_uses_fresh_counts(self):
        # 泛化后重新分组：level (1,1) 与 (0,0) 的分组必须独立重算
        recs = make_records()
        raw = group_records(recs, self.hiers, (0, 0))
        gen = group_records(recs, self.hiers, (1, 1))
        self.assertEqual(len(raw), 4)  # a1/a5 同类，其余各自成类
        self.assertEqual(len(gen), 2)  # (20-29,华北) 与 (30-39,华东)
        sizes = sorted(len(v) for v in gen.values())
        self.assertEqual(sizes, [2, 3])

    def test_null_separate_class(self):
        recs = [
            Record("n1", "s1", ("23", None)),
            Record("n2", "s2", ("23", "北京")),
        ]
        # 即使完全泛化到 *，空值也不与北京合并
        classes = group_records(recs, self.hiers, (2, 2))
        self.assertEqual(len(classes), 2)


class TestLossAndEvaluation(unittest.TestCase):
    def setUp(self):
        self.hiers = make_hierarchies()
        self.recs = make_records()

    def test_lm_metric(self):
        # age max_level=2, city max_level=2；向量 (1,2) -> (1/2 + 2/2)/2
        loss = record_loss(self.hiers, ("23", "北京"), (1, 2), "lm")
        self.assertAlmostEqual(loss, 0.75)

    def test_min_suppression_is_whole_small_classes(self):
        # (0,0) 下 a2/a3/a4 各自成单主体类，k=2 -> 全部抑制
        ev = evaluate(self.recs, self.hiers, (0, 0), k=2, sup_budget=10)
        self.assertTrue(ev.feasible)
        self.assertEqual(sorted(ev.suppressed), ["a2", "a3", "a4"])

    def test_budget_infeasible(self):
        ev = evaluate(self.recs, self.hiers, (0, 0), k=2, sup_budget=1)
        self.assertFalse(ev.feasible)
        self.assertIsNone(ev.loss)

    def test_lower_bound_property(self):
        # LB(v) <= L(v)：抑制以 sup_cost=1 替代 <=1 的权重
        for vector in product((0, 1, 2), (0, 1, 2)):
            ev = evaluate(self.recs, self.hiers, vector, k=2, sup_budget=10)
            self.assertLessEqual(ev.lower_bound, ev.loss + 1e-9)


class TestExactSolver(unittest.TestCase):
    def setUp(self):
        self.hiers = make_hierarchies()
        self.recs = make_records()

    def brute_force_best(self, k, budget, metric="ncp"):
        best = None
        for vector in product(*[range(h.max_level + 1) for h in self.hiers]):
            ev = evaluate(self.recs, self.hiers, vector, k, budget, metric)
            if ev.feasible and (best is None or ev.loss < best.loss):
                best = ev
        return best

    def test_optimality_against_brute_force(self):
        for k, budget in ((2, 0), (2, 2), (3, 1), (3, 5)):
            for metric in ("ncp", "lm"):
                res = solve_exact(self.recs, self.hiers, k, budget, metric=metric)
                bf = self.brute_force_best(k, budget, metric)
                if bf is None:
                    self.assertEqual(res.status, "infeasible")
                else:
                    self.assertEqual(res.status, "optimal")
                    self.assertAlmostEqual(res.best.loss, bf.loss)

    def test_full_enumeration_proves_infeasible(self):
        # k=6 超过全数据集主体总数 5：任何向量、任何抑制都不可行
        res = solve_exact(self.recs, self.hiers, k=6, sup_budget=0)
        self.assertEqual(res.status, "infeasible")
        self.assertIsNone(res.best)

    def test_budget_interruption_gives_bounds(self):
        full = solve_exact(self.recs, self.hiers, k=2, sup_budget=2)
        part = solve_exact(self.recs, self.hiers, k=2, sup_budget=2,
                           max_evaluations=1)
        self.assertEqual(part.status, "budget_exceeded")
        self.assertIsNotNone(part.lower_bound)
        if part.upper_bound is not None:
            self.assertLessEqual(part.lower_bound, part.upper_bound + 1e-9)
            self.assertLessEqual(part.lower_bound, full.best.loss + 1e-9)
            self.assertGreaterEqual(part.upper_bound, full.best.loss - 1e-9)

    def test_evidence_covers_every_record(self):
        res = solve_exact(self.recs, self.hiers, k=2, sup_budget=2)
        ev = build_evidence(self.recs, res.best, k=2)
        self.assertEqual(set(ev), {r.rid for r in self.recs})
        supp = set(res.best.suppressed)
        for rid, e in ev.items():
            self.assertEqual(e["suppressed"], rid in supp)
            if not e["suppressed"]:
                self.assertTrue(e["meets_k"])
                self.assertGreaterEqual(e["class_subject_count"], 2)


class TestIncremental(unittest.TestCase):
    def setUp(self):
        self.hiers = make_hierarchies()
        self.recs = make_records()

    def test_add_record_repair(self):
        anon = Anonymizer(self.hiers, k=2, sup_budget=2)
        anon.load(self.recs)
        report = anon.add_records([Record("a6", "s6", ("31", "上海"))])
        self.assertTrue(report["all_groups_revalidated"])
        self.assertEqual(anon.result.status, "optimal")
        self.assertIn("a6", anon.evidence)

    def test_add_unknown_value_rejected(self):
        anon = Anonymizer(self.hiers, k=2, sup_budget=2)
        anon.load(self.recs)
        with self.assertRaises(KeyError):
            anon.add_records([Record("a6", "s6", ("45", "北京"))])

    def test_remove_breaks_k_then_repaired(self):
        anon = Anonymizer(self.hiers, k=2, sup_budget=2)
        anon.load(self.recs)
        self.assertEqual(anon.result.status, "optimal")
        # 撤回 a5：原始向量下 a1 落单；修复必须重新分组并给出可行方案
        report = anon.remove_records(["a5"])
        self.assertTrue(report["all_groups_revalidated"])
        self.assertIsNotNone(report["new_vector"])
        self.assertEqual(anon.result.status, "optimal")
        # 修复后所有保留记录所在类均满足 k（按主体计数）
        for e in anon.evidence.values():
            if not e["suppressed"]:
                self.assertTrue(e["meets_k"])

    def test_repair_checks_all_groups_not_just_local(self):
        # k=3、预算 0：向量 (1,1) 下两组分别为 3、4 个主体，均达标。
        # 撤回 b1、b2 后，未变更的 a3/a4 所在组跌破 k（2 < 3），
        # 修复必须发现该非局部违例并重新求解。
        recs = self.recs + [
            Record("b1", "s6", ("31", "上海")),
            Record("b2", "s7", ("38", "南京")),
        ]
        anon = Anonymizer(self.hiers, k=3, sup_budget=0)
        anon.load(recs)
        self.assertEqual(anon.result.best.vector, (1, 1))
        report = anon.remove_records(["b1", "b2"])
        self.assertTrue(report["all_groups_revalidated"])
        # 旧向量下 a3/a4 所在组不足 k（变更点 b1/b2 不在该组）
        self.assertFalse(report["old_vector_still_feasible"])
        old_check = evaluate(anon.records, self.hiers, (1, 1), k=3, sup_budget=0)
        self.assertEqual(sorted(old_check.suppressed), ["a3", "a4"])
        # 修复后新解可行，所有保留记录均满足 k
        self.assertEqual(anon.result.status, "optimal")
        for e in anon.evidence.values():
            if not e["suppressed"]:
                self.assertTrue(e["meets_k"])

    def test_remove_unknown_rejected(self):
        anon = Anonymizer(self.hiers, k=2, sup_budget=2)
        anon.load(self.recs)
        with self.assertRaises(KeyError):
            anon.remove_records(["nope"])


if __name__ == "__main__":
    unittest.main()
