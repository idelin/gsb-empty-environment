import unittest

from minicoro import (
    Coroutine, Interpreter, LangError, ReentrancyError, Step, parse, tokenize,
)


def byte_offset(source, char_index):
    return len(source[:char_index].encode("utf-8"))


class LexerTest(unittest.TestCase):
    def test_utf8_byte_spans(self):
        src = '# 注释\nlet 甲 = "hé"\n'
        tokens = tokenize(src)
        let_tok = tokens[0]
        self.assertEqual(let_tok.text, "let")
        self.assertEqual(let_tok.start, byte_offset(src, src.index("let")))
        self.assertEqual(let_tok.end, let_tok.start + 3)
        name_tok = tokens[1]
        self.assertEqual(name_tok.text, "甲")
        self.assertEqual(name_tok.start, byte_offset(src, src.index("甲")))
        self.assertEqual(name_tok.end, name_tok.start + len("甲".encode("utf-8")))
        str_tok = tokens[3]
        self.assertEqual(str_tok.value, "hé")
        self.assertEqual(str_tok.start, byte_offset(src, src.index('"hé"')))
        self.assertEqual(str_tok.end, byte_offset(src, src.index('"hé"') + len('"hé"')))

    def test_ast_byte_spans(self):
        src = "let x = 1 + 2  # 加法\n"
        program = parse(src)
        let_node = program.stmts[0]
        self.assertEqual(let_node.start, 0)
        binop = let_node.expr
        self.assertEqual(src.encode("utf-8")[binop.start:binop.end], b"1 + 2")


class CoroutineTest(unittest.TestCase):
    def test_yield_and_send_values(self):
        it = Interpreter()
        it.run("fn f() { let x = yield 1  yield x + 1  return 99 }")
        co = it.invoke(it.get("f"), [])
        self.assertIsInstance(co, Coroutine)
        self.assertEqual(co.resume(), Step(False, 1))
        self.assertEqual(co.resume(10), Step(False, 11))
        self.assertEqual(co.resume(), Step(True, 99))
        self.assertEqual(co.state, "done")

    def test_terminal_state_after_finish(self):
        it = Interpreter()
        it.run("fn f() { yield 1  return 99 }")
        co = it.invoke(it.get("f"), [])
        co.resume()
        self.assertEqual(co.resume(), Step(True, 99))
        # 完成协程再次 resume：返回明确终态，不报错
        self.assertEqual(co.resume(), Step(True, 99))
        self.assertEqual(co.resume(), Step(True, 99))
        self.assertEqual(co.state, "done")

    def test_interleaved_coroutines_isolated_locals(self):
        it = Interpreter()
        it.run("""
fn counter(n) {
    let i = 0
    let acc = 100
    while i < n {
        acc = acc + i
        yield acc
        i = i + 1
    }
    return acc
}
""")
        make = it.get("counter")
        a = it.invoke(make, [2])
        b = it.invoke(make, [3])
        self.assertEqual(a.resume(), Step(False, 100))
        self.assertEqual(b.resume(), Step(False, 100))
        self.assertEqual(a.resume(), Step(False, 101))
        self.assertEqual(b.resume(), Step(False, 101))
        self.assertEqual(a.resume(), Step(True, 101))
        self.assertEqual(b.resume(), Step(False, 103))
        self.assertEqual(b.resume(), Step(True, 103))

    def test_suspended_stack_frames_preserved(self):
        it = Interpreter()
        it.run("""
fn double(x) { return x * 2 }
fn f() {
    let total = 0
    let i = 0
    while i < 3 {
        total = total + double(i)
        let got = yield total
        total = total + got
        i = i + 1
    }
    return total
}
""")
        co = it.invoke(it.get("f"), [])
        self.assertEqual(co.resume(), Step(False, 0))
        self.assertEqual(co.resume(10), Step(False, 12))
        self.assertEqual(co.resume(100), Step(False, 116))
        self.assertEqual(co.resume(1000), Step(True, 1116))

    def test_reentry_forbidden(self):
        it = Interpreter()
        it.run("fn f() { resume(me)  yield 1 }")
        co = it.invoke(it.get("f"), [])
        it.set_global("me", co)
        with self.assertRaises(ReentrancyError):
            co.resume()
        self.assertEqual(co.state, "done")

    def test_close_runs_finally_once(self):
        it = Interpreter()
        it.run("""
let log = []
fn f() {
    try {
        yield 1
        yield 2
    } finally {
        push(log, "cleaned")
    }
}
""")
        co = it.invoke(it.get("f"), [])
        co.resume()
        co.close()
        co.close()
        self.assertEqual(it.get("log"), ["cleaned"])
        self.assertEqual(co.state, "closed")
        self.assertEqual(co.resume(), Step(True, None))

    def test_exception_unwinds_finally_stack(self):
        it = Interpreter()
        it.run("""
let log = []
fn f() {
    try {
        try {
            yield 1
            let z = 1 / 0
        } finally {
            push(log, "inner")
        }
    } finally {
        push(log, "outer")
    }
}
""")
        co = it.invoke(it.get("f"), [])
        co.resume()
        with self.assertRaises(LangError) as ctx:
            co.resume()
        self.assertIn("division by zero", str(ctx.exception))
        self.assertEqual(it.get("log"), ["inner", "outer"])
        self.assertEqual(co.state, "done")

    def test_yield_outside_coroutine_rejected(self):
        with self.assertRaises(LangError):
            Interpreter().run("yield 1")

    def test_inlanguage_resume_pair_and_state(self):
        it = Interpreter()
        it.run("""
fn g() { yield 7  return "end" }
fn main() {
    let c = g()
    let r1 = resume(c)
    let r2 = resume(c)
    let r3 = resume(c)
    return [r1, r2, r3, state(c)]
}
""")
        result = it.invoke(it.get("main"), [])
        self.assertEqual(result, [[False, 7], [True, "end"], [True, "end"], "done"])

    def test_nested_function_yield_scope(self):
        it = Interpreter()
        it.run("""
fn outer() {
    fn inner() { yield 1 }
    return inner
}
""")
        inner = it.invoke(it.get("outer"), [])
        co = it.invoke(inner, [])
        self.assertIsInstance(co, Coroutine)
        self.assertEqual(co.resume(), Step(False, 1))
        self.assertEqual(co.resume(), Step(True, None))


class LanguageTest(unittest.TestCase):
    def test_control_flow(self):
        it = Interpreter()
        it.run("""
fn classify(n) {
    if n < 0 { return "neg" } elif n == 0 { return "zero" } else { return "pos" }
}
fn sum_odd(n) {
    let i = 0
    let total = 0
    while true {
        i = i + 1
        if i > n { break }
        if i % 2 == 0 { continue }
        total = total + i
    }
    return total
}
""")
        self.assertEqual(it.invoke(it.get("classify"), [-5]), "neg")
        self.assertEqual(it.invoke(it.get("classify"), [0]), "zero")
        self.assertEqual(it.invoke(it.get("classify"), [9]), "pos")
        self.assertEqual(it.invoke(it.get("sum_odd"), [10]), 25)

    def test_runtime_error_reports_byte_span(self):
        src = "let x = missing_var + 1"
        with self.assertRaises(LangError) as ctx:
            Interpreter().run(src)
        msg = str(ctx.exception)
        self.assertIn("undefined variable 'missing_var'", msg)
        start = byte_offset(src, src.index("missing_var"))
        self.assertIn(f"bytes {start}..{start + 11}", msg)

    def test_return_outside_function_rejected(self):
        with self.assertRaises(LangError):
            Interpreter().run("return 1")


if __name__ == "__main__":
    unittest.main()
