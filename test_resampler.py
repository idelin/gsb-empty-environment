"""Unittest suite for the polyphase resampler. Standard library only."""

import math
import unittest
from fractions import Fraction

import fixtures
from resampler import (EDGE, GAP_CONTAMINATED, NUMERICAL_TOLERANCE, VALID,
                       Channel, Kernel, ResamplerClock, contaminated_ranges)


# --------------------------------------------------------------------------
# Independent naive reference (spec re-derived, O(n * kernel) per output).
# --------------------------------------------------------------------------

def _ceil_frac(x):
    return -((-x.numerator) // x.denominator)


def _floor_frac(x):
    return x.numerator // x.denominator


def _ref_kernel_value(taps, index):
    """Linear-interpolated tap value, written independently of the library."""
    if index < 0 or index > len(taps) - 1:
        return 0.0
    i = _floor_frac(index)
    r = float(index - i)
    if r == 0.0:
        return taps[i]
    j = min(i + 1, len(taps) - 1)
    return taps[i] * (1.0 - r) + taps[j] * r


def reference_run(samples, gaps, regimes, count):
    """Direct reference: build the output grid, then convolve naively.

    regimes: list of (start:int, interp:int, decim:int, taps:list[float]).
    Returns list of (time:Fraction, value:float, contaminated:bool).
    """
    grid = []
    ridx = 0
    t = Fraction(0)
    last = None
    for _ in range(count):
        while ridx + 1 < len(regimes) and t >= Fraction(regimes[ridx + 1][0]):
            ridx += 1
            start, interp, _, _ = regimes[ridx]
            g = Fraction(_ceil_frac(Fraction(start) * interp), interp)
            while last is not None and g <= last:
                g += Fraction(1, interp)
            t = g
        grid.append((t, ridx))
        last = t
        _, interp, decim, _ = regimes[ridx]
        t += Fraction(decim, interp)

    gapset = set(gaps)
    out = []
    for t, ridx in grid:
        _, interp, _, taps = regimes[ridx]
        c = Fraction(len(taps) - 1, 2)
        j_lo = _ceil_frac(t - (Fraction(len(taps) - 1, interp) - c / interp))
        j_hi = _floor_frac(t + c / interp)
        kj = (t - j_lo) * interp + c  # decremented by interp per step
        acc = 0.0
        dirty = False
        for j in range(j_lo, j_hi + 1):
            h = _ref_kernel_value(taps, kj)
            kj -= interp
            if j < 0 or j >= len(samples) or j in gapset:
                if j in gapset:
                    dirty = True
                continue  # zeros outside the stream; gaps contribute 0.0
            acc += samples[j] * h
        out.append((t, acc, dirty))
    return out


def run_oneshot(samples_with_gaps, clock):
    ch = clock.add_channel()
    return ch.process(samples_with_gaps)


# --------------------------------------------------------------------------

class ResamplerTest(unittest.TestCase):

    def make_clock(self, half_width=8):
        taps = fixtures.design_lowpass(2, 3, half_width)
        clock = ResamplerClock(fixtures.INPUT_RATE, 2, 3, Kernel(taps, 2))
        return clock, taps

    # -- impulse response matches the direct reference --------------------

    def test_impulse_matches_direct_reference(self):
        clock, taps = self.make_clock()
        n = 4000
        data = fixtures.impulse(n, at=2000)
        outs = run_oneshot(data, clock)
        ref = reference_run(data, (), [(0, 2, 3, taps)], len(outs))
        self.assertEqual(len(outs), len(ref))
        worst = 0.0
        for o, (t, v, _) in zip(outs, ref):
            self.assertEqual(o.time, t)               # exact rational grid
            worst = max(worst, abs(o.value - v))
        self.assertLessEqual(worst, NUMERICAL_TOLERANCE)
        # Steady-state grid is exactly n * 3/2 input samples.
        for k, o in enumerate(outs):
            self.assertEqual(o.time, Fraction(3 * k, 2))

    def test_multi_rate_matches_direct_reference(self):
        half_width = 6
        taps_a = fixtures.design_lowpass(2, 3, half_width)
        taps_b = fixtures.design_lowpass(3, 4, half_width)
        clock = ResamplerClock(fixtures.INPUT_RATE, 2, 3, Kernel(taps_a, 2))
        clock.set_ratio(3, 4, at=997, kernel=Kernel(taps_b, 3))
        n = 3000
        data = fixtures.mixed_signal(n)
        outs = run_oneshot(data, clock)
        ref = reference_run(data, (), [(0, 2, 3, taps_a),
                                       (997, 3, 4, taps_b)], len(outs))
        worst = 0.0
        for o, (t, v, _) in zip(outs, ref):
            self.assertEqual(o.time, t)
            worst = max(worst, abs(o.value - v) / max(1.0, abs(v)))
        self.assertLessEqual(worst, NUMERICAL_TOLERANCE)

    # -- chunked / empty / short blocks equal one-shot ---------------------

    def test_random_chunks_equal_oneshot(self):
        clock, _ = self.make_clock()
        n = 5000
        data = fixtures.mixed_signal(n)
        ref = run_oneshot(data, clock)

        ch = clock.add_channel()
        got = []
        pos = 0
        sizes = fixtures.chunk_split(n, seed=7)  # includes 0- and 1-blocks
        self.assertIn(0, sizes)
        self.assertIn(1, sizes)
        for k in sizes:
            got.extend(ch.process(data[pos:pos + k]))
            pos += k
        self.assertEqual(pos, n)
        self.assertEqual(ref, got)  # bit-identical, including flags/times

    # -- checkpoint / restore inside the filter tail -----------------------

    def test_checkpoint_restore_in_filter_tail(self):
        taps_a = fixtures.design_lowpass(2, 3, 6)
        taps_b = fixtures.design_lowpass(3, 4, 6)
        clock = ResamplerClock(fixtures.INPUT_RATE, 2, 3, Kernel(taps_a, 2))
        clock.set_ratio(3, 4, at=997, kernel=Kernel(taps_b, 3))
        n = 4000
        data = fixtures.mixed_signal(n)

        ch = clock.add_channel()
        # Feed just past the switch so the next outputs sit in the tail:
        # their kernel windows still cover pre-switch samples.
        ch.process(data[:1005])
        nxt = ch.next_output_time
        self.assertIsNotNone(nxt)
        self.assertLess(nxt - ch.latency, 997)  # window straddles the switch
        self.assertGreaterEqual(nxt, 997)
        snap = ch.snapshot()

        tail_ref = ch.process(data[1005:])
        self.assertTrue(tail_ref)

        ch.restore(snap)
        tail_got = ch.process(data[1005:])
        self.assertEqual(tail_ref, tail_got)  # exact, phase included

    def test_checkpoint_restore_with_rechunked_input(self):
        clock, _ = self.make_clock()
        n = 3000
        data = fixtures.mixed_signal(n)
        ch = clock.add_channel()
        ch.process(data[:1500])
        snap = ch.snapshot()
        ref = ch.process(data[1500:])
        ch.restore(snap)
        got = []
        pos = 1500
        for k in fixtures.chunk_split(n - 1500, seed=99):
            got.extend(ch.process(data[pos:pos + k]))
            pos += k
        self.assertEqual(ref, got)

    # -- ratio switch at a non-integer phase -------------------------------

    def test_switch_times_monotonic_no_repeat_no_backward(self):
        taps_a = fixtures.design_lowpass(2, 3, 6)
        taps_b = fixtures.design_lowpass(3, 4, 6)
        clock = ResamplerClock(fixtures.INPUT_RATE, 2, 3, Kernel(taps_a, 2))
        # 997 is not on the 1/3 grid of the new regime -> non-integer phase.
        clock.set_ratio(3, 4, at=997, kernel=Kernel(taps_b, 3))
        outs = run_oneshot(fixtures.mixed_signal(3000), clock)
        times = [o.time for o in outs]
        for a, b in zip(times, times[1:]):
            self.assertLess(a, b)  # strictly increasing: no repeat/backward
        # Locate the regime boundary.
        first_new = next(o for o in outs if o.time >= 997)
        i = outs.index(first_new)
        self.assertGreaterEqual(first_new.time, 997)
        self.assertLess(first_new.time, Fraction(997) + Fraction(1, 3))
        self.assertEqual((first_new.time * 3).denominator, 1)  # on new grid
        for o in outs[i + 1:]:
            self.assertEqual((o.time * 3).denominator, 1)
        # Steps are exactly the rational periods on both sides.
        for a, b in zip(outs[:i - 1], outs[1:i]):
            self.assertEqual(b.time - a.time, Fraction(3, 2))
        for a, b in zip(outs[i:], outs[i + 1:]):
            self.assertEqual(b.time - a.time, Fraction(4, 3))

    def test_switch_point_must_advance(self):
        clock, _ = self.make_clock()
        taps = fixtures.design_lowpass(3, 4, 4)
        clock.set_ratio(3, 4, at=500, kernel=Kernel(taps, 3))
        with self.assertRaises(ValueError):
            clock.set_ratio(1, 1, at=400, kernel=Kernel([1.0], 1))

    def test_fractional_switch_point(self):
        taps_a = fixtures.design_lowpass(2, 3, 6)
        taps_b = fixtures.design_lowpass(3, 4, 6)
        clock = ResamplerClock(fixtures.INPUT_RATE, 2, 3, Kernel(taps_a, 2))
        clock.set_ratio(3, 4, at=Fraction(1999, 2), kernel=Kernel(taps_b, 3))
        outs = run_oneshot(fixtures.mixed_signal(3000), clock)
        times = [o.time for o in outs]
        for a, b in zip(times, times[1:]):
            self.assertLess(a, b)
        first_new = next(o for o in outs if o.time >= Fraction(1999, 2))
        # smallest 1/3-grid point >= 999.5 is 2999/3
        self.assertEqual(first_new.time, Fraction(2999, 3))

    def test_kernel_value_at_fractional_index(self):
        k = Kernel([0.0, 1.0, 0.0], 1)
        self.assertEqual(k.value_at(Fraction(1)), 1.0)
        self.assertAlmostEqual(k.value_at(Fraction(1, 2)), 0.5)
        self.assertAlmostEqual(k.value_at(Fraction(3, 2)), 0.5)
        self.assertEqual(k.value_at(Fraction(-1)), 0.0)
        self.assertEqual(k.value_at(Fraction(3)), 0.0)

    # -- gaps: marked, never filled with previous values -------------------

    def test_gap_marks_contaminated_range_and_contributes_zero(self):
        clock, taps = self.make_clock()
        n = 1200
        data = fixtures.mixed_signal(n)
        gaps = [600]
        outs = run_oneshot(fixtures.with_gaps(data, gaps), clock)

        # 1) Contaminated outputs are exactly those whose window covers 600.
        latency = Fraction(len(taps) - 1, 2 * 2)
        lo, hi = Fraction(600) - latency, Fraction(600) + latency
        flagged = [o for o in outs if o.flags & GAP_CONTAMINATED]
        self.assertTrue(flagged)
        for o in outs:
            in_window = lo <= o.time <= hi
            self.assertEqual(bool(o.flags & GAP_CONTAMINATED), in_window,
                             f"flag mismatch at t={o.time}")
        ranges = contaminated_ranges(outs)
        self.assertEqual(len(ranges), 1)
        self.assertEqual(ranges[0][0], flagged[0].time)
        self.assertEqual(ranges[0][1], flagged[-1].time)

        # 2) A gap contributes exactly 0.0 (same values as explicit zeros).
        zeros = list(data)
        zeros[600] = 0.0
        outs_zero = run_oneshot(zeros, clock)
        self.assertEqual([o.value for o in outs],
                         [o.value for o in outs_zero])
        self.assertTrue(all(not (o.flags & GAP_CONTAMINATED)
                            for o in outs_zero))

        # 3) It is NOT the previous sample value (no sample-and-hold).
        held = list(data)
        held[600] = data[599]
        outs_held = run_oneshot(held, clock)
        diffs = [abs(a.value - b.value)
                 for a, b in zip(outs, outs_held)
                 if a.flags & GAP_CONTAMINATED]
        self.assertTrue(diffs)
        self.assertGreater(max(diffs), 1e-6)

    def test_gaps_are_per_channel(self):
        clock, _ = self.make_clock()
        data = fixtures.mixed_signal(800)
        ch_a = clock.add_channel()
        ch_b = clock.add_channel()
        out_a = ch_a.process(data)
        out_b = ch_b.process(fixtures.with_gaps(data, [400]))
        self.assertTrue(all(o.flags == VALID or not (o.flags & GAP_CONTAMINATED)
                            for o in out_a))
        self.assertTrue(any(o.flags & GAP_CONTAMINATED for o in out_b))
        self.assertEqual([o.time for o in out_a], [o.time for o in out_b])

    # -- shared clock: different processing rates must not drift -----------

    def test_shared_clock_no_drift(self):
        taps_a = fixtures.design_lowpass(2, 3, 6)
        taps_b = fixtures.design_lowpass(3, 4, 6)
        clock = ResamplerClock(fixtures.INPUT_RATE, 2, 3, Kernel(taps_a, 2))
        clock.set_ratio(3, 4, at=1000, kernel=Kernel(taps_b, 3))
        n = 6000
        data = fixtures.mixed_signal(n)

        ch_a = clock.add_channel()
        ch_b = clock.add_channel()
        out_a, out_b = [], []
        pos = 0
        for k in fixtures.chunk_split(n, seed=1):
            out_a.extend(ch_a.process(data[pos:pos + k]))
            pos += k
        pos = 0
        for k in fixtures.chunk_split(n, seed=2):  # different chunking
            out_b.extend(ch_b.process(data[pos:pos + k]))
            pos += k

        self.assertEqual(len(out_a), len(out_b))
        for a, b in zip(out_a, out_b):
            self.assertEqual(a.index, b.index)
            self.assertEqual(a.time, b.time)        # exact rationals: 0 drift
            self.assertEqual(a.timestamp, b.timestamp)
            self.assertEqual(a.value, b.value)
        # Spot-check the exact rational time of a late output.
        last = out_a[-1]
        self.assertIsInstance(last.time, Fraction)
        self.assertAlmostEqual(last.timestamp,
                               float(last.time) / fixtures.INPUT_RATE)

    # -- declared latency / bounded state ----------------------------------

    def test_latency_bound_respected(self):
        clock, taps = self.make_clock()
        ch = clock.add_channel()
        latency = ch.latency
        self.assertEqual(latency, Fraction(len(taps) - 1, 2 * 2))
        data = fixtures.mixed_signal(500)
        received = 0
        for sample in data:
            received += 1
            for o in ch.process([sample]):
                # Emitted only once input covers t + latency.
                self.assertLessEqual(
                    _floor_frac(o.time + latency), received - 1)

    def test_bounded_memory(self):
        clock, taps = self.make_clock()
        ch = clock.add_channel()
        span = math.ceil(Fraction(len(taps) - 1, 2))
        # history <= kernel span + latency + one output step + margin
        bound = span + math.ceil(ch.latency) + 2 + 2
        pos = 0
        data = fixtures.mixed_signal(20000)
        for k in fixtures.chunk_split(len(data), seed=3):
            ch.process(data[pos:pos + k])
            pos += k
            self.assertLessEqual(ch.buffered_samples, bound)

    def test_edge_flag_at_stream_start(self):
        clock, taps = self.make_clock()
        outs = run_oneshot(fixtures.mixed_signal(200), clock)
        latency = Fraction(len(taps) - 1, 2 * 2)
        left = Fraction(len(taps) - 1, 2) - latency  # span - latency
        for o in outs:
            self.assertEqual(bool(o.flags & EDGE),
                             _ceil_frac(o.time - left) < 0)

    def test_empty_input_produces_no_output(self):
        clock, _ = self.make_clock()
        ch = clock.add_channel()
        self.assertEqual(ch.process([]), [])
        self.assertEqual(ch.process([None, None]), [])  # gaps still count
        self.assertEqual(ch.buffered_samples, 2)


if __name__ == "__main__":
    unittest.main()
