"""独立验证模块测试：代入、核、证书、整除证据、矛盾环。"""
import unittest
from fractions import Fraction

from unitchk import (Registry, Leaf, Mul, Pow, Equation, build_system,
                     solve_integer_system, Solution, Infeasible,
                     ConversionGraph, AffineMap, Contradiction,
                     conversion_between, verify)


def make_registry():
    reg = Registry(["L", "M", "T"])
    reg.define("m", {"L": 1})
    reg.define("s", {"T": 1})
    reg.define("kg", {"M": 1})
    reg.define("mpss", {"L": 1, "T": -2})
    return reg


def solved_system():
    reg = make_registry()
    eqs = [Equation(Leaf("F"), Mul(Leaf("kg"), Leaf("mpss")), "F=ma")]
    system = build_system(eqs, reg, {"F"})
    sol = solve_integer_system(system.A, system.b,
                               system.var_names, system.row_labels)
    return reg, system, sol


class TestSubstitutionVerification(unittest.TestCase):
    def test_valid_assignment_passes(self):
        reg, system, sol = solved_system()
        keyed = {v: sol.particular[k] for k, v in enumerate(system.variables)}
        self.assertEqual(verify.verify_substitution(system, keyed), [])

    def test_tampered_assignment_fails(self):
        reg, system, sol = solved_system()
        keyed = {v: sol.particular[k] for k, v in enumerate(system.variables)}
        keyed[("F", 0)] += 1   # 篡改 L 指数
        errors = verify.verify_substitution(system, keyed)
        self.assertEqual(len(errors), 1)
        self.assertIn("F=ma", errors[0])

    def test_kernel_verification(self):
        reg = make_registry()
        eqs = [Equation(Mul(Leaf("X"), Leaf("Y")), Leaf("m"), "X*Y=m")]
        system = build_system(eqs, reg, {"X", "Y"})
        sol = solve_integer_system(system.A, system.b,
                                   system.var_names, system.row_labels)
        self.assertEqual(verify.verify_kernel(system, sol), [])
        sol.lattice[0][0] += 1   # 篡改格基
        self.assertTrue(len(verify.verify_kernel(system, sol)) > 0)


class TestCertificates(unittest.TestCase):
    def test_certificate_valid_and_invalid(self):
        A = [[1, 0], [1, 0]]
        b = [1, 2]
        self.assertTrue(verify.verify_certificate(A, b, [1, -1]))   # 0 = -1
        self.assertFalse(verify.verify_certificate(A, b, [1, 1]))   # 非证书

    def test_det_bareiss(self):
        self.assertEqual(verify.det_bareiss([[2]]), 2)
        self.assertEqual(verify.det_bareiss([[1, 2], [3, 4]]), -2)
        self.assertEqual(verify.det_bareiss(
            [[2, 0, 0], [0, 3, 0], [0, 0, 5]]), 30)
        self.assertEqual(verify.det_bareiss(
            [[1, 2, 3], [4, 5, 6], [7, 8, 9]]), 0)

    def test_divisibility_evidence_independent_check(self):
        reg = make_registry()
        eqs = [Equation(Pow(Leaf("X"), 2), Leaf("m"), "X^2=m")]
        system = build_system(eqs, reg, {"X"})
        res = solve_integer_system(system.A, system.b,
                                   system.var_names, system.row_labels)
        self.assertIsInstance(res, Infeasible)
        self.assertTrue(verify.check_infeasibility(system.A, system.b,
                                                   res.evidence))
        # 篡改 U（非幺模）应被 det 检查拒绝
        bad = dict(res.evidence)
        bad["U"] = [[2 * v for v in row] for row in res.evidence["U"]]
        self.assertFalse(verify.check_infeasibility(system.A, system.b, bad))


class TestCycleVerification(unittest.TestCase):
    def test_cycle_recomposition(self):
        reg = Registry(["L"])
        reg.define("m", {"L": 1})
        reg.define("km", {"L": 1}, scale=1000)
        reg.define("mile", {"L": 1}, scale=Fraction("1609.344"))
        g = ConversionGraph(reg)
        for name in ("m", "km", "mile"):
            g.add_unit(name)
        g.add_conversion("km", "m", conversion_between(
            reg.units["km"], reg.units["m"]))
        g.add_conversion("mile", "m", conversion_between(
            reg.units["mile"], reg.units["m"]))
        res = g.add_conversion("mile", "km", AffineMap(Fraction("1.6")))
        self.assertIsInstance(res, Contradiction)
        composed = verify.verify_cycle(res.cycle)
        self.assertEqual(composed, res.composed)
        self.assertFalse(composed.is_identity())
        # 复合值精确：1.6 / 1.609344 ≠ 1
        self.assertEqual(composed.a, Fraction("1.6") / Fraction("1.609344"))
        self.assertEqual(composed.b, 0)


if __name__ == "__main__":
    unittest.main()
