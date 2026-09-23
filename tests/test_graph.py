"""版本化转换图测试：精确换算、矛盾环、版本快照、仿射/比例分离。"""
import unittest
from fractions import Fraction

from unitchk import (Registry, UnitKind, ConversionGraph, AffineMap,
                     Contradiction, conversion_between, verify)


def make_registry():
    reg = Registry(["L", "Θ"])
    reg.define("m", {"L": 1})
    reg.define("km", {"L": 1}, scale=1000)
    reg.define("mile", {"L": 1}, scale=Fraction("1609.344"))
    reg.define("K", {"Θ": 1})                                    # 差量
    reg.define("degC", {"Θ": 1}, kind=UnitKind.ABSOLUTE,
               offset=Fraction("273.15"))
    reg.define("degF", {"Θ": 1}, kind=UnitKind.ABSOLUTE,
               scale=Fraction(5, 9),
               offset=Fraction("273.15") - 32 * Fraction(5, 9))
    reg.define("dC", {"Θ": 1})
    return reg


def make_length_graph(reg):
    g = ConversionGraph(reg)
    for name in ("m", "km", "mile"):
        g.add_unit(name)
    g.add_conversion("km", "m", conversion_between(
        reg.units["km"], reg.units["m"]), "1 km = 1000 m")
    g.add_conversion("mile", "m", conversion_between(
        reg.units["mile"], reg.units["m"]), "1 mile = 1609.344 m")
    return g


class TestExactConversion(unittest.TestCase):
    def setUp(self):
        self.reg = make_registry()
        self.g = make_length_graph(self.reg)

    def test_exact_rational_factor(self):
        path, amap = self.g.find("mile", "km")
        self.assertEqual(amap.a, Fraction("1.609344"))
        self.assertEqual(amap.b, 0)
        self.assertIsInstance(amap.a, Fraction)
        # 1/3 级别的精确性：3 * (1/3) == 1 严格成立
        third = AffineMap(Fraction(1, 3))
        self.assertEqual(third.then(third.inverse()).composed if False else
                         third.then(third.inverse()).a, 1)

    def test_cycle_consistency_exact(self):
        # mile -> m -> km -> ? 反向复合应严格为恒等
        path1, fwd = self.g.find("mile", "km")
        path2, back = self.g.find("km", "mile")
        self.assertTrue(fwd.then(back).is_identity())

    def test_affine_temperature_conversion(self):
        g = ConversionGraph(self.reg)
        for name in ("degC", "degF"):
            g.add_unit(name)
        g.add_conversion("degF", "degC", conversion_between(
            self.reg.units["degF"], self.reg.units["degC"]))
        _, amap = g.find("degF", "degC")
        self.assertEqual(amap(32), 0)      # 32°F = 0°C 精确
        self.assertEqual(amap(212), 100)   # 212°F = 100°C 精确


class TestContradictionCycle(unittest.TestCase):
    def setUp(self):
        self.reg = make_registry()
        self.g = make_length_graph(self.reg)

    def test_contradictory_edge_rejected_with_evidence(self):
        # 错误声明：1 mile = 1.6 km（与 1.609344 矛盾）
        res = self.g.add_conversion("mile", "km", AffineMap(Fraction("1.6")),
                                    "错误声明")
        self.assertIsInstance(res, Contradiction)
        self.assertFalse(res.composed.is_identity())
        # 独立重算矛盾环：复合映射 ≠ 恒等
        recomposed = verify.verify_cycle(res.cycle)
        self.assertEqual(recomposed, res.composed)
        self.assertFalse(recomposed.is_identity())
        # 被拒绝的边不改变图
        _, amap = self.g.find("mile", "km")
        self.assertEqual(amap.a, Fraction("1.609344"))

    def test_consistent_duplicate_is_idempotent(self):
        v = self.g.version
        res = self.g.add_conversion("mile", "m",
                                    conversion_between(self.reg.units["mile"],
                                                       self.reg.units["m"]))
        self.assertNotIsInstance(res, Contradiction)
        self.assertEqual(self.g.version, v)  # 幂等，不增版本

    def test_dimension_mismatch_rejected(self):
        self.reg.define("s2", {"L": 2})
        self.g.add_unit("s2")
        with self.assertRaises(ValueError):
            self.g.add_conversion("m", "s2", AffineMap(1))

    def test_ratio_offset_separation(self):
        g = ConversionGraph(self.reg)
        g.add_unit("degC")
        g.add_unit("dC")
        with self.assertRaises(ValueError):
            g.add_conversion("degC", "dC", AffineMap(1))

    def test_ratio_edge_cannot_have_offset(self):
        g = ConversionGraph(self.reg)
        g.add_unit("m")
        g.add_unit("km")
        with self.assertRaises(ValueError):
            g.add_conversion("m", "km", AffineMap(1, 5))


class TestVersioning(unittest.TestCase):
    def setUp(self):
        self.reg = make_registry()
        self.g = make_length_graph(self.reg)

    def test_version_bumps_and_journal(self):
        self.reg.define("yd", {"L": 1}, scale=Fraction("0.9144"))
        v0 = self.g.version
        self.g.add_unit("yd")
        self.assertEqual(self.g.version, v0 + 1)
        self.g.add_conversion("yd", "m", conversion_between(
            self.reg.units["yd"], self.reg.units["m"]))
        self.assertEqual(self.g.version, v0 + 2)
        self.g.remove_conversion("yd", "m")
        self.assertEqual(self.g.version, v0 + 3)
        ops = [op for _, op in self.g.journal]
        self.assertIn("add-unit yd", ops)
        self.assertIn("add-conversion yd->m", ops)
        self.assertIn("remove-conversion yd->m", ops)

    def test_snapshot_query_old_version(self):
        v = self.g.version
        self.g.remove_conversion("mile", "m")
        self.assertIsNone(self.g.find("mile", "km"))       # 当前版本已断
        found = self.g.find("mile", "km", version=v)       # 历史快照仍可查
        self.assertIsNotNone(found)
        self.assertEqual(found[1].a, Fraction("1.609344"))

    def test_remove_unit_drops_incident_edges(self):
        self.g.remove_unit("mile")
        self.assertIsNone(self.g.find("mile", "m"))
        self.assertIsNone(self.g.find("km", "mile"))


if __name__ == "__main__":
    unittest.main()
