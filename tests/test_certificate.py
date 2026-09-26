import unittest
from fractions import Fraction

from wmmf import (
    Demand,
    build_certificate,
    check_feasible,
    verify_certificate,
    water_fill,
)


def make_alloc():
    return water_fill(
        {"A": 10, "B": 4},
        {
            "d1": Demand("d1", weight=1, cap=10, links=("A",)),
            "d2": Demand("d2", weight=1, cap=10, links=("A", "B")),
        },
    )


class TestCertificate(unittest.TestCase):
    def test_feasible_allocation_passes(self):
        alloc = make_alloc()
        self.assertEqual(check_feasible(alloc), [])
        cert = build_certificate(alloc)
        self.assertEqual(verify_certificate(alloc, cert), [])
        # d2 的瓶颈是 B（先饱和），d1 的瓶颈是 A
        self.assertEqual(cert["d2"], "B")
        self.assertEqual(cert["d1"], "A")

    def test_satisfied_demand_needs_no_certificate(self):
        alloc = water_fill(
            {"A": 10},
            {
                "d1": Demand("d1", weight=1, cap=2, links=("A",)),
                "d2": Demand("d2", weight=1, cap=10, links=("A",)),
            },
        )
        cert = build_certificate(alloc)
        self.assertNotIn("d1", cert)   # 已达上限，无需证书
        self.assertEqual(cert["d2"], "A")

    def test_unfair_allocation_has_no_certificate(self):
        alloc = make_alloc()
        # 篡改：d1 降到 4，d2 升到 6（链路 A 仍饱和，但不公平）
        alloc.rates["d1"] = Fraction(4)
        alloc.rates["d2"] = Fraction(6)
        alloc.link_loads["A"] = Fraction(10)
        alloc.link_loads["B"] = Fraction(6)  # B 超载
        self.assertNotEqual(check_feasible(alloc), [])  # B 超容量，不可行
        with self.assertRaises(ValueError):
            build_certificate(alloc)

    def test_tampered_certificate_rejected(self):
        alloc = make_alloc()
        cert = build_certificate(alloc)
        bad = dict(cert)
        bad["d2"] = "A"  # d2 的归一速率在 A 上不是最大者之一？d1 归一 6 > 4
        errors = verify_certificate(alloc, bad)
        self.assertTrue(errors)


if __name__ == "__main__":
    unittest.main()
