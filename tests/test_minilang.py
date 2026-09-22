import random
import unittest

import minilang as ml
from minilang import ast_nodes as ast
from minilang.lexer import lex, FN, IDENT, NUMBER, EOF


def spans_ok(node, limit):
    """Every node span is sane and inside the source."""
    if not (0 <= node.start <= node.end <= limit):
        return False
    return all(spans_ok(c, limit) for c in node.child_nodes())


class LexerTests(unittest.TestCase):
    def test_basic_tokens(self):
        kinds = [t.kind for t in lex(b"fn f(x) = x + 1")]
        self.assertEqual(kinds, [FN, IDENT, "LPAREN", IDENT, "RPAREN",
                                 "EQUALS", IDENT, "PLUS", NUMBER, EOF])

    def test_positions_are_utf8_bytes(self):
        src = "# 评论\nfn f() = 1\n".encode("utf-8")
        tokens = lex(src)
        fn_tok = tokens[0]
        self.assertEqual(fn_tok.kind, FN)
        self.assertEqual(fn_tok.start, 9)  # '#'(1)+' '(1)+'评'(3)+'论'(3)+'\n'(1)
        num = [t for t in tokens if t.kind == NUMBER][0]
        self.assertEqual(src[num.start:num.end], b"1")

    def test_multibyte_junk_becomes_error_tokens(self):
        tokens = lex("é".encode("utf-8"))
        self.assertEqual([t.kind for t in tokens], ["ERROR", "ERROR", EOF])
        self.assertEqual(tokens[0].span if hasattr(tokens[0], "span") else
                         (tokens[0].start, tokens[0].end), (0, 1))


class ParserTests(unittest.TestCase):
    def test_precedence(self):
        tree = ml.parse("let x = 1 + 2 * 3\n")
        body = tree.decls[0].value
        self.assertIsInstance(body, ast.Binary)
        self.assertEqual(body.op, "+")
        self.assertEqual(body.right.op, "*")

    def test_parens_group(self):
        tree = ml.parse("let x = (1 + 2) * 3\n")
        body = tree.decls[0].value
        self.assertEqual(body.op, "*")
        self.assertIsInstance(body.left, ast.Group)
        self.assertEqual(body.left.inner.op, "+")

    def test_calls_and_chains(self):
        tree = ml.parse("let x = f(1, 2)(3)\n")
        call = tree.decls[0].value
        self.assertIsInstance(call, ast.Call)
        self.assertIsInstance(call.callee, ast.Call)
        self.assertEqual(len(call.callee.args), 2)

    def test_if_expression(self):
        tree = ml.parse("fn f(n) = if n < 2 then 1 else 0\n")
        self.assertIsInstance(tree.decls[0].body, ast.If)

    def test_spans_cover_source(self):
        src = "fn f(a, b) = a * (b + 1)\nlet y = f(2, 3)\n"
        tree = ml.parse(src)
        self.assertEqual(tree.span, (0, len(src.encode())))
        self.assertTrue(spans_ok(tree, len(src.encode())))
        f = tree.decls[0]
        self.assertEqual(src.encode()[f.start:f.end], b"fn f(a, b) = a * (b + 1)")


class ErrorRecoveryTests(unittest.TestCase):
    def test_unclosed_paren_does_not_swallow_next_decl(self):
        tree = ml.parse("fn f(x) = (x + 1\nfn g(y) = y * 2\n")
        self.assertEqual(len(tree.decls), 2)
        f, g = tree.decls
        self.assertIsInstance(f, ast.FnDecl)
        self.assertIsInstance(g, ast.FnDecl)
        errors = ml.collect_errors(tree)
        self.assertTrue(any("expected ')'" in e.message for e in errors))
        # g parsed cleanly and is usable
        self.assertIsInstance(g.body, ast.Binary)
        self.assertEqual(g.body.op, "*")

    def test_unclosed_call_does_not_swallow_next_decl(self):
        tree = ml.parse("fn f() = g(1\nfn h() = 2\n")
        self.assertEqual([type(d).__name__ for d in tree.decls],
                         ["FnDecl", "FnDecl"])
        call = tree.decls[0].body
        self.assertIsInstance(call, ast.Call)
        self.assertIsNotNone(call.error)
        self.assertEqual(tree.decls[1].body.value, 2)

    def test_junk_between_decls(self):
        tree = ml.parse("fn a() = 1\n@ @\nfn b() = 2\n")
        kinds = [type(d).__name__ for d in tree.decls]
        self.assertEqual(kinds, ["FnDecl", "ErrorNode", "FnDecl"])

    def test_missing_equals(self):
        tree = ml.parse("fn a(x) x + 1\nfn b() = 2\n")
        self.assertIsInstance(tree.decls[0], ast.ErrorNode)
        self.assertIsInstance(tree.decls[1], ast.FnDecl)

    def test_error_spans_are_byte_ranges(self):
        src = "fn f() = (1\n"
        tree = ml.parse(src)
        errors = ml.collect_errors(tree)
        self.assertEqual(len(errors), 1)
        self.assertEqual(errors[0].span, (11, 11))  # right after the "1"


class InterpreterTests(unittest.TestCase):
    def test_arithmetic_and_let(self):
        env = ml.run("let x = 2 + 3 * 4\nlet y = (x - 4) / 2\n")
        self.assertEqual(env.get("x"), 14)
        self.assertEqual(env.get("y"), 5)

    def test_recursion_factorial(self):
        env = ml.run("fn fact(n) = if n < 2 then 1 else n * fact(n - 1)\n")
        self.assertEqual(ml.evaluate_expression("fact(5)", env), 120)

    def test_function_calls_and_comparisons(self):
        env = ml.run("fn add(a, b) = a + b\nfn sign(x) = if x < 0 then -1 else 1\n")
        self.assertEqual(ml.evaluate_expression("add(2, 3) * sign(-7)", env), -5)

    def test_runtime_errors_have_spans(self):
        env = ml.run("fn f(x) = x + 1\n")
        with self.assertRaises(ml.LanguageError) as ctx:
            ml.evaluate_expression("nope(1)", env)
        self.assertIsNotNone(ctx.exception.start)
        with self.assertRaises(ml.LanguageError):
            ml.evaluate_expression("f(1, 2)", env)
        with self.assertRaises(ml.LanguageError):
            ml.evaluate_expression("1(2)", env)

    def test_interpret_refuses_erroneous_tree(self):
        tree = ml.parse("fn f() = (1\n")
        with self.assertRaises(ml.LanguageError):
            ml.interpret(tree)


class IncrementalTests(unittest.TestCase):
    SRC = "fn a(x) = x * 2\nfn b(y) = y + 10\nfn c(z) = z - 1\n"

    def test_number_edit_reuses_unrelated_functions(self):
        session = ml.Session(self.SRC)
        old_a, old_b, old_c = session.tree.decls
        old_c_span = old_c.span
        old_b_left = old_b.body.left
        start = self.SRC.index("10")
        tree = session.edit(start, start + 2, "200")  # delta = +1

        # Unrelated function declarations are the very same objects.
        self.assertIs(tree.decls[0], old_a)
        self.assertIs(tree.decls[2], old_c)
        self.assertIsNot(tree.decls[1], old_b)
        # Unchanged subtree *inside* the edited function is reused too.
        self.assertIs(tree.decls[1].body.left, old_b_left)
        # Offsets after the edit moved by the delta.
        self.assertEqual(tree.decls[2].span,
                         (old_c_span[0] + 1, old_c_span[1] + 1))
        self.assertEqual(tree.decls[0].span, old_a.span)
        # Incremental tree equals a full re-parse.
        self.assertEqual(tree, ml.parse(session.src))

    def test_edit_in_comment_keeps_everything(self):
        src = "fn a() = 1\n# 注释: hello\nfn b() = 2\n"
        session = ml.Session(src)
        old_decls = list(session.tree.decls)
        pos = src.encode().index("hello".encode())
        tree = session.edit(pos, pos + 5, "世界")
        self.assertEqual(tree.decls, old_decls)  # equal ...
        for new, old in zip(tree.decls, old_decls):
            self.assertIs(new, old)              # ... and identical
        self.assertEqual(tree, ml.parse(session.src))

    def test_insert_decl_in_middle(self):
        src = "fn a() = 1\nfn c() = 3\n"
        session = ml.Session(src)
        old_a = session.tree.decls[0]
        pos = src.index("fn c")
        tree = session.edit(pos, pos, "fn b() = 2\n")
        self.assertEqual([d.name for d in tree.decls], ["a", "b", "c"])
        self.assertIs(tree.decls[0], old_a)
        self.assertEqual(tree, ml.parse(session.src))

    def test_delete_decl(self):
        session = ml.Session(self.SRC)
        old_a, _, old_c = session.tree.decls
        start = self.SRC.index("fn b")
        end = self.SRC.index("fn c")
        tree = session.edit(start, end, "")
        self.assertEqual([d.name for d in tree.decls], ["a", "c"])
        self.assertIs(tree.decls[0], old_a)
        self.assertIs(tree.decls[1], old_c)
        self.assertEqual(tree, ml.parse(session.src))

    def test_edit_to_broken_syntax_matches_full_parse(self):
        session = ml.Session(self.SRC)
        start = self.SRC.index("x * 2")
        tree = session.edit(start, start + 5, "(x * ")
        errors = ml.collect_errors(tree)
        self.assertTrue(errors)
        self.assertEqual(tree, ml.parse(session.src))
        # a later declaration still parses after the broken one
        self.assertEqual(tree.decls[-1].name, "c")

    def test_edit_fixing_error_reuses_again(self):
        src = "fn f(x) = (x + 1\nfn g(y) = y * 2\n"
        session = ml.Session(src)
        old_g = session.tree.decls[1]
        pos = src.index("\nfn g")
        tree = session.edit(pos, pos, ")")
        self.assertEqual(ml.collect_errors(tree), [])
        self.assertIs(tree.decls[1], old_g)
        self.assertEqual(tree, ml.parse(session.src))
        env = ml.interpret(tree)
        self.assertEqual(ml.evaluate_expression("g(21)", env), 42)

    def test_utf8_boundary_validation(self):
        src = "# 评论\nfn f() = 1\n"
        session = ml.Session(src)
        with self.assertRaises(ValueError):
            session.edit(3, 4, "x")  # cuts the multibyte char 评
        with self.assertRaises(ValueError):
            session.edit(0, 3, "x")
        with self.assertRaises(ValueError):
            session.edit(0, 10**9, "")
        with self.assertRaises(UnicodeDecodeError):
            session.edit(0, 0, b"\xff\xfe")

    def test_reused_nodes_reported(self):
        session = ml.Session(self.SRC)
        start = self.SRC.index("10")
        session.edit(start, start + 2, "200")
        self.assertTrue(session.reused)
        self.assertTrue(all(isinstance(n, ml.Node) for n in session.reused))

    def test_sequential_edits(self):
        session = ml.Session("fn a() = 1\n")
        session.edit(9, 10, "2")
        session.edit(0, 0, "let z = 9\n")
        session.edit(len(session.src), len(session.src), "fn b() = a() + z\n")
        self.assertEqual(session.tree, ml.parse(session.src))
        env = ml.interpret(session.tree)
        self.assertEqual(ml.evaluate_expression("b()", env), 11)


class RandomizedConsistencyTests(unittest.TestCase):
    """Incremental result must always equal a full re-parse."""

    FRAGMENTS = [
        "fn f(x) = x + 1\n", "fn g(a, b) = a * b - 2\n", "let x = 3\n",
        "fn h() = if 1 < 2 then (3 + 4) else f(2, 3)\n",
        "(", ")", "1", "2.5", "foo", "+", "*", " ", "\n", ",", "=",
        "# 注释 comment\n", "fn k() = (1 + \n", "!@#", "if 1 then 2 else 3",
        "-", "fn ", "let y = ",
    ]

    def random_source(self, rng):
        return "".join(rng.choice(self.FRAGMENTS)
                       for _ in range(rng.randint(1, 10)))

    def test_random_edits(self):
        rng = random.Random(20260922)
        for trial in range(400):
            src = self.random_source(rng)
            session = ml.Session(src)
            for _ in range(rng.randint(1, 4)):
                old = session.src
                bounds = [i for i in range(len(old) + 1)
                          if ml.is_char_boundary(old, i)]
                start = rng.choice(bounds)
                end = rng.choice([b for b in bounds if b >= start])
                replacement = rng.choice(self.FRAGMENTS + ["", "é"])
                tree = session.edit(start, end, replacement)
                full = ml.parse(session.src)
                self.assertEqual(tree, full,
                                 msg=f"trial {trial}: {session.src!r}")
                self.assertTrue(spans_ok(tree, len(session.src)),
                                msg=f"trial {trial}: {session.src!r}")


if __name__ == "__main__":
    unittest.main()
