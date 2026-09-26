import unittest

from minisearch import And, Budget, BudgetExhausted, Engine, Fuzzy, Index, Not, Phrase, Term


def build_index(n_docs: int = 50) -> Index:
    idx = Index()
    for i in range(n_docs):
        idx.add_document(f"doc{i:03d}", f"alpha beta gamma document number {i}")
    idx.add_document("special", "alpha unique token")
    return idx


class TestBudgetMechanics(unittest.TestCase):
    def test_charge_and_exhaustion(self):
        b = Budget(3)
        b.charge(2)
        b.charge(1)
        with self.assertRaises(BudgetExhausted):
            b.charge(1)
        self.assertEqual(b.remaining, 0)

    def test_negative_charge_rejected(self):
        with self.assertRaises(ValueError):
            Budget(1).charge(-1)


class TestBudgetedSearch(unittest.TestCase):
    def setUp(self):
        self.engine = Engine(build_index())

    def test_estimate_refusal_before_execution(self):
        # 估算即超预算：直接 incomplete，ops_used 为 0
        r = self.engine.search(Term("alpha"), max_ops=1)
        self.assertTrue(r.incomplete)
        self.assertIsNone(r.total_hits)
        self.assertEqual(r.hits, [])
        self.assertEqual(r.stats.ops_used, 0)
        self.assertGreater(r.stats.estimate, 1)
        self.assertIn("估算", r.stats.reason)

    def test_exhaustion_during_execution(self):
        # Phrase 对高词频文档的估算偏保守，实际执行会超过估算：
        # 用“估算 <= max_ops < 实际消耗”验证执行中真实计费会中断。
        idx = Index()
        for i in range(20):
            idx.add_document(f"rep{i}", "x x x x x")
        engine = Engine(idx)
        q = Phrase(["x", "x"])
        full = engine.search(q, max_ops=100_000)
        self.assertFalse(full.incomplete)
        self.assertEqual(full.total_hits, 20)
        estimate = full.stats.estimate
        used = full.stats.ops_used
        self.assertGreater(used, estimate)  # 该场景实际消耗高于估算
        tight = engine.search(q, max_ops=used - 1)
        self.assertGreaterEqual(used - 1, estimate)  # 确保不是被估算拒绝
        self.assertTrue(tight.incomplete)
        self.assertIsNone(tight.total_hits)  # 关键：不能伪报总命中
        self.assertIn("预算耗尽", tight.stats.reason)

    def test_shared_budget_across_subqueries(self):
        # 两个子查询各自都在预算内，但共享预算合计超支 -> 必须中断。
        # 若每个子查询各自重置预算，此查询会正常完成。
        idx = Index()
        for i in range(30):
            idx.add_document(f"a{i}", "apple fruit")
            idx.add_document(f"b{i}", "banana fruit")
        engine = Engine(idx)
        q = And(Term("apple"), Term("banana"))
        full = engine.search(q, max_ops=100_000)
        self.assertFalse(full.incomplete)
        used = full.stats.ops_used
        half = used // 2 + 1  # 每个子查询单独都够，合起来不够
        self.assertGreater(used, half)
        r = engine.search(q, max_ops=half)
        self.assertTrue(r.incomplete)
        self.assertIsNone(r.total_hits)

    def test_broad_not_controlled(self):
        # 宽泛 NOT：补集需扫描全部文档，小预算必须被拒绝/中断
        r = self.engine.search(Not(Term("nonexistent")), max_ops=10)
        self.assertTrue(r.incomplete)
        self.assertIsNone(r.total_hits)
        # 预算充足时同样的查询应完整完成
        r2 = self.engine.search(Not(Term("nonexistent")), max_ops=100_000)
        self.assertFalse(r2.incomplete)
        self.assertEqual(r2.total_hits, 51)

    def test_fuzzy_budget(self):
        r = self.engine.search(Fuzzy("alpha", max_edits=2), max_ops=100_000)
        self.assertFalse(r.incomplete)
        tight = self.engine.search(Fuzzy("alpha", max_edits=2), max_ops=5)
        self.assertTrue(tight.incomplete)
        self.assertIsNone(tight.total_hits)


class TestLimitVsExhaustion(unittest.TestCase):
    def setUp(self):
        self.engine = Engine(build_index())

    def test_limit_truncates_but_total_is_exact(self):
        r = self.engine.search(Term("alpha"), limit=5)
        self.assertFalse(r.incomplete)
        self.assertEqual(len(r.hits), 5)
        self.assertEqual(r.total_hits, 51)  # 精确总命中，不受 limit 影响
        self.assertTrue(r.stats.limit_applied)

    def test_no_limit_no_truncation_flag(self):
        r = self.engine.search(Term("alpha"))
        self.assertFalse(r.stats.limit_applied)
        self.assertEqual(len(r.hits), r.total_hits)

    def test_exhaustion_is_not_limit(self):
        r = self.engine.search(Term("alpha"), max_ops=1, limit=5)
        self.assertTrue(r.incomplete)
        self.assertIsNone(r.total_hits)
        self.assertFalse(r.stats.limit_applied)


if __name__ == "__main__":
    unittest.main()
