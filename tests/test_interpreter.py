import io
import unittest

from minilang import Interpreter, parse_source
from minilang.interpreter import InterpError


def run(src):
    out = []
    interp = Interpreter(out=out.append)
    interp.run(parse_source(src))
    return out, interp.env


class TestInterpreter(unittest.TestCase):
    def test_arithmetic_and_precedence(self):
        out, env = run("x = 1 + 2 * 3; y = (1 + 2) * 3; print x; print y;")
        self.assertEqual(out, ["7", "9"])

    def test_division_truncates_and_mod(self):
        out, _ = run("print 7 / 2; print -7 / 2; print 7 % 3;")
        self.assertEqual(out, ["3", "-3", "1"])

    def test_comparisons(self):
        out, _ = run("print 1 < 2; print 2 == 2; print 2 != 2;")
        self.assertEqual(out, ["1", "1", "0"])

    def test_if_else(self):
        out, _ = run("a = 5; if (a > 3) { print 1; } else { print 0; }")
        self.assertEqual(out, ["1"])

    def test_while_factorial(self):
        src = "n = 5; acc = 1; i = 1; while (i <= n) { acc = acc * i; i = i + 1; } print acc;"
        out, _ = run(src)
        self.assertEqual(out, ["120"])

    def test_while_zero_iterations(self):
        out, env = run("x = 0; while (x > 0) { x = x - 1; } print x;")
        self.assertEqual(out, ["0"])

    def test_undefined_variable_raises_with_span(self):
        with self.assertRaises(InterpError) as ctx:
            run("print missing;")
        self.assertEqual(ctx.exception.span, (6, 13))

    def test_division_by_zero(self):
        with self.assertRaises(InterpError):
            run("x = 1 / 0;")


if __name__ == "__main__":
    unittest.main()
