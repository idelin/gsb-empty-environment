"""核心类型规则测试：速度×时间=长度、仿射分离、自由维度。"""
import unittest
from fractions import Fraction

from unitchk import (QType, Registry, UnitKind, UnitTypeError, StepLog,
                     add, sub, mul, div, power, expect)


def make_registry():
    reg = Registry(["L", "M", "T", "Θ"])
    reg.define("m", {"L": 1})
    reg.define("s", {"T": 1})
    reg.define("kg", {"M": 1})
    reg.define("mps", {"L": 1, "T": -1})
    reg.define("K", {"Θ": 1})                                  # 差量开尔文
    reg.define("degC", {"Θ": 1}, kind=UnitKind.ABSOLUTE,
               offset=Fraction("273.15"))                      # 绝对摄氏
    reg.define("dC", {"Θ": 1})                                 # 摄氏差量
    return reg


def qtype(reg, name):
    u = reg.units[name]
    return QType(u.dim, u.kind is UnitKind.ABSOLUTE)


class TestRatioArithmetic(unittest.TestCase):
    def setUp(self):
        self.reg = make_registry()

    def test_speed_times_time_is_length(self):
        log = StepLog()
        t = mul(qtype(self.reg, "mps"), qtype(self.reg, "s"), log)
        expect(t, self.reg.units["m"], log, "速度×时间")
        self.assertFalse(t.absolute)
        self.assertTrue(all(st.domain for st in log.steps))

    def test_div_subtracts_exponents(self):
        log = StepLog()
        t = div(qtype(self.reg, "m"), qtype(self.reg, "s"), log)
        self.assertEqual(t.dim, self.reg.units["mps"].dim)

    def test_power_integer_exponents(self):
        log = StepLog()
        a = mul(qtype(self.reg, "mps"), qtype(self.reg, "mps"), log)
        b = power(qtype(self.reg, "mps"), 2, log)
        self.assertEqual(a, b)
        neg = power(qtype(self.reg, "mps"), -1, log)
        self.assertEqual(neg.dim, (-1, 0, 1, 0))

    def test_power_rejects_non_integer(self):
        with self.assertRaises(UnitTypeError):
            power(qtype(self.reg, "m"), 0.5, StepLog())

    def test_add_requires_same_dim(self):
        with self.assertRaises(UnitTypeError):
            add(qtype(self.reg, "m"), qtype(self.reg, "s"), StepLog())


class TestAffineSeparation(unittest.TestCase):
    def setUp(self):
        self.reg = make_registry()

    def test_absolute_temperature_not_in_multiplication(self):
        with self.assertRaises(UnitTypeError) as ctx:
            mul(qtype(self.reg, "degC"), qtype(self.reg, "s"), StepLog())
        self.assertIn("绝对仿射", str(ctx.exception))

    def test_absolute_temperature_not_in_division(self):
        with self.assertRaises(UnitTypeError):
            div(qtype(self.reg, "degC"), qtype(self.reg, "s"), StepLog())
        with self.assertRaises(UnitTypeError):
            div(qtype(self.reg, "m"), qtype(self.reg, "degC"), StepLog())

    def test_absolute_temperature_not_in_power(self):
        with self.assertRaises(UnitTypeError):
            power(qtype(self.reg, "degC"), 2, StepLog())

    def test_difference_of_absolutes_is_delta_and_usable(self):
        log = StepLog()
        delta = sub(qtype(self.reg, "degC"), qtype(self.reg, "degC"), log)
        self.assertFalse(delta.absolute)  # 差量是比例量
        t = mul(delta, qtype(self.reg, "s"), log)  # 差量可参与乘法
        self.assertEqual(t.dim, (0, 0, 1, 1))

    def test_absolute_plus_delta_is_absolute(self):
        log = StepLog()
        t = add(qtype(self.reg, "degC"), qtype(self.reg, "dC"), log)
        self.assertTrue(t.absolute)

    def test_absolute_plus_absolute_rejected(self):
        with self.assertRaises(UnitTypeError):
            add(qtype(self.reg, "degC"), qtype(self.reg, "degC"), StepLog())

    def test_ratio_minus_absolute_rejected(self):
        with self.assertRaises(UnitTypeError):
            sub(qtype(self.reg, "dC"), qtype(self.reg, "degC"), StepLog())

    def test_ratio_unit_cannot_have_offset(self):
        with self.assertRaises(ValueError):
            self.reg.define("bad", {"Θ": 1}, offset=Fraction(5))


class TestUnknownFreeDimension(unittest.TestCase):
    def test_unknown_unit_gets_free_dimension(self):
        reg = make_registry()
        x = reg.unknown("X")
        self.assertTrue(x.unknown)
        self.assertIn("dim<X>", reg.dimensions)
        self.assertEqual(x.dim[-1], 1)
        # 已有单位向量被零填充，长度一致
        self.assertEqual(len(reg.units["m"].dim), len(reg.dimensions))

    def test_free_dimension_arithmetic(self):
        reg = make_registry()
        reg.unknown("X")
        log = StepLog()
        x2 = mul(qtype(reg, "X"), qtype(reg, "X"), log)
        self.assertEqual(x2.dim[-1], 2)
        # 自由维度与长度不可相加
        with self.assertRaises(UnitTypeError):
            add(qtype(reg, "X"), qtype(reg, "m"), StepLog())


if __name__ == "__main__":
    unittest.main()
