import math
import random
import unittest

from xsplice import EQUAL_POWER, LINEAR, Segment, compile_plan, render, render_chunked, render_range

from reference import ref_entry, ref_render

TOL = 1e-12  # declared numeric tolerance for sample comparisons


def ramp_source(n):
    # Distinct, non-symmetric values so index errors cannot hide.
    return [0.001 * i + 0.25 for i in range(n)]


class PerSampleReferenceTest(unittest.TestCase):
    def assert_render_matches_reference(self, segments, sources):
        plan = compile_plan(segments, sources, 48000)
        entries = [ref_entry(s) for s in segments if not s.is_empty]
        expected = ref_render(entries, sources, plan.total)
        actual = render(plan)
        self.assertEqual(len(actual), len(expected))
        for t, (got, want) in enumerate(zip(actual, expected)):
            self.assertAlmostEqual(got, want, delta=TOL, msg=f"sample {t}")

    def test_short_segment_with_shortened_fades(self):
        # fade_in + fade_out = 8 > duration 5 -> shortened to (2, 2).
        sources = {"s": ramp_source(5)}
        seg = Segment("a", "s", 0, 5, 0, fade_in=4, fade_out=4, curve=LINEAR)
        self.assert_render_matches_reference([seg], sources)

    def test_short_segment_equal_power(self):
        sources = {"s": ramp_source(7)}
        seg = Segment("a", "s", 0, 7, 0, fade_in=6, fade_out=6, curve=EQUAL_POWER)
        self.assert_render_matches_reference([seg], sources)

    def test_triple_overlap_chain(self):
        # A[0,10) fades out over [6,10); B[6,14) fades in then out;
        # C[10,18) fades in.  Fade regions touch but never re-overlap.
        sources = {"s": ramp_source(32), "u": ramp_source(32)}
        segs = [
            Segment("a", "s", 0, 10, 0, fade_out=4),
            Segment("b", "u", 2, 10, 6, fade_in=4, fade_out=4),
            Segment("c", "s", 10, 18, 10, fade_in=4),
        ]
        self.assert_render_matches_reference(segs, sources)

    def test_triple_overlap_mixed_curves(self):
        sources = {"s": ramp_source(32)}
        segs = [
            Segment("a", "s", 0, 10, 0, fade_out=4, curve=EQUAL_POWER),
            Segment("b", "s", 4, 12, 6, fade_in=4, fade_out=4, curve=EQUAL_POWER),
            Segment("c", "s", 8, 16, 10, fade_in=4, curve=EQUAL_POWER),
        ]
        self.assert_render_matches_reference(segs, sources)

    def test_reversed_segment(self):
        sources = {"s": ramp_source(16)}
        seg = Segment("a", "s", 16, 0, 0)
        self.assert_render_matches_reference([seg], sources)
        plan = compile_plan([seg], sources, 48000)
        out = render(plan)
        self.assertEqual(out, list(reversed(sources["s"])))

    def test_reversed_segment_with_fades_and_overlap(self):
        sources = {"s": ramp_source(32), "u": ramp_source(32)}
        segs = [
            Segment("a", "s", 20, 4, 0, fade_out=4),          # reversed
            Segment("b", "u", 0, 16, 12, fade_in=4),          # forward
        ]
        self.assert_render_matches_reference(segs, sources)

    def test_reversed_short_segment(self):
        sources = {"s": ramp_source(8)}
        seg = Segment("a", "s", 6, 1, 0, fade_in=5, fade_out=5)
        self.assert_render_matches_reference([seg], sources)


class CrossfadeShapeTest(unittest.TestCase):
    def test_linear_crossfade_of_unity_is_constant_amplitude(self):
        sources = {"s": [1.0] * 16}
        segs = [
            Segment("a", "s", 0, 16, 0, fade_out=8),
            Segment("b", "s", 0, 16, 8, fade_in=8),
        ]
        for v in render(compile_plan(segs, sources, 48000)):
            self.assertAlmostEqual(v, 1.0, delta=TOL)

    def test_equal_power_crossfade_of_unity_is_constant_power(self):
        sources = {"s": [1.0] * 16}
        segs = [
            Segment("a", "s", 0, 16, 0, fade_out=8, curve=EQUAL_POWER),
            Segment("b", "s", 0, 16, 8, fade_in=8, curve=EQUAL_POWER),
        ]
        plan = compile_plan(segs, sources, 48000)
        for t in range(8, 16):
            x = (t - 8) / 8
            want = math.cos(0.5 * math.pi * x) + math.sin(0.5 * math.pi * x)
            self.assertAlmostEqual(render_range(plan, t, t + 1)[0], want, delta=TOL)


class ChunkedExecutionTest(unittest.TestCase):
    def make_plan(self):
        sources = {"s": ramp_source(64), "u": ramp_source(64)}
        segs = [
            Segment("a", "s", 0, 20, 0, fade_out=6, curve=EQUAL_POWER),
            Segment("b", "u", 3, 25, 14, fade_in=6, fade_out=5),
            Segment("c", "s", 40, 10, 31, fade_in=5),  # reversed tail
        ]
        return compile_plan(segs, sources, 48000)

    def test_random_chunking_is_bit_identical(self):
        plan = self.make_plan()
        whole = render(plan)
        rng = random.Random(20260926)
        for _ in range(50):
            sizes = [rng.randint(1, 17) for _ in range(rng.randint(1, 12))]
            self.assertEqual(render_chunked(plan, sizes), whole)

    def test_single_sample_chunks(self):
        plan = self.make_plan()
        self.assertEqual(render_chunked(plan, [1] * plan.total), render(plan))

    def test_render_range_matches_whole_render(self):
        plan = self.make_plan()
        whole = render(plan)
        for start in range(0, plan.total, 7):
            end = min(start + 7, plan.total)
            self.assertEqual(render_range(plan, start, end), whole[start:end])

    def test_empty_segment_does_not_change_render(self):
        sources = {"s": ramp_source(16)}
        real = Segment("a", "s", 0, 16, 0, fade_in=4)
        empty = Segment("e", "s", 5, 5, 3)
        self.assertEqual(
            render(compile_plan([real], sources, 48000)),
            render(compile_plan([real, empty], sources, 48000)),
        )


if __name__ == "__main__":
    unittest.main()
