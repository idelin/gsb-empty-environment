"""规则撤回后的增量修复测试：增量结果必须与全量重算一致。"""
import unittest

from cfg_analyzer import Grammar, IncrementalAnalyzer, analyze
from cfg_analyzer.samples import SAMPLES


def recompute(grammar: Grammar, removed: set[int]):
    """全量重算：删除指定规则后重新诊断。"""
    g2 = Grammar.build(
        grammar.terminals, grammar.nonterminals, grammar.start,
        [(p.lhs, p.rhs) for i, p in enumerate(grammar.productions) if i not in removed],
    )
    return analyze(g2)


def useless_set(diag):
    return {f.index for f in diag.cleanup.fates if not f.kept}


class TestIncrementalRetraction(unittest.TestCase):
    def check_consistent(self, grammar, inc, removed):
        full = recompute(grammar, removed)
        self.assertEqual(inc.diagnosis.generating, full.generating, "可生成集不一致")
        self.assertEqual(inc.diagnosis.reachable, full.reachable, "可达集不一致")
        self.assertEqual(inc.diagnosis.useful, full.useful, "有用集不一致")
        self.assertEqual(inc.diagnosis.language_empty, full.language_empty, "空语言判定不一致")
        self.assertEqual(inc.diagnosis.cycle_analysis.infinitely_ambiguous,
                         full.cycle_analysis.infinitely_ambiguous, "无限歧义判定不一致")
        self.assertEqual(inc.diagnosis.cycle_analysis.language_infinite,
                         full.cycle_analysis.language_infinite, "语言无限判定不一致")
        # 无用规则（按产生式内容比对）
        self.assertEqual(
            {str(f.production) for f in inc.diagnosis.cleanup.fates if not f.kept},
            {str(f.production) for f in full.cleanup.fates if not f.kept},
            "无用规则集合不一致",
        )

    def test_single_retractions_all_samples(self):
        for name, (_desc, g) in SAMPLES.items():
            for i in range(len(g.productions)):
                with self.subTest(sample=name, retract=i):
                    inc = IncrementalAnalyzer(g)
                    inc.retract(i)
                    self.check_consistent(g, inc, {i})

    def test_sequential_retractions(self):
        g = SAMPLES["nullable_cycle_with_exit"][1]
        inc = IncrementalAnalyzer(g)
        removed = set()
        # 依次撤回两条终结出口：#4 A→a、#3 B→ε
        d1 = inc.retract(4)
        removed.add(4)
        self.assertEqual(d1.lost_generating, [])          # B→ε 仍可支撑
        self.assertEqual(sorted(d1.recovered), ["A", "S"])  # 级联删除后被替代证明恢复
        self.check_consistent(g, inc, removed)
        d2 = inc.retract(3)
        removed.add(3)
        self.assertEqual(sorted(d2.lost_generating), ["A", "B", "S"])
        self.assertTrue(d2.language_became_empty)
        self.assertTrue(inc.diagnosis.language_empty)
        self.check_consistent(g, inc, removed)

    def test_retract_breaks_growth_cycle(self):
        g = SAMPLES["growth_cycle"][1]
        inc = IncrementalAnalyzer(g)
        self.assertTrue(inc.diagnosis.cycle_analysis.language_infinite)
        inc.retract(1)  # 撤回 A→A a
        self.assertFalse(inc.diagnosis.cycle_analysis.language_infinite)
        self.check_consistent(g, inc, {1})

    def test_double_retract_idempotent_guard(self):
        g = SAMPLES["mixed"][1]
        inc = IncrementalAnalyzer(g)
        inc.retract(0)
        with self.assertRaises(ValueError):
            inc.retract(0)

    def test_diff_reports_new_useless_rules(self):
        g = Grammar.build(["a", "b"], ["S", "A", "B"], "S",
                          [("S", ("A", "B")), ("A", ("a",)), ("B", ("b",))])
        inc = IncrementalAnalyzer(g)
        diff = inc.retract(1)  # 撤回 A→a ⇒ A 不可生成 ⇒ S→A B 无用 ⇒ S 也不可生成
        self.assertEqual(diff.lost_generating, ["A", "S"])
        self.assertIn(0, diff.newly_useless_rules)
        self.assertTrue(diff.language_became_empty)


if __name__ == "__main__":
    unittest.main()
