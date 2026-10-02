import unittest

from interval_analysis import lang
from interval_analysis.lang import (
    Assert, Assign, BinOp, Compare, Const, If, Nondet, Var, While, seq,
)
from interval_analysis.verifier import Verdict, Verifier


class TestConcreteInterpreter(unittest.TestCase):
    def test_truncating_division(self):
        prog = seq(
            Assign("a", BinOp("/", Const(-7), Const(2))),
            Assign("b", BinOp("%", Const(-7), Const(2))),
        )
        result = lang.run(prog)
        self.assertIsNone(result.error)
        self.assertEqual(result.env["a"], -3)
        self.assertEqual(result.env["b"], -1)

    def test_replay_is_deterministic(self):
        prog = Assign("x", Nondet(-5, 5))
        first = lang.run(prog, inputs=[3])
        second = lang.run(prog, inputs=[3])
        self.assertEqual(first.env, second.env)
        self.assertEqual(first.choices, (3,))

    def test_replay_validates_range(self):
        with self.assertRaises(ValueError):
            lang.run(Assign("x", Nondet(0, 1)), inputs=[7])


class TestVerifier(unittest.TestCase):
    def test_safe_model(self):
        prog = seq(
            Assign("x", Nondet(0, 3)),
            Assert(Compare(">=", Var("x"), Const(0)), label="ok"),
        )
        result = Verifier().verify(prog)
        self.assertEqual(result.verdict, Verdict.SAFE)
        self.assertIsNone(result.counterexample)

    def test_unsafe_assertion_with_replayable_counterexample(self):
        prog = seq(
            Assign("x", Nondet(-2, 2)),
            Assert(Compare("!=", Var("x"), Const(0)), label="nz"),
        )
        result = Verifier().verify(prog)
        self.assertEqual(result.verdict, Verdict.UNSAFE)
        cex = result.counterexample
        self.assertEqual(cex.error_kind, "assertion")
        self.assertEqual(cex.choices, (0,))
        replayed = cex.replay(prog)
        self.assertIsInstance(replayed.error, lang.AssertionFailed)
        self.assertEqual(replayed.choices, cex.choices)

    def test_unsafe_division_by_zero_replays(self):
        prog = seq(
            Assign("d", Nondet(0, 2)),
            Assign("y", BinOp("/", Const(10), Var("d")), label="div"),
        )
        result = Verifier().verify(prog)
        self.assertEqual(result.verdict, Verdict.UNSAFE)
        cex = result.counterexample
        self.assertEqual(cex.error_kind, "division_by_zero")
        replayed = cex.replay(prog)
        self.assertIsInstance(replayed.error, lang.DivisionByZero)

    def test_division_by_zero_inside_expression_replays(self):
        # The failing nondet choice sits inside the divisor expression;
        # the counterexample must still capture it.
        prog = Assign("y", BinOp("/", Const(10), Nondet(-1, 1)), label="div")
        result = Verifier().verify(prog)
        self.assertEqual(result.verdict, Verdict.UNSAFE)
        cex = result.counterexample
        self.assertEqual(cex.choices, (0,))
        replayed = cex.replay(prog)
        self.assertIsInstance(replayed.error, lang.DivisionByZero)

    def test_unknown_when_budget_exceeded(self):
        prog = Assign("x", Nondet(0, 1000))
        result = Verifier(max_states=10).verify(prog)
        self.assertEqual(result.verdict, Verdict.UNKNOWN)
        self.assertIsNone(result.counterexample)

    def test_loop_model_checked_exhaustively(self):
        # x = 0; while x < 3: x = x + nondet(0, 1); assert x <= 3
        prog = seq(
            Assign("x", Const(0)),
            While(Compare("<", Var("x"), Const(3)),
                  Assign("x", BinOp("+", Var("x"), Nondet(0, 1)))),
            Assert(Compare("<=", Var("x"), Const(3)), label="bound"),
        )
        result = Verifier().verify(prog)
        self.assertEqual(result.verdict, Verdict.SAFE)

    def test_loop_counterexample_replays(self):
        # x = 0; while x < 3: x = x + nondet(0, 2); assert x <= 3  -- can hit 4
        prog = seq(
            Assign("x", Const(0)),
            While(Compare("<", Var("x"), Const(3)),
                  Assign("x", BinOp("+", Var("x"), Nondet(0, 2)))),
            Assert(Compare("<=", Var("x"), Const(3)), label="bound"),
        )
        result = Verifier().verify(prog)
        self.assertEqual(result.verdict, Verdict.UNSAFE)
        replayed = result.counterexample.replay(prog)
        self.assertIsInstance(replayed.error, lang.AssertionFailed)
        self.assertEqual(replayed.choices, result.counterexample.choices)


if __name__ == "__main__":
    unittest.main()
