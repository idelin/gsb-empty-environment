"""unittest suite for MiniLang. Run with: python3.11 -m unittest discover -s tests -v"""
import unittest

from minilang import (
    LangThrow,
    LexError,
    Lexer,
    ParseError,
    Parser,
    RuntimeFault,
    run_source,
)
from minilang.ast_nodes import LetStmt, TryStmt


def run(src):
    """Run source, returning the list of printed lines."""
    out = []
    run_source(src, out=out.append)
    return out


class LexerTest(unittest.TestCase):
    def test_ascii_byte_spans(self):
        tokens = Lexer("let x = 42;").tokenize()
        self.assertEqual(
            [(t.kind, t.start, t.end) for t in tokens],
            [
                ("LET", 0, 3),
                ("IDENT", 4, 5),
                ("EQUAL", 6, 7),
                ("NUMBER", 8, 10),
                ("SEMICOLON", 10, 11),
                ("EOF", 11, 11),
            ],
        )

    def test_utf8_byte_spans(self):
        # 名 is 3 UTF-8 bytes, é is 2: spans must be byte ranges, not chars.
        tokens = Lexer('let 名 = "hé";').tokenize()
        kinds = [(t.kind, t.lexeme, t.start, t.end) for t in tokens]
        self.assertEqual(kinds[0], ("LET", "let", 0, 3))
        self.assertEqual(kinds[1], ("IDENT", "名", 4, 7))
        self.assertEqual(kinds[2], ("EQUAL", "=", 8, 9))
        self.assertEqual(kinds[3], ("STRING", '"hé"', 10, 15))
        self.assertEqual(kinds[4], ("SEMICOLON", ";", 15, 16))
        self.assertEqual(kinds[5], ("EOF", "", 16, 16))
        self.assertEqual(tokens[3].value, "hé")

    def test_string_escapes(self):
        tokens = Lexer(r'let s = "a\nb\t\"c\\";').tokenize()
        self.assertEqual(tokens[3].value, 'a\nb\t"c\\')

    def test_unterminated_string_reports_byte_span(self):
        with self.assertRaises(LexError) as ctx:
            Lexer('let s = "abc').tokenize()
        self.assertEqual((ctx.exception.start, ctx.exception.end), (8, 12))

    def test_comments_and_block_comments(self):
        src = "let a = 1; // 注释\n/* 多字节 */ let b = 2;"
        tokens = [t for t in Lexer(src).tokenize() if t.kind == "IDENT"]
        self.assertEqual([t.lexeme for t in tokens], ["a", "b"])
        # 'b' starts after the multi-byte comment; verify byte offset.
        self.assertEqual(src.encode("utf-8")[tokens[1].start:tokens[1].end], b"b")


class ParserTest(unittest.TestCase):
    def parse(self, src):
        return Parser(Lexer(src).tokenize()).parse()

    def test_let_span_covers_whole_statement(self):
        program = self.parse("let x = 1;")
        stmt = program.stmts[0]
        self.assertIsInstance(stmt, LetStmt)
        self.assertEqual((stmt.start, stmt.end), (0, 10))

    def test_try_structure(self):
        program = self.parse("try { throw 1; } catch (e) { } finally { }")
        stmt = program.stmts[0]
        self.assertIsInstance(stmt, TryStmt)
        self.assertEqual(stmt.catch_param, "e")
        self.assertIsNotNone(stmt.catch_body)
        self.assertIsNotNone(stmt.finally_body)

    def test_try_requires_catch_or_finally(self):
        with self.assertRaises(ParseError):
            self.parse("try { }")

    def test_invalid_assignment_target(self):
        with self.assertRaises(ParseError) as ctx:
            self.parse("1 = 2;")
        self.assertIn("invalid assignment target", ctx.exception.message)

    def test_missing_semicolon_reports_span(self):
        with self.assertRaises(ParseError) as ctx:
            self.parse("let x = 1")
        self.assertEqual((ctx.exception.start, ctx.exception.end), (9, 9))


class BasicsTest(unittest.TestCase):
    def test_arithmetic_and_precedence(self):
        self.assertEqual(run("print(2 + 3 * 4);"), ["14"])
        self.assertEqual(run("print((2 + 3) * 4);"), ["20"])
        self.assertEqual(run("print(7 / 2);"), ["3.5"])
        self.assertEqual(run("print(7 % 3);"), ["1"])
        self.assertEqual(run("print(-3 + 1);"), ["-2"])

    def test_strings_and_booleans(self):
        self.assertEqual(run('print("a" + "b");'), ["ab"])
        self.assertEqual(run("print(true && false);"), ["false"])
        self.assertEqual(run("print(false || true);"), ["true"])
        self.assertEqual(run("print(!nil);"), ["true"])
        self.assertEqual(run("print(1 == 1.0);"), ["true"])
        self.assertEqual(run('print(1 == "1");'), ["false"])
        self.assertEqual(run("print(true == 1);"), ["false"])

    def test_variables_and_scope(self):
        out = run(
            """
            let x = 1;
            {
                let x = 2;
                print(x);
            }
            print(x);
            """
        )
        self.assertEqual(out, ["2", "1"])

    def test_undefined_variable_fault_has_span(self):
        with self.assertRaises(RuntimeFault) as ctx:
            run("let a = 1;\nnope;")
        err = ctx.exception
        self.assertIn("undefined variable 'nope'", err.message)
        self.assertEqual((err.start, err.end), (11, 15))

    def test_if_while_for(self):
        out = run(
            """
            let total = 0;
            for (let i = 1; i <= 4; i = i + 1) {
                if (i % 2 == 0) { total = total + i; }
            }
            let n = 3;
            while (n > 0) { total = total + 1; n = n - 1; }
            print(total);
            """
        )
        self.assertEqual(out, ["9"])

    def test_break_and_continue(self):
        out = run(
            """
            for (let i = 0; i < 10; i = i + 1) {
                if (i == 2) { continue; }
                if (i == 5) { break; }
                print(i);
            }
            """
        )
        self.assertEqual(out, ["0", "1", "3", "4"])

    def test_functions_recursion_closures(self):
        out = run(
            """
            fn fib(n) {
                if (n < 2) { return n; }
                return fib(n - 1) + fib(n - 2);
            }
            print(fib(10));
            fn make() {
                let c = 0;
                fn inc() { c = c + 1; return c; }
                return inc;
            }
            let f = make();
            f(); f();
            print(f());
            """
        )
        self.assertEqual(out, ["55", "3"])

    def test_arity_fault(self):
        with self.assertRaises(RuntimeFault) as ctx:
            run("fn f(a, b) { return a; } f(1);")
        self.assertIn("expects 2 argument(s), got 1", ctx.exception.message)

    def test_break_outside_loop_is_fault(self):
        with self.assertRaises(RuntimeFault) as ctx:
            run("break;")
        self.assertIn("outside of a loop", ctx.exception.message)

    def test_return_outside_function_is_fault(self):
        with self.assertRaises(RuntimeFault) as ctx:
            run("return 1;")
        self.assertIn("outside of a function", ctx.exception.message)

    def test_division_by_zero_is_catchable_fault(self):
        out = run(
            """
            try {
                let z = 1 / 0;
            } catch (err) {
                print("caught:", err);
            }
            """
        )
        self.assertEqual(out, ["caught: division by zero"])


class TryCatchFinallyTest(unittest.TestCase):
    def test_catch_binds_thrown_value(self):
        out = run(
            """
            try {
                throw "oops";
                print("unreachable");
            } catch (e) {
                print("got:", e);
            }
            print("after");
            """
        )
        self.assertEqual(out, ["got: oops", "after"])

    def test_finally_runs_on_normal_completion(self):
        out = run('try { print("body"); } finally { print("fin"); }')
        self.assertEqual(out, ["body", "fin"])

    def test_finally_runs_on_throw(self):
        out = run(
            """
            try {
                try { throw 1; } finally { print("fin"); }
            } catch (e) { print("caught", e); }
            """
        )
        self.assertEqual(out, ["fin", "caught 1"])

    def test_finally_runs_on_return(self):
        out = run(
            """
            fn f() {
                try { return "ret"; } finally { print("fin"); }
            }
            print(f());
            """
        )
        self.assertEqual(out, ["fin", "ret"])

    def test_finally_runs_on_break_and_continue(self):
        out = run(
            """
            for (let i = 0; i < 3; i = i + 1) {
                try {
                    if (i == 0) { continue; }
                    if (i == 2) { break; }
                    print("body", i);
                } finally {
                    print("fin", i);
                }
            }
            """
        )
        self.assertEqual(out, ["fin 0", "body 1", "fin 1", "fin 2"])

    def test_return_value_computed_before_finally(self):
        # finally mutates external state, but the value already evaluated
        # by `return x` is what gets returned.
        out = run(
            """
            let x = 1;
            fn f() {
                try { return x; } finally { x = 99; }
            }
            print(f());
            print(x);
            """
        )
        self.assertEqual(out, ["1", "99"])

    def test_finally_return_overrides_try_return(self):
        out = run(
            """
            fn f() {
                try { return "try"; } finally { return "finally"; }
            }
            print(f());
            """
        )
        self.assertEqual(out, ["finally"])

    def test_finally_throw_overrides_try_throw_and_keeps_cause(self):
        src = 'try { throw "original"; } finally { throw "from finally"; }'
        with self.assertRaises(LangThrow) as ctx:
            run(src)
        err = ctx.exception
        self.assertEqual(err.value, "from finally")
        self.assertIsNotNone(err.cause)
        self.assertEqual(err.cause.value, "original")
        self.assertIsNone(err.cause.cause)

    def test_finally_throw_overrides_pending_return(self):
        src = """
        fn f() {
            try { return 1; } finally { throw "boom"; }
        }
        try { f(); } catch (e) { print("caught:", e); }
        """
        self.assertEqual(run(src), ["caught: boom"])

    def test_finally_break_overrides_pending_return(self):
        out = run(
            """
            fn f() {
                for (let i = 0; i < 5; i = i + 1) {
                    try {
                        if (i == 1) { return "from try"; }
                    } finally {
                        if (i == 1) { break; }
                    }
                }
                return "after loop";
            }
            print(f());
            """
        )
        self.assertEqual(out, ["after loop"])

    def test_normal_finally_lets_pending_transfer_through(self):
        out = run(
            """
            fn f() {
                try { return "kept"; } finally { print("cleanup"); }
            }
            print(f());
            """
        )
        self.assertEqual(out, ["cleanup", "kept"])

    def test_nested_finally_runs_inside_out(self):
        out = []
        src = """
        try {
            try {
                try { throw "x"; } finally { print("inner"); }
            } finally { print("middle"); }
        } finally { print("outer"); }
        """
        with self.assertRaises(LangThrow):
            run_source(src, out=out.append)
        self.assertEqual(out, ["inner", "middle", "outer"])

    def test_nested_cause_chain(self):
        src = """
        try {
            try {
                try { throw "first"; }
                finally { throw "second"; }
            } finally { throw "third"; }
        } finally { print("outermost"); }
        """
        out = []
        with self.assertRaises(LangThrow) as ctx:
            run_source(src, out=out.append)
        self.assertEqual(out, ["outermost"])
        err = ctx.exception
        self.assertEqual(err.value, "third")
        self.assertEqual(err.cause.value, "second")
        self.assertEqual(err.cause.cause.value, "first")
        self.assertIsNone(err.cause.cause.cause)

    def test_inner_finally_return_recovers_throw_outer_still_runs(self):
        out = run(
            """
            fn f() {
                try {
                    try { throw "gone"; }
                    finally { return "recovered"; }
                } finally { print("outer fin"); }
            }
            print(f());
            """
        )
        self.assertEqual(out, ["outer fin", "recovered"])

    def test_catch_rethrow_and_outer_catch(self):
        out = run(
            """
            try {
                try {
                    throw "inner error";
                } catch (e) {
                    throw "wrapped: " + e;
                }
            } catch (e2) {
                print(e2);
            }
            """
        )
        self.assertEqual(out, ["wrapped: inner error"])

    def test_catch_does_not_catch_break_or_return(self):
        out = run(
            """
            fn f() {
                while (true) {
                    try {
                        try { break; } catch (e) { print("no"); }
                    } catch (e2) { print("no2"); }
                }
                return "done";
            }
            print(f());
            """
        )
        self.assertEqual(out, ["done"])

    def test_uncaught_throw_carries_byte_span(self):
        src = 'let a = 1;\nthrow "x";'
        with self.assertRaises(LangThrow) as ctx:
            run(src)
        err = ctx.exception
        self.assertEqual(err.value, "x")
        # `throw "x";` starts at byte 11 and the statement spans 11..21.
        self.assertEqual((err.start, err.end), (11, 21))
        self.assertEqual(src.encode("utf-8")[err.start:err.end], b'throw "x";')

    def test_throw_span_with_multibyte_source(self):
        src = 'let 名 = "值";\nthrow 名;'
        with self.assertRaises(LangThrow) as ctx:
            run(src)
        err = ctx.exception
        self.assertEqual(err.value, "值")
        self.assertEqual(
            src.encode("utf-8")[err.start:err.end], "throw 名;".encode("utf-8")
        )


if __name__ == "__main__":
    unittest.main()
