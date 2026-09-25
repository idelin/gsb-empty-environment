import os
import sys
import tempfile
import unittest

sys.path.insert(0, os.path.dirname(os.path.dirname(os.path.abspath(__file__))))

from modlang.errors import LangError
from modlang.lexer import tokenize
from modlang.loader import Loader


class ModlangTest(unittest.TestCase):
    def run_modules(self, files, main="main.mod"):
        tmp = tempfile.TemporaryDirectory()
        self.addCleanup(tmp.cleanup)
        for name, src in files.items():
            path = os.path.join(tmp.name, name)
            os.makedirs(os.path.dirname(path), exist_ok=True)
            with open(path, "w", encoding="utf-8") as handle:
                handle.write(src)
        lines = []
        loader = Loader(out=lines.append)
        module = loader.run(os.path.join(tmp.name, main))
        return lines, loader, module

    # ---- core language ----

    def test_arithmetic_and_vars(self):
        lines, _, _ = self.run_modules({
            "main.mod": "const a = 6 * 7;\nvar b = 1;\nb = b + 2;\n"
                        "print(a);\nprint(b);\nprint(7 / 2);\n"
                        "print(8 / 2);\n",
        })
        self.assertEqual(lines, ["42", "3", "3.5", "4"])

    def test_fn_hoisting_mutual_recursion(self):
        lines, _, _ = self.run_modules({
            "main.mod": (
                "fn isEven(n) { if n == 0 { return true; } "
                "return isOdd(n - 1); }\n"
                "fn isOdd(n) { if n == 0 { return false; } "
                "return isEven(n - 1); }\n"
                "print(isOdd(5));\n"
            ),
        })
        self.assertEqual(lines, ["true"])

    def test_while_loop_factorial(self):
        lines, _, _ = self.run_modules({
            "main.mod": (
                "var acc = 1;\nvar i = 1;\n"
                "while i <= 5 { acc = acc * i; i = i + 1; }\n"
                "print(acc);\n"
            ),
        })
        self.assertEqual(lines, ["120"])

    def test_strings_and_logic(self):
        lines, _, _ = self.run_modules({
            "main.mod": (
                'const s = "he" + "llo";\n'
                "print(s);\n"
                "print(s == \"hello\" and not false);\n"
                "print(nil);\n"
            ),
        })
        self.assertEqual(lines, ["hello", "true", "nil"])

    # ---- uninitialized reads ----

    def test_uninitialized_const_read_same_module(self):
        with self.assertRaises(LangError) as ctx:
            self.run_modules({
                "main.mod": "const a = b;\nconst b = 1;\n",
            })
        self.assertIn("cannot read 'b' before it is initialized",
                      str(ctx.exception))

    def test_const_reassignment_rejected(self):
        with self.assertRaises(LangError) as ctx:
            self.run_modules({
                "main.mod": "const a = 1;\na = 2;\n",
            })
        self.assertIn("cannot assign to const 'a'", str(ctx.exception))

    # ---- modules ----

    def test_cross_module_function_cycle(self):
        lines, loader, _ = self.run_modules({
            "main.mod": (
                'import { isOdd } from "a";\n'
                'import { isEven } from "b";\n'
                "print(isOdd(7));\n"
                "print(isEven(10));\n"
            ),
            "a.mod": (
                'import { isEven } from "b";\n'
                "export fn isOdd(n) {\n"
                "    if n == 0 { return false; }\n"
                "    return isEven(n - 1);\n"
                "}\n"
                'print("init a");\n'
            ),
            "b.mod": (
                'import { isOdd } from "a";\n'
                "export fn isEven(n) {\n"
                "    if n == 0 { return true; }\n"
                "    return isOdd(n - 1);\n"
                "}\n"
                'print("init b");\n'
            ),
        })
        self.assertEqual(lines, ["init b", "init a", "true", "true"])
        # initialization graph: main -> a -> b -> a (cycle), 3 modules
        self.assertEqual(len(loader.cache), 3)
        mods = {m.name: m for m in loader.cache.values()}
        self.assertEqual([d.name for d in mods["a"].deps], ["b"])
        self.assertEqual([d.name for d in mods["b"].deps], ["a"])
        self.assertEqual(mods["a"].state, "initialized")

    def test_circular_const_read_reports_chain(self):
        with self.assertRaises(LangError) as ctx:
            self.run_modules({
                "main.mod": 'import { x } from "a";\nprint(x);\n',
                "a.mod": 'import { y } from "b";\nexport const x = y + 1;\n',
                "b.mod": 'import { x } from "a";\nexport const y = x + 1;\n',
            })
        msg = str(ctx.exception)
        self.assertIn("still initializing", msg)
        self.assertIn("read chain: b.y -> a.x", msg)

    def test_duplicate_import_initializes_once(self):
        lines, loader, _ = self.run_modules({
            "main.mod": (
                'import { f } from "a";\n'
                'import { g } from "a";\n'
                "print(f());\nprint(g());\n"
            ),
            "a.mod": (
                'print("init a");\n'
                "export fn f() { return 1; }\n"
                "export fn g() { return 2; }\n"
            ),
        })
        self.assertEqual(lines, ["init a", "1", "2"])
        self.assertEqual(lines.count("init a"), 1)
        self.assertEqual(len(loader.cache), 2)

    def test_imported_const_uses_dependency_order(self):
        lines, _, _ = self.run_modules({
            "main.mod": 'import { answer } from "a";\nprint(answer);\n',
            "a.mod": 'import { base } from "b";\n'
                     "export const answer = base + 2;\n",
            "b.mod": "export const base = 40;\n",
        })
        self.assertEqual(lines, ["42"])

    def test_import_of_non_exported_name_fails(self):
        with self.assertRaises(LangError) as ctx:
            self.run_modules({
                "main.mod": 'import { hidden } from "b";\n',
                "b.mod": "fn hidden() { return 1; }\n"
                         "export const v = 5;\n",
            })
        self.assertIn("does not export 'hidden'", str(ctx.exception))

    def test_module_local_bindings_are_private(self):
        # main's own 'x' must not clash with a's private 'x'.
        lines, _, _ = self.run_modules({
            "main.mod": 'import { get } from "a";\nconst x = 1;\n'
                        "print(get() + x);\n",
            "a.mod": "const x = 41;\nexport fn get() { return x; }\n",
        })
        self.assertEqual(lines, ["42"])

    # ---- UTF-8 byte positions ----

    def test_lexer_utf8_byte_offsets(self):
        src = '# comüent\nconst x = "héllo";'
        toks = tokenize(src)
        self.assertEqual(toks[0].value, "const")
        self.assertEqual(toks[0].start,
                         len("# comüent\n".encode("utf-8")))
        str_tok = next(t for t in toks if t.kind == "str")
        self.assertEqual(str_tok.value, "héllo")
        self.assertEqual(str_tok.end - str_tok.start,
                         len('"héllo"'.encode("utf-8")))

    def test_error_position_is_utf8_byte_range(self):
        src = "const π = 1;\nprint(y);\n"
        with self.assertRaises(LangError) as ctx:
            self.run_modules({"main.mod": src})
        err = ctx.exception
        self.assertEqual(err.start,
                         len("const π = 1;\nprint(".encode("utf-8")))
        self.assertEqual(err.end, err.start + 1)


if __name__ == "__main__":
    unittest.main()
