import unittest

from callbyneed.errors import (
    BlackHoleError,
    DivByZeroError,
    UnboundNameError,
)
from callbyneed.interpreter import Interpreter


class TestArithmetic(unittest.TestCase):
    def run_src(self, src):
        return Interpreter().run(src)

    def test_basic_arithmetic(self):
        self.assertEqual(self.run_src("1 + 2 * 3"), 7)
        self.assertEqual(self.run_src("(1 + 2) * 3"), 9)
        self.assertEqual(self.run_src("10 - 4 - 3"), 3)
        self.assertEqual(self.run_src("20 / 5"), 4)
        self.assertEqual(self.run_src("-3 + 10"), 7)

    def test_let_bindings(self):
        self.assertEqual(self.run_src("let x = 6, y = x * 7 in y"), 42)

    def test_nested_let_and_shadowing(self):
        self.assertEqual(self.run_src("let x = 1 in let y = x + 1 in y"), 2)
        # recursive let: the inner binding shadows the outer one
        self.assertEqual(self.run_src("let x = 1 in let x = 2 in x"), 2)

    def test_unbound_name(self):
        with self.assertRaises(UnboundNameError) as ctx:
            self.run_src("let x = 1 in x + y")
        self.assertIn("unbound name 'y'", str(ctx.exception))


class TestCallByNeed(unittest.TestCase):
    def test_unused_division_by_zero_never_raises(self):
        interp = Interpreter()
        value = interp.run("let unused = 1 / 0, x = 40 + 2 in x")
        self.assertEqual(value, 42)
        thunk = interp.thunk_named("unused")
        self.assertEqual(thunk.state, "unevaluated")
        self.assertEqual(thunk.eval_count, 0)

    def test_thunk_referenced_ten_times_evaluated_once(self):
        src = "let x = 6 * 7 in " + " + ".join(["x"] * 10)
        interp = Interpreter()
        self.assertEqual(interp.run(src), 420)
        thunk = interp.thunk_named("x")
        self.assertEqual(thunk.state, "evaluated")
        self.assertEqual(thunk.eval_count, 1)
        self.assertEqual(thunk.value, 42)

    def test_shared_subexpression_across_bindings(self):
        interp = Interpreter()
        self.assertEqual(interp.run("let a = 2 * 3, b = a + 1, c = a * a in b + c"), 43)
        self.assertEqual(interp.thunk_named("a").eval_count, 1)

    def test_exception_is_cached_and_reraised_as_same_instance(self):
        interp = Interpreter()
        with self.assertRaises(DivByZeroError):
            interp.run("let bad = 1 / 0 in bad + bad")
        thunk = interp.thunk_named("bad")
        self.assertEqual(thunk.state, "failed")
        self.assertEqual(thunk.eval_count, 1)  # evaluated exactly once
        first_error = thunk.error
        self.assertIsInstance(first_error, DivByZeroError)
        with self.assertRaises(DivByZeroError) as ctx:
            interp.force(thunk)  # forcing again must not re-evaluate
        self.assertIs(ctx.exception, first_error)
        self.assertEqual(thunk.eval_count, 1)

    def test_division_by_zero_span_is_utf8_byte_range(self):
        src = "# é\nlet bad = 1 / 0 in bad"
        interp = Interpreter()
        with self.assertRaises(DivByZeroError) as ctx:
            interp.run(src)
        encoded = src.encode("utf-8")
        start = encoded.index(b"1 / 0")
        self.assertEqual(ctx.exception.span, (start, start + 5))


class TestBlackHole(unittest.TestCase):
    def test_direct_self_reference(self):
        interp = Interpreter()
        with self.assertRaises(BlackHoleError) as ctx:
            interp.run("let x = x in x")
        self.assertEqual(ctx.exception.cycle, ["x", "x"])
        self.assertIn("x -> x", str(ctx.exception))

    def test_indirect_self_reference(self):
        interp = Interpreter()
        with self.assertRaises(BlackHoleError) as ctx:
            interp.run("let x = y + 1, y = x + 1 in x")
        self.assertEqual(ctx.exception.cycle, ["x", "y", "x"])
        self.assertIn("black hole: cyclic dependency x -> y -> x", str(ctx.exception))

    def test_longer_cycle(self):
        interp = Interpreter()
        with self.assertRaises(BlackHoleError) as ctx:
            interp.run("let a = b, b = c, c = a in a")
        self.assertEqual(ctx.exception.cycle, ["a", "b", "c", "a"])

    def test_cycle_error_is_cached_too(self):
        interp = Interpreter()
        with self.assertRaises(BlackHoleError):
            interp.run("let x = x + 1 in x")
        thunk = interp.thunk_named("x")
        self.assertEqual(thunk.state, "failed")
        self.assertEqual(thunk.eval_count, 1)
        with self.assertRaises(BlackHoleError) as ctx:
            interp.force(thunk)
        self.assertIs(ctx.exception, thunk.error)
        self.assertEqual(thunk.eval_count, 1)


class TestTrace(unittest.TestCase):
    def test_trace_shows_forcing_chain(self):
        interp = Interpreter(trace=True)
        self.assertEqual(interp.run("let x = y + 1, y = 2 in x"), 3)
        lines = interp.trace_lines
        self.assertEqual(lines[0], "-> force x  (bytes 4..5)")
        self.assertEqual(lines[1], "  -> force y  (bytes 15..16)")
        self.assertEqual(lines[2], "  <- y = 2")
        self.assertEqual(lines[3], "<- x = 3")

    def test_trace_marks_cached_hits(self):
        interp = Interpreter(trace=True)
        self.assertEqual(interp.run("let x = 2 in x + x"), 4)
        self.assertIn("~~ x  (cached value 2)", interp.trace_lines)

    def test_trace_marks_cached_errors(self):
        interp = Interpreter(trace=True)
        with self.assertRaises(DivByZeroError):
            interp.run("let bad = 1 / 0 in bad")
        interp.force  # silence linters about unused attr
        with self.assertRaises(DivByZeroError):
            interp.force(interp.thunk_named("bad"))
        self.assertIn("~~ bad  (cached error DivByZeroError)", interp.trace_lines)
        self.assertTrue(any("!! bad raised DivByZeroError" in l for l in interp.trace_lines))

    def test_trace_of_black_hole_shows_chain_before_error(self):
        interp = Interpreter(trace=True)
        with self.assertRaises(BlackHoleError):
            interp.run("let x = y + 1, y = x + 1 in x")
        lines = interp.trace_lines
        self.assertEqual(lines[0], "-> force x  (bytes 4..5)")
        self.assertEqual(lines[1], "  -> force y  (bytes 15..16)")

    def test_no_trace_lines_when_disabled(self):
        interp = Interpreter(trace=False)
        interp.run("let x = 1 in x")
        self.assertEqual(interp.trace_lines, [])


if __name__ == "__main__":
    unittest.main()
