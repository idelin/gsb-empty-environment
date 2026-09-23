"""rewriter 的 unittest 测试套件。"""
import unittest
from fractions import Fraction

from rewriter import (
    Engine, Number, Status, Symbol, Step,
    PLit, PNum, PVar, papp,
    match, parse, render, standard_rules,
)


def cycle_rules():
    from rewriter import Rule
    return [
        Rule("a-to-b", PLit(Symbol("a")), PLit(Symbol("b")),
             domain="a ∈ {a, b}"),
        Rule("b-to-a", PLit(Symbol("b")), PLit(Symbol("a")),
             domain="b ∈ {a, b}"),
    ]


class TestMatch(unittest.TestCase):
    def test_var_binds_any_expr(self):
        bind = match(PVar("x"), parse("add(1, y)"))
        self.assertEqual(render(bind["x"]), "(1 + y)")

    def test_num_pattern_only_matches_number(self):
        self.assertIsNotNone(match(PNum("a"), Number(Fraction(1, 2))))
        self.assertIsNone(match(PNum("a"), Symbol("a")))

    def test_repeated_var_must_be_consistent(self):
        pat = papp("add", PVar("x"), PVar("x"))
        self.assertIsNotNone(match(pat, parse("add(y, y)")))
        self.assertIsNone(match(pat, parse("add(y, z)")))

    def test_literal_pattern(self):
        self.assertIsNotNone(match(PLit(Number(0)), Number(0)))
        self.assertIsNone(match(PLit(Number(0)), Number(1)))


class TestExactRational(unittest.TestCase):
    def setUp(self):
        self.engine = Engine(standard_rules())

    def test_add_fractions_exact(self):
        result = self.engine.rewrite(parse("add(1/2, 1/3)"))
        self.assertEqual(result.status, Status.NORMAL_FORM)
        self.assertEqual(result.final, Number(Fraction(5, 6)))

    def test_thirds_sum_exactly_to_one(self):
        result = self.engine.rewrite(parse("add(1/3, add(1/3, 1/3))"))
        self.assertEqual(result.final, Number(1))

    def test_mixed_chain(self):
        result = self.engine.rewrite(
            parse("add(add(1/2, 1/3), mul(2/3, 3/4))"))
        self.assertEqual(result.final, Number(Fraction(4, 3)))
        self.assertEqual(len(result.steps), 3)

    def test_div_exact(self):
        result = self.engine.rewrite(parse("div(3/4, 9/8)"))
        self.assertEqual(result.final, Number(Fraction(2, 3)))

    def test_no_float_anywhere(self):
        result = self.engine.rewrite(parse("mul(1/7, 21/2)"))
        self.assertEqual(result.final, Number(Fraction(3, 2)))
        self.assertIsInstance(result.final.value, Fraction)


class TestConditions(unittest.TestCase):
    def setUp(self):
        self.engine = Engine(standard_rules())

    def test_div_by_zero_rule_unavailable(self):
        result = self.engine.rewrite(parse("div(1, 0)"))
        self.assertEqual(result.status, Status.NORMAL_FORM)
        self.assertEqual(result.final, parse("div(1, 0)"))
        self.assertEqual(len(result.steps), 0)

    def test_abs_negative_branch(self):
        result = self.engine.rewrite(parse("abs(-3/4)"))
        self.assertEqual(result.final, Number(Fraction(3, 4)))
        self.assertEqual(result.steps[0].rule, "abs-neg")
        self.assertIn("< 0", result.steps[0].domain)

    def test_abs_nonnegative_branch(self):
        result = self.engine.rewrite(parse("abs(5/6)"))
        self.assertEqual(result.steps[0].rule, "abs-nonneg")

    def test_div_cancel_requires_nonzero(self):
        ok = self.engine.rewrite(parse("div(mul(x, 2), 2)"))
        self.assertEqual(ok.final, Symbol("x"))
        blocked = self.engine.rewrite(parse("div(mul(x, 0), 0)"))
        self.assertNotIn("div-cancel", [s.rule for s in blocked.steps])
        self.assertEqual(blocked.final, parse("div(0, 0)"))


class TestPriority(unittest.TestCase):
    def test_higher_priority_wins(self):
        from rewriter import Rule
        rules = [
            Rule("generic", papp("f", PVar("x")), PLit(Symbol("g")),
                 priority=1),
            Rule("specific", papp("f", PLit(Symbol("a"))),
                 PLit(Symbol("h")), priority=10),
        ]
        engine = Engine(rules)
        result = engine.rewrite(parse("f(a)"))
        self.assertEqual(result.steps[0].rule, "specific")
        self.assertEqual(result.final, Symbol("h"))

    def test_tie_broken_by_name_deterministically(self):
        from rewriter import Rule
        rules = [
            Rule("rb", papp("f", PVar("x")), PLit(Symbol("b"))),
            Rule("ra", papp("f", PVar("x")), PLit(Symbol("a"))),
        ]
        first = Engine(rules).rewrite(parse("f(z)")).steps[0].rule
        second = Engine(list(reversed(rules))).rewrite(parse("f(z)")).steps[0].rule
        self.assertEqual(first, second)
        self.assertEqual(first, "ra")


class TestCycleDetection(unittest.TestCase):
    def test_a_to_b_to_a_cycle(self):
        engine = Engine(cycle_rules())
        result = engine.rewrite(Symbol("a"))
        self.assertEqual(result.status, Status.CYCLE)
        self.assertEqual(result.cycle_prefix, 0)
        self.assertEqual([render(s.after) for s in result.steps], ["b", "a"])

    def test_longer_cycle_prefix(self):
        from rewriter import Rule
        rules = [
            Rule("a-to-b", PLit(Symbol("a")), PLit(Symbol("b"))),
            Rule("b-to-c", PLit(Symbol("b")), PLit(Symbol("c"))),
            Rule("c-to-b", PLit(Symbol("c")), PLit(Symbol("b"))),
        ]
        result = Engine(rules).rewrite(Symbol("a"))
        self.assertEqual(result.status, Status.CYCLE)
        self.assertEqual(result.cycle_prefix, 1)  # 状态 b 首次出现于步索引 1


class TestStepExhaustion(unittest.TestCase):
    def test_growing_rule_exhausts_steps(self):
        from rewriter import Rule
        rules = [Rule("grow", papp("s", PVar("x")),
                      papp("s", papp("s", PVar("x"))),
                      domain="任意 x")]
        engine = Engine(rules)
        result = engine.rewrite(parse("s(a)"), max_steps=4)
        self.assertEqual(result.status, Status.EXHAUSTED)
        self.assertEqual(len(result.steps), 4)
        self.assertEqual(render(result.final),
                         "s(s(s(s(s(a)))))")

    def test_max_steps_one(self):
        engine = Engine(standard_rules())
        result = engine.rewrite(parse("add(1/2, add(1/3, 1/4))"),
                                max_steps=1)
        self.assertEqual(result.status, Status.EXHAUSTED)
        self.assertEqual(len(result.steps), 1)


class TestDeterministicPosition(unittest.TestCase):
    def test_leftmost_outermost_position(self):
        engine = Engine(standard_rules())
        result = engine.rewrite(parse("add(add(1, 2), add(3, 4))"))
        self.assertEqual(result.steps[0].position, (0,))
        self.assertEqual(result.steps[1].position, (1,))
        self.assertEqual(result.steps[2].position, ())
        self.assertEqual(result.final, Number(10))

    def test_deep_position_path(self):
        engine = Engine(standard_rules())
        result = engine.rewrite(parse("f(g(add(1/4, 1/4)))"))
        self.assertEqual(result.steps[0].position, (0, 0))
        self.assertEqual(result.final, parse("f(g(1/2))"))


class TestReplay(unittest.TestCase):
    def test_replay_reproduces_chain(self):
        engine = Engine(standard_rules())
        result = engine.rewrite(
            parse("add(add(1/2, 1/3), mul(2/3, 3/4))"))
        final, ok = engine.replay(result.initial, result.steps)
        self.assertTrue(ok)
        self.assertEqual(final, result.final)

    def test_replay_detects_tampered_step(self):
        engine = Engine(standard_rules())
        result = engine.rewrite(parse("add(1/2, 1/3)"))
        tampered = [Step(1, "add-num", (), result.initial,
                         Number(Fraction(7, 6)), "a, b ∈ ℚ",
                         result.steps[0].bindings)]
        _, ok = engine.replay(result.initial, tampered)
        self.assertFalse(ok)

    def test_replay_rejects_wrong_position(self):
        engine = Engine(standard_rules())
        result = engine.rewrite(parse("add(add(1, 2), add(3, 4))"))
        bad = [Step(1, "add-num", (0, 0), result.initial,
                    result.initial, "a, b ∈ ℚ", {})]
        _, ok = engine.replay(result.initial, bad)
        self.assertFalse(ok)


class TestParseRender(unittest.TestCase):
    def test_roundtrip(self):
        from rewriter import render_prefix
        for text in ["x", "3/4", "-2", "add(1/2, x)",
                     "mul(2, add(x, 1))", "f(g(x), h)"]:
            self.assertEqual(render_prefix(parse(text)), text)

    def test_render_infix(self):
        self.assertEqual(render(parse("add(1/2, 1/3)")), "(1/2 + 1/3)")
        self.assertEqual(render(parse("mul(2, x)")), "(2 * x)")


class TestDomainRecorded(unittest.TestCase):
    def test_every_step_has_domain(self):
        engine = Engine(standard_rules())
        result = engine.rewrite(
            parse("add(add(1/2, 1/3), mul(2/3, 3/4))"))
        self.assertTrue(result.steps)
        for step in result.steps:
            self.assertTrue(step.domain)


if __name__ == "__main__":
    unittest.main(verbosity=2)
