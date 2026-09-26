import unittest
from fractions import Fraction

from rational_pieces.poly import Poly
from rational_pieces.ratfunc import RationalFunction


class TestRationalFunction(unittest.TestCase):
    def test_removable_singularity(self):
        # (x^2-1)/(x-1)：x=1 为可去奇点，极限 2
        f = RationalFunction.of(Poly([-1, 0, 1]), Poly([-1, 1]), 'f')
        self.assertIsNone(f.value_at(1))                 # 原式无定义（空洞）
        self.assertEqual(f.extended_value_at(1), 2)      # 连续延拓值
        self.assertTrue(f.limit_at(1).is_finite)
        self.assertEqual(f.limit_at(1).value, 2)
        self.assertEqual(f.removable_factor(), Poly([-1, 1]))
        self.assertEqual(f.simplified_den, Poly.one())   # 无极点

    def test_pole_one_sided_limits(self):
        g = RationalFunction.of(Poly([1]), Poly([-2, 1]), 'g')  # 1/(x-2)
        self.assertEqual(g.limit_at(2, 'left').sign, -1)
        self.assertEqual(g.limit_at(2, 'right').sign, 1)
        self.assertIsNone(g.limit_at(2).sign)            # 双侧异号 -> 无符号 ∞

    def test_even_multiplicity_pole(self):
        h = RationalFunction.of(Poly([1, 1]), Poly([4, -4, 1]), 'h')  # (x+1)/(x-2)^2
        self.assertEqual(h.limit_at(2).sign, 1)          # 两侧同为 +∞

    def test_domain_hole_preserved_under_addition(self):
        # 1/x + (-1/x) = 0/x^2：x=0 仍是定义域空洞（可去），不回收
        i = RationalFunction.of(Poly([1]), Poly([0, 1]), 'i')
        s = i + (-i)
        self.assertIsNone(s.value_at(0))
        self.assertEqual(s.extended_value_at(0), 0)
        self.assertEqual(s.removable_factor().degree, 2)

    def test_trace_records_domain(self):
        a = RationalFunction.of(Poly([1]), Poly([0, 1]), 'a')
        b = RationalFunction.of(Poly([-1]), Poly([0, 1]), 'b')
        s = a + b  # 1/x - 1/x = 0/x^2，约去公因子 x^2
        text = "\n".join(s.trace)
        self.assertIn("适用域", text)
        _ = s.simplified_den  # 触发约简记录
        self.assertIn("约去公因子", "\n".join(s.trace))

    def test_equality_via_simplification(self):
        f = RationalFunction(Poly([-1, 0, 1]), Poly([-1, 1]))
        g = RationalFunction(Poly([1, 1]), Poly([1]))
        self.assertEqual(f, g)  # (x^2-1)/(x-1) == x+1（作为有理式）


if __name__ == '__main__':
    unittest.main()
