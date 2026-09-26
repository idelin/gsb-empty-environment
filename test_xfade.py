"""Sample-by-sample reference checks for the xfade engine."""

import math
import random
import unittest

import fixtures
import xfade
from xfade import (
    Curve,
    GainConstraintError,
    Segment,
    ShortPolicy,
    ShortSegmentError,
    SourceBoundsError,
    UnknownSourceError,
)

DELTA = 1e-12  # exact-order reference comparisons


def small_sources():
    # Distinct offsets make mis-mapped samples easy to spot.
    return {
        "a": [float(i) for i in range(64)],
        "b": [100.0 + i for i in range(64)],
        "c": [float(-i) for i in range(64)],
    }


def ref_envelope(fin, fout, length, curve, gain, t):
    """Independent reference for the documented envelope rules."""
    w = gain
    if fin and t < fin:
        x = t / fin
        w *= x if curve is Curve.LINEAR else math.sin(0.5 * math.pi * x)
    if fout and t >= length - fout:
        k = t - (length - fout)
        x = k / fout
        w *= (1.0 - x) if curve is Curve.LINEAR else math.cos(0.5 * math.pi * x)
    return w


class LinearCrossfadeTest(unittest.TestCase):
    def test_sample_exact(self):
        sources = small_sources()
        segs = [
            Segment("a", 0, 8, 0, name="A"),
            Segment("b", 0, 8, 4, name="B"),  # overlap [4, 8), fade 4
        ]
        out, comp = xfade.render(segs, sources, 4, Curve.LINEAR)
        self.assertEqual(comp.output_length, 12)
        # Independent per-sample reference.
        expected = [0.0] * 12
        for t in range(8):
            wa = ref_envelope(0, 4, 8, Curve.LINEAR, 1.0, t)
            expected[0 + t] += wa * sources["a"][t]
        for t in range(8):
            wb = ref_envelope(4, 0, 8, Curve.LINEAR, 1.0, t)
            expected[4 + t] += wb * sources["b"][t]
        for got, want in zip(out, expected):
            self.assertAlmostEqual(got, want, delta=DELTA)
        # Linear weights of an aligned pair sum to exactly 1 per sample.
        for k in range(4):
            total = (k / 4) + (1.0 - k / 4)
            self.assertAlmostEqual(total, 1.0, delta=DELTA)


class EqualPowerCrossfadeTest(unittest.TestCase):
    def test_sample_exact_and_power(self):
        sources = small_sources()
        segs = [Segment("a", 0, 8, 0), Segment("b", 0, 8, 4)]
        out, comp = xfade.render(segs, sources, 4, Curve.EQUAL_POWER)
        for k in range(4):
            angle = 0.5 * math.pi * k / 4
            wa = math.cos(angle)
            wb = math.sin(angle)
            self.assertAlmostEqual(wa * wa + wb * wb, 1.0, delta=DELTA)
            want = wa * sources["a"][4 + k] + wb * sources["b"][k]
            self.assertAlmostEqual(out[4 + k], want, delta=DELTA)
        # Amplitude sum peaks at sqrt(2); the compiled peak respects it.
        self.assertLessEqual(
            comp.peak_total_gain, math.sqrt(2.0) + xfade.TOLERANCE
        )


class ConcatCountTest(unittest.TestCase):
    def test_crossfade_counted_once(self):
        sources = small_sources()
        segs = xfade.concatenate(
            [Segment("a", 0, 10, 0), Segment("b", 0, 8, 0),
             Segment("c", 0, 12, 0)],
            fade_len=3,
        )
        self.assertEqual([s.position for s in segs], [0, 7, 12])
        out, comp = xfade.render(segs, sources, 3)
        self.assertEqual(comp.output_length, 10 + 8 + 12 - 2 * 3)


class ShortSegmentTest(unittest.TestCase):
    def test_shorten_sample_exact(self):
        sources = small_sources()
        # Middle segment length 6, fade 4 on both sides -> 4+4 > 6:
        # shortened to fade_in = 4*6//8 = 3, fade_out = 3.
        segs = [
            Segment("a", 0, 10, 0),
            Segment("b", 0, 6, 6),
            Segment("c", 0, 10, 9),
        ]
        out, comp = xfade.render(segs, sources, 4, Curve.LINEAR,
                                 max_total_gain=4.0)
        env = comp.envelopes[1]
        self.assertEqual((env.fade_in, env.fade_out), (3, 3))
        expected = [0.0] * comp.output_length
        for seg, (fin, fout) in zip(
            segs, [(0, 4), (3, 3), (4, 0)]
        ):
            src = sources[seg.source]
            for t in range(seg.length):
                w = ref_envelope(fin, fout, seg.length, Curve.LINEAR, 1.0, t)
                expected[seg.position + t] += w * src[t]
        for got, want in zip(out, expected):
            self.assertAlmostEqual(got, want, delta=DELTA)

    def test_reject_policy(self):
        sources = small_sources()
        segs = [Segment("a", 0, 10, 0), Segment("b", 0, 3, 7)]
        with self.assertRaises(ShortSegmentError):
            xfade.render(segs, sources, 4, Curve.LINEAR,
                         short_policy=ShortPolicy.REJECT)
        # Same layout is fine under SHORTEN (gain ceiling raised: the
        # inner segment also fades out, so the sum briefly exceeds 1).
        xfade.render(segs, sources, 4, Curve.LINEAR,
                     short_policy=ShortPolicy.SHORTEN, max_total_gain=2.0)


class TripleOverlapTest(unittest.TestCase):
    def test_overlapping_fades_sample_exact(self):
        sources = small_sources()
        # Fade zones overlap each other: A fade-out [6,10), B fade-in
        # [6,10) and fade-out [10,14), C fade-in [9,13).
        segs = [
            Segment("a", 0, 10, 0),
            Segment("b", 0, 8, 6),
            Segment("c", 0, 8, 9),
        ]
        out, comp = xfade.render(segs, sources, 4, Curve.LINEAR,
                                 max_total_gain=3.0)
        expected = [0.0] * comp.output_length
        for seg, (fin, fout) in zip(segs, [(0, 4), (4, 4), (4, 0)]):
            src = sources[seg.source]
            for t in range(seg.length):
                w = ref_envelope(fin, fout, seg.length, Curve.LINEAR, 1.0, t)
                expected[seg.position + t] += w * src[t]
        for got, want in zip(out, expected):
            self.assertAlmostEqual(got, want, delta=DELTA)
        # Spot-check the triple-fade sample (output 9): A out k=3 -> 0.25,
        # B in t=3 -> 0.75, C in t=0 -> 0.
        self.assertAlmostEqual(
            out[9], 0.25 * 9.0 + 0.75 * 103.0 + 0.0 * 0.0, delta=DELTA
        )

    def test_total_gain_constraint_enforced(self):
        sources = small_sources()
        # Three linear fades coincide: sum exceeds 1.0 at output 9.
        segs = [
            Segment("a", 0, 10, 0),
            Segment("b", 0, 10, 6),
            Segment("c", 0, 10, 8),
        ]
        with self.assertRaises(GainConstraintError):
            xfade.render(segs, sources, 4, Curve.LINEAR)
        # Equal-power default limit is sqrt(2); this also exceeds it.
        with self.assertRaises(GainConstraintError):
            xfade.render(segs, sources, 4, Curve.EQUAL_POWER)
        # Explicitly raising the ceiling renders the same layout.
        out, comp = xfade.render(segs, sources, 4, Curve.LINEAR,
                                 max_total_gain=2.0)
        self.assertGreater(comp.peak_total_gain, 1.0)
        self.assertEqual(len(out), 18)


class ReversedSegmentTest(unittest.TestCase):
    def test_sample_exact_and_mapping(self):
        sources = small_sources()
        segs = [
            Segment("a", 0, 8, 0),
            Segment("b", 2, 5, 6, reversed=True),  # overlap, fade 2
        ]
        out, comp = xfade.render(segs, sources, 2, Curve.LINEAR)
        # Reversed segment reads source [2, 7) backwards: 106..102.
        # Fade-in (2 samples) applies in output time; "a" fades out over
        # its last 2 samples, so output 6/7 mix both segments.
        self.assertAlmostEqual(out[6], 1.0 * 6.0 + (0 / 2) * 106.0,
                               delta=DELTA)
        self.assertAlmostEqual(out[7], 0.5 * 7.0 + (1 / 2) * 105.0,
                               delta=DELTA)
        self.assertAlmostEqual(out[8], 1.0 * 104.0, delta=DELTA)
        m = comp.maps[1]
        for s in range(2, 7):
            o = m.source_to_output(s)
            self.assertEqual(m.output_to_source(o), s)
            self.assertEqual(o, 6 + (6 - s))


class EmptyAndBoundsTest(unittest.TestCase):
    def test_empty_segment_is_noop(self):
        sources = small_sources()
        base = [Segment("a", 0, 8, 0), Segment("b", 0, 8, 4)]
        out1, comp1 = xfade.render(base, sources, 4)
        with_empty = base + [Segment("c", 10, 0, 2, name="empty")]
        out2, comp2 = xfade.render(with_empty, sources, 4)
        self.assertEqual(out1, out2)
        self.assertEqual(len(comp2.maps), 2)  # no mapping entry for it

    def test_out_of_bounds_rejected(self):
        sources = small_sources()
        with self.assertRaises(SourceBoundsError):
            xfade.render([Segment("a", 60, 8, 0)], sources, 0)
        with self.assertRaises(SourceBoundsError):
            xfade.render([Segment("a", -1, 8, 0)], sources, 0)
        with self.assertRaises(UnknownSourceError):
            xfade.render([Segment("nope", 0, 4, 0)], sources, 0)
        # Boundary-exact interval is accepted (no out-of-bounds read).
        out, _ = xfade.render([Segment("a", 56, 8, 0)], sources, 0)
        self.assertEqual(out, [float(i) for i in range(56, 64)])


class MappingTest(unittest.TestCase):
    def test_roundtrip_and_contributions(self):
        sources = small_sources()
        segs = [
            Segment("a", 3, 10, 0, name="A"),
            Segment("b", 5, 10, 7, reversed=True, name="B"),
        ]
        out, comp = xfade.render(segs, sources, 3, Curve.EQUAL_POWER)
        for m in comp.maps:
            for s in range(m.source_start, m.source_end):
                o = m.source_to_output(s)
                self.assertEqual(m.output_to_source(o), s)
        for o in range(comp.output_length):
            total = sum(
                w * sources[comp.segments[i].source][idx]
                for i, idx, w in comp.output_to_sources(o)
            )
            self.assertAlmostEqual(out[o], total, delta=DELTA)


class IncrementalEditTest(unittest.TestCase):
    def test_random_edits_match_full_render(self):
        sources = small_sources()
        rng = random.Random(7)
        sess = xfade.EditSession(sources, 3, Curve.LINEAR,
                                 max_total_gain=8.0)
        names = ["a", "b", "c"]
        live = []
        for step in range(60):
            kind = rng.choice(["add", "remove", "move", "replace"])
            try:
                if kind == "add" or not live:
                    seg = Segment(
                        rng.choice(names), rng.randrange(0, 40),
                        rng.randrange(1, 20), rng.randrange(0, 50),
                        reversed=rng.random() < 0.3,
                        name=f"e{step}",
                    )
                    res = sess.apply(("add", seg))
                    live.append(seg.name)
                elif kind == "remove":
                    victim = rng.choice(live)
                    res = sess.apply(("remove", victim))
                    live.remove(victim)
                elif kind == "move":
                    victim = rng.choice(live)
                    res = sess.apply(("move", victim, rng.randrange(0, 50)))
                else:
                    victim = rng.choice(live)
                    seg = Segment(
                        rng.choice(names), rng.randrange(0, 40),
                        rng.randrange(1, 20), 0,
                    )
                    res = sess.apply(("replace", victim, seg))
            except (GainConstraintError, ShortSegmentError,
                    SourceBoundsError):
                continue  # rejected edit: session must be unchanged
            a, b = res.affected_output
            self.assertLessEqual(a, b)
            self.assertEqual(res.output_length, len(sess.output))
            # Incremental state must equal a full re-render bit-exactly.
            self.assertEqual(sess.verify(), 0.0, msg=f"after step {step}")
        self.assertGreater(len(sess.output), 0)


class PlanExecutionTest(unittest.TestCase):
    def test_random_chunked_execution_equivalent(self):
        sources = fixtures.make_sources()
        segs = xfade.concatenate(
            [
                Segment("lead", 0, 900, 0),
                Segment("noise", 100, 700, 0),
                Segment("pad", 0, 800, 0, reversed=True),
            ],
            fade_len=96,
        )
        out, _ = xfade.render(segs, sources, 96, Curve.EQUAL_POWER)
        plan = xfade.build_plan(segs, sources, 96, Curve.EQUAL_POWER,
                                block=64)
        for seed in range(10):
            chunked = xfade.execute_plan(plan, sources, seed=seed)
            worst = max(abs(x - y) for x, y in zip(out, chunked))
            self.assertLess(worst, xfade.TOLERANCE)


class DeclarationsTest(unittest.TestCase):
    def test_latency_and_tolerance_declared(self):
        self.assertEqual(xfade.LATENCY_SAMPLES, 0)
        self.assertGreater(xfade.TOLERANCE, 0.0)
        self.assertEqual(fixtures.SAMPLE_RATE, 48000)

    def test_empty_timeline(self):
        out, comp = xfade.render([], small_sources(), 4)
        self.assertEqual(out, [])
        self.assertEqual(comp.output_length, 0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
