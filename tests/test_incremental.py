import unittest

from minilang import Document, parse_text, tree_signature

SRC = (
    "# 求圆面积(注释包含中文)\n"
    "def area(r) = 3.14 * r * r;\n"
    "def double(x) = x + x;\n"
    "let a = area(double(2));\n"
)


def full_signature(text):
    tree, _, _ = parse_text(text)
    return tree_signature(tree)


class TestIncremental(unittest.TestCase):
    def test_edit_number_in_body_reuses_unrelated_decls(self):
        doc = Document(SRC)
        old_decls = doc.tree.decls
        old_positions = doc.positions
        start = SRC.encode("utf-8").index(b"3.14")
        doc.apply_edit(start, start + 4, "3.14159")  # 在 area 函数体内改数字
        new_decls = doc.tree.decls
        # 被编辑的 area 重新解析,其余声明原样复用(同一对象)
        self.assertIsNot(new_decls[0], old_decls[0])
        self.assertIs(new_decls[1], old_decls[1])
        self.assertIs(new_decls[2], old_decls[2])
        # 编辑后的声明位置随 delta=3 平移
        delta = len(b"3.14159") - 4
        self.assertEqual(
            doc.positions[id(new_decls[2])][0],
            old_positions[id(old_decls[2])][0] + delta,
        )
        # 增量树与全量树语义一致
        self.assertEqual(tree_signature(doc.tree), full_signature(doc.text))

    def test_insert_decl_at_end_reuses_everything(self):
        doc = Document(SRC)
        old_decls = doc.tree.decls
        end = len(SRC.encode("utf-8"))
        doc.apply_edit(end, end, "let b = double(a);\n")
        for old, new in zip(old_decls, doc.tree.decls):
            self.assertIs(old, new)
        self.assertEqual(len(doc.tree.decls), len(old_decls) + 1)
        self.assertEqual(tree_signature(doc.tree), full_signature(doc.text))

    def test_edit_in_comment_reuses_all_decls(self):
        doc = Document(SRC)
        old_decls = doc.tree.decls
        start = SRC.encode("utf-8").index("注释".encode("utf-8"))
        end = start + len("注释".encode("utf-8"))
        doc.apply_edit(start, end, "说明文字")
        for old, new in zip(old_decls, doc.tree.decls):
            self.assertIs(old, new)
        self.assertEqual(tree_signature(doc.tree), full_signature(doc.text))

    def test_error_created_then_fixed_by_edit(self):
        src = "def f() = (1 + 2;\nlet x = 3;\n"
        doc = Document(src)
        self.assertTrue(doc.errors)
        semi = src.encode("utf-8").index(b";")
        doc.apply_edit(semi, semi, ")")  # 补上右括号
        self.assertEqual(doc.errors, [])
        self.assertEqual(tree_signature(doc.tree), full_signature(doc.text))
        # 再改回未闭合,错误节点重新出现
        close = doc.text.encode("utf-8").index(b")")
        doc.apply_edit(close, close + 1, "")
        self.assertTrue(doc.errors)
        self.assertEqual(tree_signature(doc.tree), full_signature(doc.text))

    def test_multiple_sequential_edits_stay_consistent(self):
        doc = Document(SRC)
        edits = []
        pos = SRC.encode("utf-8").index(b"3.14")
        edits.append((pos, pos + 4, "3.14159"))
        edits.append((None, None, None))  # 占位,运行时按当前文本定位
        doc.apply_edit(*edits[0])
        for old, new in [(b"double(2)", b"double(4)"), (b"x + x", b"x * 2")]:
            at = doc.text.encode("utf-8").index(old)
            doc.apply_edit(at, at + len(old), new.decode("utf-8"))
            self.assertEqual(tree_signature(doc.tree), full_signature(doc.text))
        self.assertEqual(doc.errors, [])

    def test_utf8_boundary_guard(self):
        doc = Document(SRC)
        inside_char = SRC.encode("utf-8").index("注".encode("utf-8")) + 1
        with self.assertRaises(ValueError):
            doc.apply_edit(inside_char, inside_char, "x")

    def test_edit_range_out_of_bounds(self):
        doc = Document(SRC)
        with self.assertRaises(ValueError):
            doc.apply_edit(0, 10 ** 6, "x")


if __name__ == "__main__":
    unittest.main()
