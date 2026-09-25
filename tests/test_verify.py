import unittest

from temporal import (
    Change,
    ChangeKind,
    Segment,
    Timeline,
    TimePoint,
    apply,
    diff,
    endpoints,
    invert,
    merge3,
    scan_equal,
    summarize,
    verify_apply,
    verify_inverse,
    verify_summary,
)


def tp(n):
    return TimePoint(n)


def tl(*spec):
    return Timeline(Segment(tp(a), tp(b), v) for a, b, v in spec)


class TestEndpointsScan(unittest.TestCase):
    def test_endpoints_unions_all_boundaries(self):
        a = tl((0, 5, "A"), (9, 12, "B"))
        b = tl((3, 5, "A"), (5, 9, "C"))
        self.assertEqual(endpoints(a, b), (tp(0), tp(3), tp(5), tp(9), tp(12)))

    def test_scan_equal_detects_any_difference(self):
        a = tl((0, 5, "A"), (5, 10, "B"))
        same = tl((0, 3, "A"), (3, 5, "A"), (5, 10, "B"))  # coalesces to a
        self.assertTrue(scan_equal(a, same))
        different = tl((0, 5, "A"), (5, 10, "b"))
        self.assertFalse(scan_equal(a, different))
        shifted = tl((0, 6, "A"), (6, 10, "B"))
        self.assertFalse(scan_equal(a, shifted))


class TestFullScanVerification(unittest.TestCase):
    def setUp(self):
        self.base = tl((0, 5, "A"), (5, 10, "B"), (12, 15, "C"))
        self.target = tl((0, 2, "A"), (2, 7, "N"), (7, 10, "B"), (12, 15, "C2"))
        self.delta = diff(self.base, self.target)

    def test_composition_verified(self):
        self.assertTrue(verify_apply(self.base, self.delta, self.target))

    def test_inverse_verified(self):
        self.assertTrue(verify_inverse(self.base, self.delta))

    def test_inverse_of_inverse_is_identity_composition(self):
        forward = apply(self.base, self.delta)
        back = apply(forward.timeline, invert(self.delta))
        again = apply(back.timeline, self.delta)
        self.assertTrue(scan_equal(again.timeline, forward.timeline))
        self.assertTrue(scan_equal(back.timeline, self.base))

    def test_merge_verified_by_full_scan(self):
        a = diff(self.base, tl((0, 5, "A2"), (5, 10, "B"), (12, 15, "C")))
        b = diff(self.base, tl((0, 5, "A"), (5, 10, "B"), (12, 15, "C3")))
        merged = merge3(self.base, a, b)
        self.assertTrue(merged.ok)
        self.assertTrue(scan_equal(
            merged.timeline, tl((0, 5, "A2"), (5, 10, "B"), (12, 15, "C3"))))

    def test_summary_verification(self):
        baseline = summarize(self.base)
        self.assertTrue(verify_summary(self.base, baseline))
        self.assertFalse(verify_summary(self.target, baseline))


if __name__ == "__main__":
    unittest.main()
