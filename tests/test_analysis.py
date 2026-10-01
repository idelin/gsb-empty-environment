"""核心诊断能力测试：不可生成、不可达、ε-环、无限歧义 vs 语言无限、
最短问题链、清理映射、等价简化、空语言。"""
import unittest

from cfg_analyzer import Grammar, GrammarError, analyze
from cfg_analyzer.cleanup import eliminate_unit_cycles
from cfg_analyzer.core import (expand_all_to_terminals, generating_certificate,
                               nullable_certificate, replay, verify_certificate)
from cfg_analyzer.enumerate import bounded_enumerate
from cfg_analyzer.samples import SAMPLES


def g_of(name):
    return SAMPLES[name][1]


def yields_of(grammar, max_depth=12):
    return set(bounded_enumerate(grammar, max_depth=max_depth).yields)


class TestNonGenerating(unittest.TestCase):
    """仅 A→B、B→A：不可生成；开始符无用 ⇒ 空语言说明。"""

    def setUp(self):
        self.diag = analyze(g_of("cycle_no_exit"))

    def test_nothing_generating(self):
        self.assertEqual(self.diag.generating, frozenset())
        self.assertEqual(self.diag.non_generating, frozenset({"A", "B"}))

    def test_language_empty_with_explanation(self):
        self.assertTrue(self.diag.language_empty)
        self.assertIn("∅", self.diag.empty_explanation)
        self.assertIn("A", self.diag.empty_explanation)

    def test_problem_chain_rooted_at_cycle(self):
        for sym in ("A", "B"):
            chain = self.diag.chains[sym]
            self.assertEqual(chain.root_kind, "cycle")
            self.assertEqual(chain.root_cycle, ("A", "B"))

    def test_cleanup_removes_everything(self):
        self.assertEqual(self.diag.cleanup.removed_count, 2)
        self.assertEqual(len(self.diag.cleanup.simplified.productions), 0)
        for fate in self.diag.cleanup.fates:
            self.assertFalse(fate.kept)
            self.assertTrue(any("不可生成" in r for r in fate.reasons))

    def test_certificate_valid(self):
        self.assertEqual(self.diag.cert_problems, [])
        self.assertEqual(self.diag.null_cert_problems, [])


class TestNullableCycleWithExit(unittest.TestCase):
    """可空环另有终结出口：不能全删；无限歧义但语言有限。"""

    def setUp(self):
        self.diag = analyze(g_of("nullable_cycle_with_exit"))

    def test_all_generating_and_nullable(self):
        self.assertEqual(self.diag.generating, frozenset({"S", "A", "B"}))
        self.assertEqual(self.diag.nullable, frozenset({"S", "A", "B"}))

    def test_cleanup_keeps_exit_rules(self):
        # 可空环另有终结出口 ⇒ 一条都不能删
        self.assertEqual(self.diag.cleanup.removed_count, 0)
        self.assertEqual(len(self.diag.cleanup.simplified.productions), 5)

    def test_epsilon_cycle_witness(self):
        self.assertTrue(self.diag.cycle_analysis.infinitely_ambiguous)
        self.assertFalse(self.diag.cycle_analysis.language_infinite)
        self.assertIsNotNone(self.diag.ambiguity)
        cyc = self.diag.ambiguity.cycle
        # 环见证确实构成 A ⇒+ …A…
        form = replay(self.diag.grammar, [cyc.symbol], cyc.steps)
        self.assertIn(cyc.symbol, form)

    def test_language_is_finite_set(self):
        self.assertEqual(yields_of(self.diag.grammar), {"", "a"})

    def test_unit_cycle_elimination_preserves_language(self):
        g2, notes = eliminate_unit_cycles(self.diag.grammar)
        self.assertEqual(yields_of(g2), {"", "a"})
        # 消除后不再含单位产生式
        for p in g2.productions:
            self.assertFalse(len(p.rhs) == 1 and p.rhs[0] in g2.nonterminals)

    def test_witness_string_has_multiple_derivations(self):
        w = self.diag.ambiguity.string
        res = bounded_enumerate(self.diag.grammar, max_depth=8)
        self.assertGreaterEqual(res.count(w), 3)


class TestUnreachable(unittest.TestCase):
    def setUp(self):
        self.diag = analyze(g_of("unreachable"))

    def test_unreachable_detected(self):
        self.assertEqual(self.diag.unreachable, frozenset({"X", "Y"}))
        self.assertTrue(self.diag.reach_consistent)

    def test_cleanup_mapping(self):
        removed = {f.index: f for f in self.diag.cleanup.fates if not f.kept}
        self.assertEqual(set(removed), {3, 4})
        self.assertTrue(any("不可达" in r for r in removed[3].reasons))
        # 语言保持 {a, aa}
        self.assertEqual(yields_of(self.diag.cleanup.simplified),
                         yields_of(self.diag.grammar))


class TestInfiniteAmbiguityVsInfiniteLanguage(unittest.TestCase):
    """相同终结串可有无限推导 ≠ 语言无限：两者必须分别诊断。"""

    def test_growth_cycle_infinite_language_not_ambiguous(self):
        diag = analyze(g_of("growth_cycle"))
        self.assertTrue(diag.cycle_analysis.language_infinite)
        self.assertFalse(diag.cycle_analysis.infinitely_ambiguous)
        li = diag.lang_inf
        samples = {li.sample(k) for k in range(5)}
        self.assertEqual(len(samples), 5)  # 泵出串两两不同
        self.assertEqual(li.sample(0), "a")
        self.assertEqual(li.sample(2), "aaa")

    def test_epsilon_context_cycle_ambiguous_but_finite(self):
        diag = analyze(g_of("epsilon_context_cycle"))
        self.assertTrue(diag.cycle_analysis.infinitely_ambiguous)
        self.assertFalse(diag.cycle_analysis.language_infinite)
        self.assertEqual(diag.ambiguity.string, "a")
        self.assertEqual(yields_of(diag.grammar), {"a"})  # 语言有限
        # 推导数随深度增长
        c1 = bounded_enumerate(diag.grammar, max_depth=4).count("a")
        c2 = bounded_enumerate(diag.grammar, max_depth=10).count("a")
        self.assertLess(c1, c2)

    def test_ambiguous_grammar_finite_ambiguity_when_no_epsilon_cycle(self):
        # A→AA|a：每串推导数有限（Catalan），语言无限
        g = Grammar.build(["a"], ["S", "A"], "S",
                          [("S", ("A",)), ("A", ("A", "A")), ("A", ("a",))])
        diag = analyze(g)
        self.assertFalse(diag.cycle_analysis.infinitely_ambiguous)
        self.assertTrue(diag.cycle_analysis.language_infinite)


class TestCertificate(unittest.TestCase):
    def test_certificate_is_least_fixpoint(self):
        for name, (_desc, g) in SAMPLES.items():
            with self.subTest(sample=name):
                cert = generating_certificate(g)
                self.assertEqual(verify_certificate(g, cert), [])
                self.assertEqual(verify_certificate(g, nullable_certificate(g)), [])

    def test_tampered_certificate_rejected(self):
        g = g_of("nullable_cycle_with_exit")
        cert = generating_certificate(g)
        # 篡改：删掉最后一轮 ⇒ 不完备
        cert.rounds = cert.rounds[:-1]
        cert.result = frozenset(s for r in cert.rounds for s, _ in r)
        problems = verify_certificate(g, cert)
        self.assertTrue(any("不动点" in p or "完备" in p or "也应加入" in p for p in problems))

    def test_shortest_yield_replayable(self):
        for name, (_desc, g) in SAMPLES.items():
            diag = analyze(g)
            for sym in sorted(diag.generating):
                with self.subTest(sample=name, sym=sym):
                    steps, w = expand_all_to_terminals(g, [sym], diag.yields.chosen)
                    self.assertEqual(w, diag.yields.best_str[sym])
                    self.assertEqual(len(w), diag.yields.best_len[sym])


class TestEnumerationNotProof(unittest.TestCase):
    """枚举没找到 ≠ 不可生成：浅层枚举为空时证书仍然有效。"""

    def test_deep_witness(self):
        diag = analyze(g_of("deep_witness"), enum_depth=3)
        # 深度 3 的枚举找不到任何终结串（链深 10）
        res = bounded_enumerate(diag.grammar, max_depth=3)
        self.assertEqual(res.yields, {})
        # 但证书证明 A0 可生成，最短串 'x'
        self.assertIn("A0", diag.generating)
        self.assertEqual(diag.yields.best_str["A0"], "x")
        self.assertFalse(diag.language_empty)
        # 足够深度的枚举能找到（交叉核验通过）
        res2 = bounded_enumerate(diag.grammar, max_depth=10)
        self.assertEqual(res2.count("x"), 1)


class TestMixed(unittest.TestCase):
    def setUp(self):
        self.diag = analyze(g_of("mixed"))

    def test_partition(self):
        self.assertEqual(self.diag.non_generating, frozenset({"C", "D"}))
        self.assertEqual(self.diag.unreachable, frozenset({"E", "F"}))
        self.assertEqual(self.diag.useful, frozenset({"S", "A", "B"}))

    def test_problem_chain_points_to_cycle(self):
        self.assertEqual(self.diag.chains["C"].root_kind, "cycle")
        self.assertEqual(self.diag.chains["D"].root_cycle, ("C", "D"))

    def test_cleanup(self):
        removed = {f.index for f in self.diag.cleanup.fates if not f.kept}
        self.assertEqual(removed, {4, 5, 6, 7, 8})
        kept = {f.index for f in self.diag.cleanup.fates if f.kept}
        self.assertEqual(kept, {0, 1, 2, 3})

    def test_language_preserved(self):
        self.assertEqual(yields_of(self.diag.cleanup.simplified),
                         yields_of(self.diag.grammar))


class TestInputValidation(unittest.TestCase):
    def test_explicit_alphabet_required(self):
        with self.assertRaises(GrammarError):
            Grammar.from_dict({"nonterminals": ["S"], "start": "S", "productions": []})

    def test_undeclared_symbol_rejected(self):
        with self.assertRaises(GrammarError):
            Grammar.build(["a"], ["S"], "S", [("S", ("b",))])

    def test_overlap_rejected(self):
        with self.assertRaises(GrammarError):
            Grammar.build(["a"], ["a", "S"], "S", [])

    def test_start_must_be_nonterminal(self):
        with self.assertRaises(GrammarError):
            Grammar.build(["a"], ["S"], "a", [])


if __name__ == "__main__":
    unittest.main()
