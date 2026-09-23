"""symrewrite 的 unittest 测试套件。

运行：python3 -m unittest discover -s tests -v
"""
import os
import sys
import unittest
from fractions import Fraction

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from symrewrite import (
    Add, Div, Mul, Num, Pow, Sym,
    canonical, canonical_key, get_at, replace_at,
    default_rules, find_blocked, replay, rewrite, swap_rules,
    CYCLE, NORMAL_FORM, STEP_LIMIT,
)


class RewriteTestCase(unittest.TestCase):
    def setUp(self):
        self.rules = default_rules()


class TestExactRational(RewriteTestCase):
    def test_chain_and_exact_fraction(self):
        e = Mul(Add(Num("1/2"), Num("1/3")), Div(Sym("y"), Sym("y")))
        r = rewrite(e, self.rules)
        self.assertEqual(r.status, NORMAL_FORM)
        self.assertEqual([s.rule for s in r.steps],
                         ["fold-add", "div-same", "fold-mul"])
        self.assertEqual(r.final, Num(Fraction(5, 6)))
        self.assertEqual(r.final.value.denominator, 6)  # 精确有理数，非浮点

    def test_exact_thirds_not_float(self):
        # 1/3 * 3 = 1 精确成立；浮点会得到 0.9999999999999999 之类
        r = rewrite(Mul(Num("1/3"), Num(3)), self.rules)
        self.assertEqual(r.final, Num(1))
        self.assertIsInstance(r.final.value, Fraction)

    def test_chain_links_and_positions(self):
        e = Mul(Add(Num("1/2"), Num("1/3")), Div(Sym("y"), Sym("y")))
        r = rewrite(e, self.rules)
        self.assertEqual(r.steps[0].position, (0,))   # 局部位置确定
        self.assertEqual(r.steps[1].position, (1,))
        self.assertEqual(r.steps[2].position, ())
        for prev, nxt in zip(r.steps, r.steps[1:]):   # 链式衔接
            self.assertEqual(prev.after, nxt.before)
        for s in r.steps:                             # 每步都有适用域说明
            self.assertTrue(s.domain)


class TestPriorityAndCondition(RewriteTestCase):
    def test_priority_fold_beats_double_add(self):
        r = rewrite(Add(Num(1), Num(1)), self.rules)
        self.assertEqual(r.steps[0].rule, "fold-add")  # 优先级 20 > 5
        self.assertEqual(r.final, Num(2))

    def test_double_add_on_symbols(self):
        r = rewrite(Add(Sym("x"), Sym("x")), self.rules)
        self.assertEqual(r.steps[0].rule, "double-add")
        self.assertIn("fold-add", r.steps[0].blocked)  # 条件不满足被拦下
        self.assertEqual(r.final, Mul(Num(2), Sym("x")))

    def test_div_by_zero_unavailable(self):
        r = rewrite(Div(Num(1), Num(0)), self.rules)
        self.assertEqual(r.status, NORMAL_FORM)
        self.assertEqual(r.steps, [])                   # 没有可用规则
        self.assertEqual(r.final, Div(Num(1), Num(0)))  # 原样保留
        blocked = find_blocked(r.final, self.rules)
        self.assertIn(((), "fold-div"), blocked)

    def test_zero_over_zero_not_cancelled(self):
        r = rewrite(Div(Num(0), Num(0)), self.rules)
        self.assertEqual(r.final, Div(Num(0), Num(0)))  # div-same 被 x≠0 拦下
        blocked_names = [name for _, name in find_blocked(r.final, self.rules)]
        self.assertIn("div-same", blocked_names)

    def test_div_same_symbol(self):
        r = rewrite(Div(Sym("x"), Sym("x")), self.rules)
        self.assertEqual(r.final, Num(1))
        self.assertIn("≠ 0", r.steps[0].domain)         # 适用域说明

    def test_pow_zero_domain(self):
        r = rewrite(Pow(Sym("x"), Num(0)), self.rules)
        self.assertEqual(r.final, Num(1))
        self.assertIn("≠ 0", r.steps[0].domain)
        r0 = rewrite(Pow(Num(0), Num(0)), self.rules)   # 0^0 不化简
        self.assertEqual(r0.final, Pow(Num(0), Num(0)))


class TestCycleAndStepLimit(RewriteTestCase):
    def test_cycle_ab_ba(self):
        r = rewrite(Sym("a"), swap_rules(), max_steps=50)
        self.assertEqual(r.status, CYCLE)
        self.assertEqual(r.cycle, (0, 2))   # 初始状态第 2 步重复
        self.assertEqual(len(r.steps), 2)
        self.assertEqual(r.final, Sym("a"))

    def test_step_limit(self):
        e = Mul(Add(Num("1/2"), Num("1/3")), Div(Sym("y"), Sym("y")))
        r = rewrite(e, self.rules, max_steps=1)
        self.assertEqual(r.status, STEP_LIMIT)
        self.assertEqual(len(r.steps), 1)
        self.assertNotEqual(r.final, Num(Fraction(5, 6)))


class TestReplayAndPositions(RewriteTestCase):
    def test_replay_reproduces_final(self):
        e = Mul(Add(Num("1/2"), Num("1/3")), Div(Sym("y"), Sym("y")))
        r = rewrite(e, self.rules)
        self.assertEqual(replay(e, r.steps, self.rules), r.final)

    def test_replay_cycle_trace(self):
        r = rewrite(Sym("a"), swap_rules(), max_steps=50)
        self.assertEqual(replay(Sym("a"), r.steps, swap_rules()), r.final)

    def test_local_replace_at(self):
        e = Add(Sym("a"), Mul(Sym("b"), Sym("c")))
        self.assertEqual(get_at(e, (1, 0)), Sym("b"))
        self.assertEqual(replace_at(e, (1, 0), Sym("z")),
                         Add(Sym("a"), Mul(Sym("z"), Sym("c"))))
        self.assertEqual(e, Add(Sym("a"), Mul(Sym("b"), Sym("c"))))  # 不可变

    def test_canonical_commutative(self):
        e1 = Add(Sym("b"), Sym("a"))
        e2 = Add(Sym("a"), Sym("b"))
        self.assertEqual(canonical(e1), canonical(e2))
        self.assertEqual(canonical_key(e1), canonical_key(e2))


if __name__ == "__main__":
    unittest.main()
