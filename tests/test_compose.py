import unittest
from fractions import Fraction

from timemap import Interval, PiecewiseMap, check_composition, rational_samples


def make_f():
    """real -> logical: play, pause, 2x, reverse."""
    return PiecewiseMap.build(0, 0, [(5, 1), (5, 0), (10, 2), (10, -1)])


def make_g():
    """logical -> output: play, pause, half-speed."""
    return PiecewiseMap.build(0, 0, [(10, 1), (5, 0), (15, Fraction(1, 2))])


class TestCompose(unittest.TestCase):
    def setUp(self):
        self.f = make_f()
        self.g = make_g()
        self.h = self.f.compose(self.g)

    def test_composed_values(self):
        cases = {0: 0, 5: 5, 10: 5, 12: 9, 13: 10, 16: 11, 20: 15, 30: 10}
        for t, expected in cases.items():
            self.assertEqual(self.h.eval(t), expected, f"t={t}")

    def test_composed_structure(self):
        self.assertEqual(
            [(str(s.domain()), s.slope) for s in self.h.segments],
            [("[0, 5)", 1), ("[5, 10)", 0), ("[10, 25/2)", 2),
             ("[25/2, 15)", 0), ("[15, 20)", 1), ("[20, 30]", Fraction(-1, 2))],
        )

    def test_composed_pause_preimage(self):
        pre = self.h.preimage(Interval.point(10))
        self.assertEqual(str(pre), "[25/2, 15] U {30}")

    def test_composition_verified_by_rational_enumeration(self):
        result = check_composition(self.f, self.g, self.h)
        self.assertTrue(result.ok, result)
        self.assertGreater(result.samples, 0)

    def test_verifier_detects_tampering(self):
        bad = PiecewiseMap([
            type(self.h.segments[0])(0, 5, 1, 1),
            *self.h.segments[1:],
        ])
        result = check_composition(self.f, self.g, bad)
        self.assertFalse(result.ok)

    def test_compose_requires_coverage(self):
        short = PiecewiseMap.build(0, 0, [(20, 1)])
        with self.assertRaises(ValueError):
            self.f.compose(short)

    def test_huge_composition_is_breakpoint_based(self):
        big = 10**15
        f = PiecewiseMap.build(0, 0, [(big, 2), (big, 0), (big, -1)])
        g = PiecewiseMap.build(0, 0, [(big, Fraction(1, 3)), (2 * big, 1)])
        h = f.compose(g)
        self.assertLessEqual(len(h.segments), 6)
        result = check_composition(f, g, h)
        self.assertTrue(result.ok, result)
        self.assertLessEqual(len(rational_samples(h)), 2 * len(h.segments) + 1)


if __name__ == "__main__":
    unittest.main()
