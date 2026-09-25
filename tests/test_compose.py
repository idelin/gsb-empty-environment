import unittest
from fractions import Fraction

from timemap import Interval, Segment, verify_composition

from .demo_map import build_demo_map, build_second_map


class TestCompose(unittest.TestCase):
    def setUp(self):
        self.f = build_demo_map()
        self.g = build_second_map()
        self.h = self.f.compose(self.g)  # h(x) = g(f(x))

    def test_checker_passes(self):
        self.assertEqual(verify_composition(self.f, self.g, self.h), [])

    def test_composed_values(self):
        self.assertEqual(self.h.value_at(0), 0)
        self.assertEqual(self.h.value_at(10), Fraction(5, 2))  # g(5)
        self.assertEqual(self.h.value_at(25), 10)              # g(10)
        self.assertEqual(self.h.value_at(35), 25)              # g(15)

    def test_pause_stays_constant_after_compose(self):
        seg = next(s for s in self.h.segments if s.domain.lo == 5)
        self.assertEqual(seg.rate, 0)
        self.assertEqual(seg.kind, "pause")
        self.assertEqual(seg.offset, Fraction(5, 2))

    def test_reverse_times_speed_is_reverse(self):
        # r1 (rate -1) 与 g2 (rate 3) 复合得 rate -3
        seg = next(
            s for s in self.h.segments
            if s.domain.contains(22) and s.rate != 0
        )
        self.assertEqual(seg.rate, -3)
        self.assertEqual(seg.kind, "reverse")

    def test_provenance_parents(self):
        for seg in self.h.segments:
            self.assertTrue(seg.parents)
            self.assertEqual(len(seg.parents) % 2, 0)

    def test_checker_detects_tampering(self):
        bad_segments = list(self.h.segments)
        s0 = bad_segments[0]
        bad_segments[0] = Segment(
            s0.domain, s0.rate, s0.offset + 1, s0.seg_id, s0.kind
        )
        from timemap import PiecewiseAffineMap

        bad = PiecewiseAffineMap(bad_segments)
        errors = verify_composition(self.f, self.g, bad)
        self.assertTrue(errors)

    def test_compose_with_huge_domain(self):
        from timemap import PiecewiseAffineMap

        big = PiecewiseAffineMap(
            [Segment(Interval(0, 10**18, True, False), 2, 1, "big", "speed")]
        )
        # h(x) = 4x+3，定义域为原像与定义域的交 [0, (1e18-1)/2)
        h = big.compose(big)
        self.assertEqual(verify_composition(big, big, h), [])
        self.assertEqual(h.value_at(10**17), 4 * 10**17 + 3)
        self.assertIsNone(h.value_at(10**18 - 1))


if __name__ == "__main__":
    unittest.main()
