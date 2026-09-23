"""运算符表：合法表接受、歧义表拒绝。"""
import unittest

from minparen import (DEFAULT_TABLE, LEFT, NONE, RIGHT, OperatorTable,
                      TableError, parse)
from minparen.parser import ParseError


class TableValidationTests(unittest.TestCase):
    def test_default_table_ok(self):
        self.assertIn("^", DEFAULT_TABLE.binary)
        self.assertEqual(DEFAULT_TABLE.binary["^"].assoc, RIGHT)
        self.assertEqual(DEFAULT_TABLE.unary["-"].precedence, 3)

    def test_mixed_assoc_same_level_rejected(self):
        with self.assertRaises(TableError):
            OperatorTable({"+": (1, LEFT), "-": (1, RIGHT)}, {})

    def test_none_assoc_sharing_level_rejected(self):
        with self.assertRaises(TableError):
            OperatorTable({"=": (1, NONE), "<": (1, NONE)}, {})

    def test_none_assoc_alone_accepted(self):
        table = OperatorTable({"=": (1, NONE), "+": (2, LEFT)}, {})
        self.assertEqual(table.binary["="].assoc, NONE)

    def test_bad_assoc_rejected(self):
        with self.assertRaises(TableError):
            OperatorTable({"+": (1, "sideways")}, {})

    def test_bad_precedence_rejected(self):
        with self.assertRaises(TableError):
            OperatorTable({"+": (0, LEFT)}, {})
        with self.assertRaises(TableError):
            OperatorTable({"+": (1.5, LEFT)}, {})

    def test_identifier_symbol_rejected(self):
        with self.assertRaises(TableError):
            OperatorTable({"mod": (1, LEFT)}, {})

    def test_reserved_char_symbol_rejected(self):
        with self.assertRaises(TableError):
            OperatorTable({"?": (1, LEFT)}, {})

    def test_same_symbol_unary_and_binary_ok(self):
        # '-' 同时作为一元与二元运算符是上下文可区分的，不算歧义。
        table = OperatorTable({"-": (1, LEFT)}, {"-": 2})
        self.assertIn("-", table.unary)

    def test_nonassoc_chain_parse_error(self):
        table = OperatorTable({"=": (1, NONE)}, {})
        with self.assertRaises(ParseError):
            parse("a = b = c", table)
        # 加括号后合法
        parse("(a = b) = c", table)


if __name__ == "__main__":
    unittest.main()
