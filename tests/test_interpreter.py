import unittest

from minilang.interpreter import RuntimeFailure, run
from minilang.parser import parse


def run_src(src):
    out = []
    run(parse(src), emit=out.append)
    return out


class InterpreterTests(unittest.TestCase):
    def test_arithmetic_and_precedence(self):
        self.assertEqual(run_src("print 1 + 2 * 3;"), ["7"])
        self.assertEqual(run_src("print (1 + 2) * 3;"), ["9"])
        self.assertEqual(run_src("print 7 / 2;"), ["3"])
        self.assertEqual(run_src("print 0 - 7 / 2;"), ["-3"])
        self.assertEqual(run_src("print 7 % 3;"), ["1"])
        self.assertEqual(run_src("print 0 - -5;"), ["5"])

    def test_comparisons_and_logic(self):
        self.assertEqual(run_src("print 1 < 2;"), ["1"])
        self.assertEqual(run_src("print 2 <= 1;"), ["0"])
        self.assertEqual(run_src("print 3 == 3;"), ["1"])
        self.assertEqual(run_src("print 3 != 3;"), ["0"])
        self.assertEqual(run_src("print !0;"), ["1"])
        self.assertEqual(run_src("print !9;"), ["0"])

    def test_if_else(self):
        src = "let a = 1; if a { print 10; } else { print 20; }"
        self.assertEqual(run_src(src), ["10"])
        src = "let a = 0; if a { print 10; } else { print 20; }"
        self.assertEqual(run_src(src), ["20"])

    def test_while_loop_sum(self):
        src = """
        let i = 1;
        let total = 0;
        while i <= 10 {
            total = total + i;
            i = i + 1;
        }
        print total;
        """
        self.assertEqual(run_src(src), ["55"])

    def test_while_zero_iterations(self):
        self.assertEqual(run_src("let i = 5; while i < 0 { print 1; } print 2;"), ["2"])

    def test_halt_stops_execution(self):
        self.assertEqual(run_src("print 1; halt; print 2;"), ["1"])

    def test_undefined_variable_runtime_error(self):
        with self.assertRaises(RuntimeFailure) as ctx:
            run_src("print missing;")
        self.assertIn("missing", str(ctx.exception))

    def test_division_by_zero(self):
        with self.assertRaises(RuntimeFailure):
            run_src("print 1 / 0;")
        with self.assertRaises(RuntimeFailure):
            run_src("print 1 % 0;")

    def test_step_limit(self):
        with self.assertRaises(RuntimeFailure) as ctx:
            run_src("while 1 { }")
        self.assertIn("step limit", str(ctx.exception))
