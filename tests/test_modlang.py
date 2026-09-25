import unittest

from modlang import Interpreter, LangError
from modlang.ast_nodes import Bin, ConstDecl, FnDecl, Import
from modlang.lexer import tokenize
from modlang.parser import Parser


def run(sources, entry):
    out = []
    interp = Interpreter(sources, output=out.append)
    interp.load(entry)
    return out, interp


def run_error(sources, entry):
    interp = Interpreter(sources, output=lambda _line: None)
    try:
        interp.load(entry)
    except LangError as err:
        return err
    raise AssertionError("expected LangError")


class LexerTests(unittest.TestCase):
    def test_byte_spans_with_multibyte_chars(self):
        tokens = tokenize('const 变量 = "值"')
        self.assertEqual(
            [(t.kind, t.text, t.start, t.end) for t in tokens],
            [
                ("keyword", "const", 0, 5),
                ("ident", "变量", 6, 12),
                ("punct", "=", 13, 14),
                ("string", "值", 15, 20),
                ("eof", "", 20, 20),
            ],
        )

    def test_comments_and_operators(self):
        tokens = tokenize("a == 1 // note\nb != 2")
        texts = [t.text for t in tokens]
        self.assertEqual(texts, ["a", "==", "1", "b", "!=", "2", ""])


class ParserTests(unittest.TestCase):
    def parse(self, source):
        return Parser(tokenize(source)).parse_module()

    def test_precedence_and_spans(self):
        ast = self.parse("export const x = 1 + 2 * 3")
        decl = ast.items[0]
        self.assertIsInstance(decl, ConstDecl)
        self.assertTrue(decl.exported)
        self.assertIsInstance(decl.expr, Bin)
        self.assertEqual(decl.expr.op, "+")
        self.assertEqual(decl.expr.right.op, "*")
        self.assertEqual(decl.span, (0, 26))

    def test_import_and_fn(self):
        ast = self.parse('import { f } from "m"\nexport fn g(n) { return f(n) }')
        self.assertIsInstance(ast.items[0], Import)
        self.assertEqual(ast.items[0].names, ["f"])
        self.assertEqual(ast.items[0].module, "m")
        self.assertIsInstance(ast.items[1], FnDecl)
        self.assertEqual(ast.items[1].params, ["n"])


class ModuleTests(unittest.TestCase):
    def test_mutual_function_import_cycle_links(self):
        sources = {
            "a": (
                'import { is_odd } from "b"\n'
                "export fn is_even(n) {\n"
                "  if n == 0 { return true }\n"
                "  return is_odd(n - 1)\n"
                "}\n"
                "export const check = is_even(10)\n"
                "print(check)\n"
            ),
            "b": (
                'import { is_even } from "a"\n'
                "export fn is_odd(n) {\n"
                "  if n == 0 { return false }\n"
                "  return is_even(n - 1)\n"
                "}\n"
            ),
        }
        out, interp = run(sources, "a")
        self.assertEqual(out, ["true"])
        self.assertEqual(interp.modules["a"].state, "initialized")
        self.assertEqual(interp.modules["b"].state, "initialized")

    def test_const_cycle_reports_initialization_chain(self):
        sources = {
            "a": 'import { y } from "b"\nexport const x = y + 1\n',
            "b": 'import { x } from "a"\nexport const y = x + 1\n',
        }
        err = run_error(sources, "a")
        text = str(err)
        self.assertIn("not initialized", text)
        self.assertIn("a -> b -> a", text)
        self.assertIn("'x'", text)

    def test_repeated_import_initializes_once(self):
        sources = {
            "main": (
                'import { left } from "left"\n'
                'import { right } from "right"\n'
                "print(left)\n"
                "print(right)\n"
            ),
            "left": 'import { answer } from "lib"\nexport const left = answer + 1\n',
            "right": 'import { answer } from "lib"\nexport const right = answer + 2\n',
            "lib": 'print("lib initialized")\nexport const answer = 42\n',
        }
        out, interp = run(sources, "main")
        self.assertEqual(out, ["lib initialized", "43", "44"])
        self.assertEqual(len(interp.modules), 4)

    def test_uninitialized_local_read_rejected(self):
        err = run_error({"m": "const a = b\nconst b = 1\n"}, "m")
        self.assertIn("not initialized", str(err))
        self.assertIn("'b'", str(err))

    def test_circular_self_initializer_rejected(self):
        err = run_error({"m": "const x = x + 1\n"}, "m")
        self.assertIn("circular initializer", str(err))

    def test_import_of_non_exported_name_rejected(self):
        sources = {
            "a": 'import { hidden } from "b"\n',
            "b": "const hidden = 1\n",
        }
        err = run_error(sources, "a")
        self.assertIn("does not export 'hidden'", str(err))

    def test_fn_hoisting_allows_forward_reference(self):
        sources = {
            "m": (
                "export const v = double(21)\n"
                "fn double(n) { return n * 2 }\n"
                "print(v)\n"
            ),
        }
        out, _ = run(sources, "m")
        self.assertEqual(out, ["42"])

    def test_undefined_name_reports_byte_span(self):
        err = run_error({"m": "const x = y\n"}, "m")
        self.assertEqual(err.span, (10, 11))
        self.assertIn("bytes 10..11", str(err))

    def test_module_local_bindings_are_private(self):
        sources = {
            "a": 'import { get } from "b"\nconst secret = 1\nprint(get())\n',
            "b": "const secret = 2\nexport fn get() { return secret }\n",
        }
        out, _ = run(sources, "a")
        self.assertEqual(out, ["2"])

    def test_unicode_identifiers_and_strings(self):
        sources = {"m": 'const 问候 = "你好"\nprint(问候 + "！")\n'}
        out, _ = run(sources, "m")
        self.assertEqual(out, ["你好！"])

    def test_missing_module_error(self):
        err = run_error({"a": 'import { x } from "nope"\n'}, "a")
        self.assertIn("module 'nope' not found", str(err))

    def test_recursion_and_control_flow(self):
        sources = {
            "m": (
                "fn fib(n) {\n"
                "  if n <= 1 { return n }\n"
                "  return fib(n - 1) + fib(n - 2)\n"
                "}\n"
                "print(fib(15))\n"
            ),
        }
        out, _ = run(sources, "m")
        self.assertEqual(out, ["610"])


if __name__ == "__main__":
    unittest.main()
