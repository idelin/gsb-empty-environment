import io
import unittest

from minilang.errors import LangError
from minilang.interpreter import Interpreter, ThrowSignal
from minilang.lexer import tokenize
from minilang.parser import Parser


def run(source):
    """Execute source; return (output_lines, error_or_None)."""
    out = io.StringIO()
    interp = Interpreter(out=out)
    program = Parser(tokenize(source)).parse_program()
    error = None
    try:
        interp.run(program)
    except (LangError, ThrowSignal) as exc:
        error = exc
    return out.getvalue().splitlines(), error


def output(source):
    lines, error = run(source)
    assert error is None, f"unexpected error: {error}"
    return lines


class BasicsTest(unittest.TestCase):
    def test_arithmetic_and_precedence(self):
        self.assertEqual(output("print(1 + 2 * 3); print((1 + 2) * 3);"),
                         ["7", "9"])

    def test_variables(self):
        self.assertEqual(output("let x = 1; x = x + 41; print(x);"), ["42"])

    def test_strings(self):
        self.assertEqual(output('print("foo" + "bar"); print(len("abc"));'),
                         ["foobar", "3"])

    def test_if_else_and_truthiness(self):
        src = """
        if (0) { print("no"); } else { print("yes"); }
        if ("s") { print("truthy"); }
        if (null) { print("no"); } else { print("null falsy"); }
        """
        self.assertEqual(output(src), ["yes", "truthy", "null falsy"])

    def test_logical_short_circuit(self):
        src = """
        fn boom() { print("evaluated"); return true; }
        print(false && boom());
        print(true || boom());
        """
        self.assertEqual(output(src), ["false", "true"])

    def test_functions_and_recursion(self):
        src = """
        fn fib(n) { if (n < 2) { return n; } return fib(n-1) + fib(n-2); }
        print(fib(10));
        """
        self.assertEqual(output(src), ["55"])

    def test_closures(self):
        src = """
        fn make_counter() {
          let n = 0;
          fn step() { n = n + 1; return n; }
          return step;
        }
        let c = make_counter();
        c(); c();
        print(c());
        """
        self.assertEqual(output(src), ["3"])

    def test_while_break_continue(self):
        src = """
        let i = 0; let acc = "";
        while (i < 6) {
          i = i + 1;
          if (i % 2 == 0) { continue; }
          if (i > 5) { break; }
          acc = acc + "x";
        }
        print(acc);
        """
        self.assertEqual(output(src), ["xxx"])

    def test_runtime_errors(self):
        _, err = run("print(nope);")
        self.assertIsInstance(err, LangError)
        self.assertIn("undefined variable", err.message)
        _, err = run("break;")
        self.assertIn("break outside loop", err.message)
        _, err = run("fn f(a) {} f(1, 2);")
        self.assertIn("expects 1 argument", err.message)
        _, err = run("print(1 / 0);")
        self.assertIn("division by zero", err.message)
        _, err = run("print(1 + \"s\");")
        self.assertIn("expects two numbers or two strings", err.message)

    def test_error_span_is_utf8_bytes(self):
        src = 'let 名 = 1;\nprint(名 + nope);'
        _, err = run(src)
        self.assertIsInstance(err, LangError)
        start, end = err.span
        self.assertEqual(src.encode("utf-8")[start:end].decode("utf-8"),
                         "nope")


class ThrowCatchTest(unittest.TestCase):
    def test_throw_and_catch(self):
        src = """
        try { throw "oops"; } catch (e) { print("got", e); }
        print("after");
        """
        self.assertEqual(output(src), ["got oops", "after"])

    def test_throw_skips_catch_of_wrong_nesting(self):
        src = """
        try {
          try { throw 7; } finally { print("inner finally"); }
        } catch (e) { print("outer catch", e); }
        """
        self.assertEqual(output(src), ["inner finally", "outer catch 7"])

    def test_catch_can_rethrow(self):
        src = """
        try {
          try { throw "first"; } catch (e) { throw e + "+second"; }
        } catch (e) { print(e); }
        """
        self.assertEqual(output(src), ["first+second"])

    def test_uncaught_throw_propagates(self):
        _, err = run('throw "raw";')
        self.assertIsInstance(err, ThrowSignal)
        self.assertEqual(err.value, "raw")

    def test_throw_carries_value_not_just_strings(self):
        _, err = run("throw 42;")
        self.assertEqual(err.value, 42)


class FinallyTest(unittest.TestCase):
    def test_finally_runs_on_normal_completion(self):
        self.assertEqual(output('try { print("a"); } finally { print("f"); }'),
                         ["a", "f"])

    def test_finally_runs_on_break_and_continue(self):
        src = """
        let i = 0;
        while (i < 2) {
          i = i + 1;
          try {
            if (i == 1) { continue; }
            break;
          } finally { print("finally", i); }
        }
        """
        self.assertEqual(output(src), ["finally 1", "finally 2"])

    def test_finally_runs_on_return(self):
        src = """
        fn f() { try { return "ret"; } finally { print("cleanup"); } }
        print(f());
        """
        self.assertEqual(output(src), ["cleanup", "ret"])

    def test_finally_runs_on_throw(self):
        src = """
        try {
          try { throw "x"; } finally { print("cleanup"); }
        } catch (e) { print("caught", e); }
        """
        self.assertEqual(output(src), ["cleanup", "caught x"])

    def test_nested_finally_order(self):
        src = """
        fn f() {
          try {
            try { return 1; } finally { print("inner"); }
          } finally { print("outer"); }
        }
        print(f());
        """
        self.assertEqual(output(src), ["inner", "outer", "1"])

    def test_return_snapshots_value_before_finally(self):
        # finally mutates outer state, but the already-evaluated return
        # value must be preserved.
        src = """
        let x = 1;
        fn f() { try { return x; } finally { x = 99; } }
        print(f());
        print(x);
        """
        self.assertEqual(output(src), ["1", "99"])

    def test_finally_throw_overrides_original_and_keeps_cause(self):
        src = 'try { throw "original"; } finally { throw "cleanup failed"; }'
        _, err = run(src)
        self.assertIsInstance(err, ThrowSignal)
        self.assertEqual(err.value, "cleanup failed")
        self.assertIsNotNone(err.cause)
        self.assertEqual(err.cause.value, "original")

    def test_cause_chain_across_multiple_finally(self):
        src = """
        try { throw "e1"; }
        finally {
          try { throw "e2"; } finally { throw "e3"; }
        }
        """
        _, err = run(src)
        self.assertEqual(err.value, "e3")
        self.assertEqual(err.cause.value, "e2")
        self.assertEqual(err.cause.cause.value, "e1")
        self.assertIsNone(err.cause.cause.cause)


class PriorityTest(unittest.TestCase):
    """throw(4) > return(3) > break(2) > continue(1) > normal(0);
    ties are won by the newer (finally) transfer."""

    def test_return_in_finally_overrides_break(self):
        src = """
        fn f() {
          while (true) {
            try { break; } finally { return "return wins"; }
          }
          return "loop ended";
        }
        print(f());
        """
        self.assertEqual(output(src), ["return wins"])

    def test_throw_in_finally_overrides_return(self):
        src = """
        fn f() { try { return "ret"; } finally { throw "thrown"; } }
        try { f(); } catch (e) { print("caught", e); }
        """
        self.assertEqual(output(src), ["caught thrown"])

    def test_equal_priority_newer_transfer_wins(self):
        src = """
        fn f() { try { return "try"; } finally { return "finally"; } }
        print(f());
        """
        self.assertEqual(output(src), ["finally"])

    def test_lower_priority_in_finally_does_not_override_throw(self):
        src = """
        fn f() {
          while (true) {
            try { throw "boom"; } finally { break; }
          }
          return "unreachable";
        }
        try { f(); } catch (e) { print("escaped:", e); }
        """
        self.assertEqual(output(src), ["escaped: boom"])

    def test_lower_priority_in_finally_does_not_override_return(self):
        src = """
        fn f() {
          while (true) {
            try { return "kept"; } finally { break; }
          }
          return "lost";
        }
        print(f());
        """
        self.assertEqual(output(src), ["kept"])

    def test_normal_finally_keeps_pending_transfer(self):
        src = """
        fn f() { try { return 5; } finally { print("side effect"); } }
        print(f());
        """
        self.assertEqual(output(src), ["side effect", "5"])

    def test_break_overrides_continue_in_finally(self):
        src = """
        let n = 0;
        while (true) {
          try { continue; } finally { break; }
        }
        print("done");
        """
        self.assertEqual(output(src), ["done"])


if __name__ == "__main__":
    unittest.main()
