import unittest

from minilang import (Binary, Call, ErrorNode, FuncDecl, LetDecl, Number,
                      compute_positions, parse, walk)


def parse_ok(src):
    tree, errors = parse(src)
    assert not errors, errors
    return tree


class TestFullParse(unittest.TestCase):
    def test_structure(self):
        tree = parse_ok("def f(x, y) = x * y + 1;\nlet z = f(2, 3);")
        func, let = tree.decls
        self.assertIsInstance(func, FuncDecl)
        self.assertEqual(func.name, "f")
        self.assertEqual(func.params, ("x", "y"))
        self.assertIsInstance(func.body, Binary)
        self.assertEqual(func.body.op, "+")
        self.assertIsInstance(let, LetDecl)
        self.assertEqual(let.name, "z")
        self.assertIsInstance(let.value, Call)
        self.assertEqual(let.value.callee.name, "f")
        self.assertEqual(len(let.value.args), 2)

    def test_precedence_and_parens(self):
        tree = parse_ok("let a = 2 + 3 * 4;\nlet b = (2 + 3) * 4;")
        a, b = tree.decls
        self.assertEqual(a.value.op, "+")
        self.assertEqual(a.value.right.op, "*")
        self.assertEqual(b.value.op, "*")
        self.assertEqual(b.value.left.op, "+")

    def test_positions_are_utf8_bytes(self):
        src = "# 注释\ndef f() = 1;"
        tree = parse_ok(src)
        pos = compute_positions(tree)
        # "# 注释\n" 占 9 个字节(两个汉字各 3 字节)
        self.assertEqual(pos[id(tree)], (0, len(src.encode("utf-8"))))
        self.assertEqual(pos[id(tree.decls[0])], (9, 21))
        number = next(n for n in walk(tree) if isinstance(n, Number))
        self.assertEqual(pos[id(number)], (19, 20))
        self.assertEqual(src.encode("utf-8")[19:20], b"1")

    def test_unclosed_paren_still_parses_later_decls(self):
        src = "def bad() = (1 + 2;\ndef good() = 42;\nlet g = good();"
        tree, errors = parse(src)
        self.assertTrue(errors)
        self.assertEqual(len(tree.decls), 3)
        self.assertIsInstance(tree.decls[1], FuncDecl)
        self.assertEqual(tree.decls[1].name, "good")
        self.assertIsInstance(tree.decls[2], LetDecl)
        err = next(n for n in walk(tree) if isinstance(n, ErrorNode))
        self.assertIn(")", err.message)

    def test_unclosed_call_still_parses_later_decls(self):
        src = "let a = f(1, 2;\nlet b = 3;"
        tree, errors = parse(src)
        self.assertTrue(errors)
        self.assertEqual(len(tree.decls), 2)
        self.assertEqual(tree.decls[1].name, "b")
        err = next(n for n in walk(tree) if isinstance(n, ErrorNode))
        self.assertIn(")", err.message)

    def test_unclosed_param_list(self):
        src = "def f(x = x;\ndef g() = 1;"
        tree, errors = parse(src)
        self.assertTrue(errors)
        self.assertIsInstance(tree.decls[0], ErrorNode)
        self.assertIsInstance(tree.decls[1], FuncDecl)
        self.assertEqual(tree.decls[1].name, "g")

    def test_missing_semicolon(self):
        tree, errors = parse("let a = 1\nlet b = 2;")
        self.assertTrue(errors)
        self.assertEqual([d.name for d in tree.decls], ["a", "b"])
        self.assertTrue(tree.decls[0].extras)
        self.assertIsInstance(tree.decls[0].extras[0], ErrorNode)

    def test_unexpected_top_level_content(self):
        tree, errors = parse("42\ndef f() = 1;")
        self.assertTrue(errors)
        self.assertIsInstance(tree.decls[0], ErrorNode)
        self.assertIsInstance(tree.decls[1], FuncDecl)

    def test_error_positions_are_byte_ranges(self):
        src = "def bad() = (1 + 2;"
        _, errors = parse(src)
        self.assertEqual(len(errors), 1)
        _, start, end = errors[0]
        self.assertEqual(src.encode("utf-8")[start:end], b"(1 + 2")


if __name__ == "__main__":
    unittest.main()
