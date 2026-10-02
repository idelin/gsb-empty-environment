import math
import unittest

from interval_analysis.analyzer import Analyzer
from interval_analysis.interval import Interval
from interval_analysis.lang import (
    Assert, Assign, BinOp, Compare, Const, If, Nondet, Var, While, seq,
)


def analyze(program, **kwargs):
    return Analyzer(**kwargs).analyze(program)


class TestDiagnostics(unittest.TestCase):
    def test_definite_division_by_zero(self):
        prog = seq(
            Assign("x", Const(0)),
            Assign("y", BinOp("/", Const(1), Var("x")), label="div"),
        )
        result = analyze(prog)
        self.assertEqual(len(result.diagnostics), 1)
        diag = result.diagnostics[0]
        self.assertEqual(diag.kind, "division_by_zero")
        self.assertEqual(diag.certainty, "definite")
        self.assertEqual(diag.label, "div")

    def test_possible_division_by_zero_is_marked_possible(self):
        prog = Assign("y", BinOp("/", Const(10), Nondet(-2, 3)), label="div")
        result = analyze(prog)
        self.assertEqual(len(result.diagnostics), 1)
        self.assertEqual(result.diagnostics[0].certainty, "possible")

    def test_no_alarm_when_divisor_excludes_zero(self):
        prog = seq(
            Assign("d", Nondet(1, 5)),
            Assign("y", BinOp("/", Const(10), Var("d"))),
        )
        self.assertEqual(analyze(prog).diagnostics, [])

    def test_definite_and_possible_assertions(self):
        definite = analyze(Assert(Const(0), label="a"))
        self.assertEqual(definite.diagnostics[0].certainty, "definite")
        possible = analyze(seq(
            Assign("x", Nondet(0, 1)),
            Assert(Compare("==", Var("x"), Const(1)), label="b"),
        ))
        self.assertEqual(possible.diagnostics[0].certainty, "possible")
        ok = analyze(seq(
            Assign("x", Nondet(0, 1)),
            Assert(Compare(">=", Var("x"), Const(0))),
        ))
        self.assertEqual(ok.diagnostics, [])


class TestBranchContraction(unittest.TestCase):
    def test_then_branch_is_refined(self):
        prog = seq(
            Assign("x", Nondet(0, 10)),
            If(Compare(">", Var("x"), Const(5)),
               Assign("y", Const(1)),
               Assign("y", Const(0))),
        )
        # After the join y is [0,1]; x is back to [0,10].
        result = analyze(prog)
        self.assertEqual(result.exit_state["y"], Interval.of(0, 1))
        self.assertEqual(result.exit_state["x"], Interval.of(0, 10))

    def test_neq_zero_straddling_interval_is_not_pretended_exact(self):
        # x in [-3,3]; the x != 0 branch cannot drop 0 exactly, so the
        # analysis must keep 0 (sound) and flag both the imprecision and
        # the resulting possible division by zero.
        prog = seq(
            Assign("x", Nondet(-3, 3)),
            If(Compare("!=", Var("x"), Const(0)),
               Assign("y", BinOp("/", Const(1), Var("x")), label="div"),
               label="guard"),
        )
        result = analyze(prog)
        self.assertTrue(result.precision_notes,
                        "imprecise refinement must be reported")
        alarms = [d for d in result.diagnostics if d.kind == "division_by_zero"]
        self.assertEqual(len(alarms), 1)
        self.assertEqual(alarms[0].certainty, "possible")

    def test_neq_zero_at_boundary_is_exact(self):
        # x in [0,3]; x != 0 refines exactly to [1,3]: no alarm, no note.
        prog = seq(
            Assign("x", Nondet(0, 3)),
            If(Compare("!=", Var("x"), Const(0)),
               Assign("y", BinOp("/", Const(1), Var("x")))),
        )
        result = analyze(prog)
        self.assertEqual(result.diagnostics, [])
        self.assertEqual(result.precision_notes, [])

    def test_dead_branch_is_bottom(self):
        prog = seq(
            Assign("x", Const(3)),
            If(Compare("<", Var("x"), Const(0)),
               Assign("y", BinOp("/", Const(1), Const(0)))),
        )
        self.assertEqual(analyze(prog).diagnostics, [])


class TestLoops(unittest.TestCase):
    def counting_loop(self):
        return seq(
            Assign("x", Const(0)),
            While(Compare("<", Var("x"), Const(100)),
                  Assign("x", BinOp("+", Var("x"), Const(1))),
                  label="loop"),
        )

    def test_widening_terminates_and_overapproximates(self):
        result = analyze(self.counting_loop(), narrowing_iterations=0)
        self.assertEqual(len(result.fixpoints), 1)
        fp = result.fixpoints[0]
        self.assertTrue(fp.widening_applied)
        self.assertEqual(fp.invariant["x"], Interval.of(0, math.inf))
        self.assertEqual(result.exit_state["x"], Interval.of(100, math.inf))

    def test_narrowing_recovers_precision(self):
        result = analyze(self.counting_loop(), narrowing_iterations=3)
        fp = result.fixpoints[0]
        self.assertTrue(fp.widening_applied)
        self.assertGreater(fp.narrowing_iterations, 0)
        self.assertEqual(fp.invariant["x"], Interval.of(0, 100))
        self.assertEqual(result.exit_state["x"], Interval.of(100, 100))

    def test_nondet_increment_loop(self):
        # x = 0; while x < 10: x = x + nondet(1, 3)
        prog = seq(
            Assign("x", Const(0)),
            While(Compare("<", Var("x"), Const(10)),
                  Assign("x", BinOp("+", Var("x"), Nondet(1, 3)))),
        )
        result = analyze(prog, narrowing_iterations=2)
        self.assertEqual(result.exit_state["x"], Interval.of(10, 12))

    def test_fixpoint_status_is_reported(self):
        result = analyze(self.counting_loop())
        fp = result.fixpoints[0]
        self.assertEqual(fp.label, "loop")
        self.assertGreater(fp.iterations, 0)


if __name__ == "__main__":
    unittest.main()
