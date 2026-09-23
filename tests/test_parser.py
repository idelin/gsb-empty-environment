"""解析器：支持域内的语法与拒绝规则。"""

import unittest

from ratid.errors import Unsupported
from ratid.parser import parse


class TestParser(unittest.TestCase):
    def test_basic_structures(self):
        for text in ["x + 1", "x^2 - 1", "(x+1)/(x-1)", "2*x**3", "x^-2",
                     "0.5*x", "x^2^3", "-(x+1)", "x - -1"]:
            parse(text)  # 不抛异常即可

    def test_right_associative_power(self):
        tree = parse("x^2^3")
        self.assertEqual(tree.kind, "pow")
        self.assertEqual(tree.value, 8)  # 2^3 折叠

    def test_reject_multiple_variables(self):
        with self.assertRaises(Unsupported):
            parse("x + y")
        with self.assertRaises(Unsupported):
            parse("sin(x)")

    def test_reject_implicit_multiplication(self):
        with self.assertRaises(Unsupported):
            parse("2x")

    def test_reject_non_integer_exponent(self):
        with self.assertRaises(Unsupported):
            parse("x^0.5")
        with self.assertRaises(Unsupported):
            parse("x^(1/2)")

    def test_reject_garbage(self):
        with self.assertRaises(Unsupported):
            parse("x +")
        with self.assertRaises(Unsupported):
            parse("(x + 1")
        with self.assertRaises(Unsupported):
            parse("")
        with self.assertRaises(Unsupported):
            parse("x @ 1")


if __name__ == "__main__":
    unittest.main()
