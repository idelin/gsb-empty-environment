import unittest

from minicoro import Interpreter, LangError, ReentrancyError
from minicoro.lexer import Lexer


def make(source, name="main", args=()):
    interp = Interpreter()
    interp.load(source)
    return interp, interp.spawn(name, args)


class TestYieldResume(unittest.TestCase):
    def test_yield_values_and_send_back(self):
        _, co = make("""
            fn main() {
                let a = yield 1;
                let b = yield a + 1;
                return b * 10;
            }
        """)
        r1 = co.resume()
        self.assertEqual((r1.status, r1.value, r1.terminal), ("suspended", 1, False))
        r2 = co.resume(10)
        self.assertEqual((r2.status, r2.value), ("suspended", 11))
        r3 = co.resume(5)
        self.assertEqual((r3.status, r3.value, r3.terminal), ("returned", 50, True))

    def test_suspension_preserves_frames_across_calls_and_loops(self):
        _, co = make("""
            fn helper(x) {
                let y = yield x;
                return y + 1;
            }
            fn main() {
                let total = 0;
                let i = 0;
                while i < 3 {
                    total = total + helper(i * 10);
                    i = i + 1;
                }
                return total;
            }
        """)
        self.assertEqual(co.resume().value, 0)    # helper(0) yields 0
        self.assertEqual(co.resume(1).value, 10)  # helper returns 2
        self.assertEqual(co.resume(2).value, 20)  # helper returns 3
        r = co.resume(3)                          # helper returns 4
        self.assertEqual((r.status, r.value), ("returned", 9))  # 2+3+4

    def test_resume_after_completion_returns_terminal_state(self):
        _, co = make("fn main() { return 42; }")
        r1 = co.resume()
        self.assertEqual((r1.status, r1.value, r1.terminal), ("returned", 42, True))
        for _ in range(3):
            r = co.resume()
            self.assertTrue(r.terminal)
            self.assertEqual(r.status, "returned")
            self.assertEqual(r.value, 42)
        self.assertEqual(co.state, "returned")

    def test_first_resume_cannot_send_value(self):
        _, co = make("fn main() { yield 1; }")
        with self.assertRaises(ValueError):
            co.resume(5)


class TestCoroutineLocalState(unittest.TestCase):
    def test_interleaved_coroutines_do_not_share_locals(self):
        source = """
            fn counter(tag, step) {
                let i = 0;
                let acc = 0;
                while i < 3 {
                    acc = acc + step;
                    yield tag;
                    i = i + 1;
                }
                return acc;
            }
        """
        interp = Interpreter()
        interp.load(source)
        co_a = interp.spawn("counter", ["A", 10])
        co_b = interp.spawn("counter", ["B", 100])
        seen_a, seen_b = [], []
        ra = co_a.resume()
        rb = co_b.resume()
        while ra.status == "suspended" or rb.status == "suspended":
            if ra.status == "suspended":
                seen_a.append(ra.value)
                ra = co_a.resume()
            if rb.status == "suspended":
                seen_b.append(rb.value)
                rb = co_b.resume()
        self.assertEqual(seen_a, ["A", "A", "A"])
        self.assertEqual(seen_b, ["B", "B", "B"])
        self.assertEqual((ra.status, ra.value), ("returned", 30))
        self.assertEqual((rb.status, rb.value), ("returned", 300))


class TestReentrancy(unittest.TestCase):
    def test_resume_while_running_is_forbidden(self):
        interp = Interpreter()
        interp.load("fn main() { poke(); yield 1; }")
        co = interp.spawn("main")
        interp.define_builtin("poke", lambda args, it: co.resume())
        with self.assertRaises(ReentrancyError):
            co.resume()
        self.assertEqual(co.state, "failed")

    def test_close_and_throw_while_running_are_forbidden(self):
        interp = Interpreter()
        interp.load("fn main() { poke(); }")
        co = interp.spawn("main")
        interp.define_builtin("poke", lambda args, it: co.close())
        with self.assertRaises(ReentrancyError):
            co.resume()

        interp2 = Interpreter()
        interp2.load("fn main() { poke(); }")
        co2 = interp2.spawn("main")
        interp2.define_builtin("poke", lambda args, it: co2.throw("x"))
        with self.assertRaises(ReentrancyError):
            co2.resume()


class TestExceptionsAndClose(unittest.TestCase):
    def test_runtime_error_unwinds_finally(self):
        interp, co = make("""
            fn main() {
                try {
                    let x = 1 / 0;
                } finally {
                    cleanup();
                }
            }
        """)
        calls = []
        interp.define_builtin("cleanup", lambda args, it: calls.append("cleaned") or None)
        with self.assertRaises(LangError) as ctx:
            co.resume()
        self.assertIn("division by zero", str(ctx.exception))
        self.assertEqual(calls, ["cleaned"])
        self.assertEqual(co.state, "failed")
        r = co.resume()
        self.assertTrue(r.terminal)
        self.assertEqual(r.status, "failed")

    def test_throw_and_catch_binds_value_and_finally_runs(self):
        interp, co = make("""
            fn main() {
                try {
                    throw "boom";
                } catch e {
                    yield e;
                } finally {
                    cleanup();
                }
                return "ok";
            }
        """)
        calls = []
        interp.define_builtin("cleanup", lambda args, it: calls.append("cleaned") or None)
        r1 = co.resume()
        self.assertEqual((r1.status, r1.value), ("suspended", "boom"))
        r2 = co.resume()
        self.assertEqual((r2.status, r2.value), ("returned", "ok"))
        self.assertEqual(calls, ["cleaned"])

    def test_uncaught_throw_reports_span(self):
        source = "fn main() {\n  throw 7;\n}\n"
        _, co = make(source)
        with self.assertRaises(LangError) as ctx:
            co.resume()
        start = len("fn main() {\n  ".encode("utf-8"))
        end = start + len("throw 7;".encode("utf-8"))
        self.assertEqual(ctx.exception.span, (start, end))
        self.assertEqual(ctx.exception.value, 7)

    def test_host_throw_into_suspended_coroutine(self):
        _, co = make("""
            fn main() {
                try {
                    yield 1;
                } catch e {
                    return e;
                }
                return "unreachable";
            }
        """)
        self.assertEqual(co.resume().value, 1)
        r = co.throw("injected")
        self.assertEqual((r.status, r.value), ("returned", "injected"))

    def test_close_runs_finally_exactly_once(self):
        interp, co = make("""
            fn main() {
                try {
                    yield 1;
                    yield 2;
                } finally {
                    cleanup();
                }
            }
        """)
        calls = []
        interp.define_builtin("cleanup", lambda args, it: calls.append("cleaned") or None)
        self.assertEqual(co.resume().value, 1)
        r = co.close()
        self.assertEqual((r.status, r.terminal), ("closed", True))
        self.assertEqual(calls, ["cleaned"])
        # second close: no-op, finally does not run again
        r2 = co.close()
        self.assertEqual((r2.status, r2.terminal), ("closed", True))
        self.assertEqual(calls, ["cleaned"])
        # resume after close: terminal state, coroutine does not restart
        r3 = co.resume()
        self.assertTrue(r3.terminal)
        self.assertEqual(r3.status, "closed")
        self.assertEqual(calls, ["cleaned"])

    def test_close_nested_finally_unwinds_whole_stack(self):
        interp, co = make("""
            fn inner() {
                try {
                    yield "deep";
                } finally {
                    cleanup("inner");
                }
            }
            fn main() {
                try {
                    inner();
                } finally {
                    cleanup("outer");
                }
            }
        """)
        calls = []
        interp.define_builtin(
            "cleanup", lambda args, it: calls.append(args[0]) or None)
        self.assertEqual(co.resume().value, "deep")
        co.close()
        self.assertEqual(calls, ["inner", "outer"])

    def test_close_on_returned_coroutine_is_noop(self):
        _, co = make("fn main() { return 1; }")
        co.resume()
        r = co.close()
        self.assertEqual(r.status, "returned")
        self.assertTrue(r.terminal)


class TestUtf8ByteSpans(unittest.TestCase):
    def test_lexer_tracks_utf8_byte_offsets(self):
        source = 'let 变量 = 1;'
        tokens = Lexer(source).tokenize()
        encoded = source.encode("utf-8")
        name_tok = tokens[1]
        self.assertEqual(name_tok.value, "变量")
        self.assertEqual(encoded[name_tok.start:name_tok.end], "变量".encode("utf-8"))
        int_tok = tokens[3]
        self.assertEqual(encoded[int_tok.start:int_tok.end], b"1")

    def test_runtime_error_span_is_utf8_byte_range(self):
        source = 'fn main() {\n  // 中文注释占多字节\n  let x = 缺省;\n}\n'
        _, co = make(source)
        with self.assertRaises(LangError) as ctx:
            co.resume()
        encoded = source.encode("utf-8")
        start = encoded.index("缺省".encode("utf-8"))
        end = start + len("缺省".encode("utf-8"))
        self.assertEqual(ctx.exception.span, (start, end))
        self.assertIn("bytes %d..%d" % (start, end), str(ctx.exception))

    def test_syntax_error_span_is_utf8_byte_range(self):
        interp = Interpreter()
        source = 'fn main() {\n  // 注释\n  let x = ;\n}\n'
        with self.assertRaises(LangError) as ctx:
            interp.load(source)
        encoded = source.encode("utf-8")
        semi = encoded.index(b";")
        self.assertEqual(ctx.exception.span, (semi, semi + 1))


class TestLanguageBasics(unittest.TestCase):
    def run_main(self, source, args=()):
        _, co = make(source, args=args)
        while True:
            r = co.resume()
            if r.terminal:
                return r

    def test_arithmetic_and_precedence(self):
        r = self.run_main("fn main() { return 2 + 3 * 4 - 10 / 3; }")
        self.assertEqual(r.value, 2 + 12 - 3)

    def test_if_else_and_string_concat(self):
        r = self.run_main("""
            fn classify(n) {
                if n < 0 { return "neg"; }
                else if n == 0 { return "zero"; }
                else { return "pos"; }
            }
            fn main() {
                return classify(-5) + "," + classify(0) + "," + classify(9);
            }
        """)
        self.assertEqual(r.value, "neg,zero,pos")

    def test_recursion(self):
        r = self.run_main("""
            fn fib(n) {
                if n < 2 { return n; }
                return fib(n - 1) + fib(n - 2);
            }
            fn main() { return fib(10); }
        """)
        self.assertEqual(r.value, 55)

    def test_print_builtin_collects_output(self):
        interp, co = make('fn main() { print("hi", 1, true, null); }')
        co.resume()
        self.assertEqual(interp.output, ["hi 1 true null"])

    def test_undefined_variable_error(self):
        _, co = make("fn main() { return missing; }")
        with self.assertRaises(LangError) as ctx:
            co.resume()
        self.assertIn("undefined variable", str(ctx.exception))

    def test_assign_to_undefined_variable_error(self):
        _, co = make("fn main() { missing = 1; }")
        with self.assertRaises(LangError) as ctx:
            co.resume()
        self.assertIn("undefined variable", str(ctx.exception))


if __name__ == "__main__":
    unittest.main()
