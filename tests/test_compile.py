import unittest

from xsplice import (
    EQUAL_POWER,
    LINEAR,
    REJECT,
    GainConstraintError,
    Segment,
    SegmentValidationError,
    ShortSegmentError,
    compile_plan,
    resolve_fades,
)

SRC = {"s": [0.0] * 64, "u": [1.0] * 64}


class ShortSegmentRuleTest(unittest.TestCase):
    def test_fades_that_fit_pass_through(self):
        seg = Segment("a", "s", 0, 10, 0, fade_in=3, fade_out=4)
        self.assertEqual(resolve_fades(seg), (3, 4))

    def test_shorten_scales_proportionally_and_floors(self):
        seg = Segment("a", "s", 0, 5, 0, fade_in=4, fade_out=4)
        self.assertEqual(resolve_fades(seg), (2, 2))  # scale 5/8, floored

    def test_shorten_never_exceeds_duration(self):
        for dur in range(1, 10):
            for fi in range(0, 15):
                for fo in range(0, 15):
                    seg = Segment("a", "s", 0, dur, 0, fade_in=fi, fade_out=fo)
                    efi, efo = resolve_fades(seg)
                    self.assertGreaterEqual(efi, 0)
                    self.assertGreaterEqual(efo, 0)
                    self.assertLessEqual(efi + efo, dur)

    def test_reject_policy_raises(self):
        seg = Segment("a", "s", 0, 5, 0, fade_in=4, fade_out=4)
        with self.assertRaises(ShortSegmentError):
            compile_plan([seg], SRC, 48000, on_short=REJECT)

    def test_reject_policy_accepts_fitting_fades(self):
        seg = Segment("a", "s", 0, 10, 0, fade_in=5, fade_out=5)
        plan = compile_plan([seg], SRC, 48000, on_short=REJECT)
        self.assertEqual(plan.total, 10)

    def test_unknown_policy_raises(self):
        seg = Segment("a", "s", 0, 5, 0, fade_in=4, fade_out=4)
        with self.assertRaises(ValueError):
            resolve_fades(seg, "bogus")


class ValidationTest(unittest.TestCase):
    def test_negative_fade_rejected(self):
        with self.assertRaises(SegmentValidationError):
            compile_plan([Segment("a", "s", 0, 4, 0, fade_in=-1)], SRC, 48000)

    def test_negative_offset_rejected(self):
        with self.assertRaises(SegmentValidationError):
            compile_plan([Segment("a", "s", 0, 4, -1)], SRC, 48000)

    def test_forward_out_of_bounds_rejected(self):
        with self.assertRaises(SegmentValidationError):
            compile_plan([Segment("a", "s", 60, 65, 0)], SRC, 48000)

    def test_reversed_out_of_bounds_rejected(self):
        with self.assertRaises(SegmentValidationError):
            compile_plan([Segment("a", "s", 65, 60, 0)], SRC, 48000)
        with self.assertRaises(SegmentValidationError):
            compile_plan([Segment("a", "s", 5, -1, 0)], SRC, 48000)

    def test_full_reversed_range_is_in_bounds(self):
        plan = compile_plan([Segment("a", "s", 64, 0, 0)], SRC, 48000)
        self.assertEqual(plan.total, 64)

    def test_unknown_curve_rejected(self):
        with self.assertRaises(SegmentValidationError):
            compile_plan([Segment("a", "s", 0, 4, 0, curve="cosine")], SRC, 48000)

    def test_duplicate_ids_rejected(self):
        segs = [Segment("a", "s", 0, 4, 0), Segment("a", "s", 4, 8, 4)]
        with self.assertRaises(SegmentValidationError):
            compile_plan(segs, SRC, 48000)

    def test_unknown_source_rejected(self):
        with self.assertRaises(SegmentValidationError):
            compile_plan([Segment("a", "nope", 0, 4, 0)], SRC, 48000)


class EmptySegmentTest(unittest.TestCase):
    def test_empty_segment_does_not_change_result(self):
        real = Segment("a", "s", 0, 8, 0)
        empty = Segment("e", "s", 3, 3, 100)
        plain = compile_plan([real], SRC, 48000)
        with_empty = compile_plan([real, empty], SRC, 48000)
        self.assertEqual(plain.total, with_empty.total)
        self.assertEqual(len(with_empty.segments), 1)
        self.assertEqual(len(with_empty.intervals), 1)

    def test_all_empty_plan_is_silent_and_empty(self):
        plan = compile_plan([Segment("e", "s", 3, 3, 0)], SRC, 48000)
        self.assertEqual(plan.total, 0)
        self.assertEqual(plan.intervals, ())


class GainConstraintTest(unittest.TestCase):
    def test_two_full_gain_overlaps_violate_power(self):
        segs = [Segment("a", "s", 0, 16, 0), Segment("b", "s", 0, 16, 8)]
        with self.assertRaises(GainConstraintError) as ctx:
            compile_plan(segs, SRC, 48000)
        self.assertEqual(ctx.exception.kind, "power")
        self.assertGreaterEqual(ctx.exception.position, 8)
        self.assertLess(ctx.exception.position, 16)

    def test_triple_full_gain_overlap_violates(self):
        segs = [
            Segment("a", "s", 0, 16, 0),
            Segment("b", "s", 0, 16, 4),
            Segment("c", "s", 0, 16, 8),
        ]
        with self.assertRaises(GainConstraintError):
            compile_plan(segs, SRC, 48000)

    def test_matched_linear_crossfade_passes(self):
        segs = [
            Segment("a", "s", 0, 16, 0, fade_out=8),
            Segment("b", "s", 0, 16, 8, fade_in=8),
        ]
        plan = compile_plan(segs, SRC, 48000)
        self.assertEqual(plan.total, 24)

    def test_matched_equal_power_crossfade_passes(self):
        segs = [
            Segment("a", "s", 0, 16, 0, fade_out=8, curve=EQUAL_POWER),
            Segment("b", "s", 0, 16, 8, fade_in=8, curve=EQUAL_POWER),
        ]
        plan = compile_plan(segs, SRC, 48000)
        self.assertEqual(plan.total, 24)

    def test_overlapping_fade_regions_are_checked_globally(self):
        # Three fade regions overlapping at once: A fading out while B and
        # C both fade in over [0, 8).  Power exceeds 1 -> rejected.
        segs = [
            Segment("a", "s", 0, 8, 0, fade_out=8),
            Segment("b", "s", 0, 8, 0, fade_in=8),
            Segment("c", "s", 0, 8, 0, fade_in=8),
        ]
        with self.assertRaises(GainConstraintError):
            compile_plan(segs, SRC, 48000)

    def test_max_power_is_configurable(self):
        segs = [Segment("a", "s", 0, 16, 0), Segment("b", "s", 0, 16, 8)]
        plan = compile_plan(segs, SRC, 48000, max_power=2.0)
        self.assertEqual(plan.total, 24)

    def test_amplitude_constraint_is_opt_in(self):
        # Equal-power crossfade: amplitude sum peaks at sqrt(2) > 1.
        segs = [
            Segment("a", "s", 0, 16, 0, fade_out=8, curve=EQUAL_POWER),
            Segment("b", "s", 0, 16, 8, fade_in=8, curve=EQUAL_POWER),
        ]
        with self.assertRaises(GainConstraintError) as ctx:
            compile_plan(segs, SRC, 48000, max_amplitude=1.0)
        self.assertEqual(ctx.exception.kind, "amplitude")
        # Linear crossfade keeps amplitude sum at exactly 1.
        linear = [
            Segment("a", "s", 0, 16, 0, fade_out=8, curve=LINEAR),
            Segment("b", "s", 0, 16, 8, fade_in=8, curve=LINEAR),
        ]
        compile_plan(linear, SRC, 48000, max_amplitude=1.0)


class CrossfadeCountingTest(unittest.TestCase):
    def test_overlap_counted_once_in_total_length(self):
        segs = [
            Segment("a", "s", 0, 100, 0, fade_out=20),
            Segment("b", "s", 0, 100, 80, fade_in=20),
        ]
        plan = compile_plan(segs, {"s": [0.0] * 100}, 48000)
        self.assertEqual(plan.total, 100 + 100 - 20)

    def test_declared_latency_and_tolerance(self):
        plan = compile_plan([Segment("a", "s", 0, 8, 0)], SRC, 48000)
        self.assertEqual(plan.latency, 0)
        self.assertEqual(plan.tolerance, 1e-9)


if __name__ == "__main__":
    unittest.main()
