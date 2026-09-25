"""unittest suite for the rational-ratio polyphase FIR resampler.

Run: python3 -m unittest test_resampler -v
"""

from __future__ import annotations

import math
import unittest
from fractions import Fraction

import fixtures
from resampler import (
    FLAG_CONTAMINATED,
    FLAG_TAIL,
    Resampler,
    SharedClock,
    TOLERANCE,
)


def direct_reference(x, h, L, M, count):
    """Naive up-sample -> convolve -> down-sample reference."""
    N = len(h)
    ref = []
    for n in range(count):
        u = n * M
        p, k = u % L, u // L
        acc = 0.0
        j = 0
        while p + j * L < N:
            i = k - j
            if 0 <= i < len(x):
                acc += h[p + j * L] * x[i]
            j += 1
        ref.append(acc)
    return ref


def run_all(resampler, chunks):
    outs = []
    for chunk, gaps in chunks:
        outs.extend(resampler.process(chunk, gaps))
    while True:
        tail = resampler.flush()
        outs.extend(tail)
        if resampler.done:
            break
    return outs


class ImpulseResponseTest(unittest.TestCase):
    """Impulse response must match the direct reference."""

    def check(self, L, M, taps, n_in):
        h = fixtures.lowpass_kernel(taps, L, M)
        x = fixtures.impulse(n_in)
        r = Resampler(h, L, M, fs_in=fixtures.FS_AUDIO)
        outs = run_all(r, [(x, ())])
        ref = direct_reference(x, h, L, M, len(outs))
        self.assertEqual(len(outs), len(ref))
        err = max(abs(o.value - v) for o, v in zip(outs, ref))
        self.assertLess(err, 1e-12)
        # positions must sit exactly on the polyphase grid
        for n, o in enumerate(outs):
            self.assertEqual(o.source_position, Fraction(n * M, L))

    def test_impulse_upsample_3_2(self):
        self.check(L=3, M=2, taps=25, n_in=64)

    def test_impulse_downsample_2_3(self):
        self.check(L=2, M=3, taps=33, n_in=96)

    def test_impulse_upsample_4_1(self):
        self.check(L=4, M=1, taps=33, n_in=48)


class LatencyTest(unittest.TestCase):
    def test_declared_latency(self):
        L, M, taps = 3, 2, 25
        h = fixtures.lowpass_kernel(taps, L, M)
        r = Resampler(h, L, M, fs_in=fixtures.FS_AUDIO)
        self.assertEqual(r.latency_samples, Fraction(taps - 1, 2 * L))
        self.assertEqual(r.latency_seconds,
                         Fraction(taps - 1, 2 * L) / fixtures.FS_AUDIO)
        outs = r.process(fixtures.impulse(8))
        # first output sits one group delay before the time origin
        self.assertEqual(outs[0].time, -r.latency_seconds)


class RatioSwitchTest(unittest.TestCase):
    """Ratio change at a non-integer phase: times never repeat/regress."""

    def test_switch_at_fractional_position(self):
        h2 = fixtures.lowpass_kernel(33, 2, 1)
        h3 = fixtures.lowpass_kernel(25, 3, 2)
        r = Resampler(h2, 2, 1, fs_in=fixtures.FS_VOICE)
        switch_at = Fraction(21, 2)  # non-integer source position
        r.set_ratio(3, 2, at_source_index=switch_at, kernel=h3)
        x = fixtures.tone_samples(120, fixtures.FS_VOICE, [440.0, 1200.0])
        outs = run_all(r, [(x, ())])

        times = [o.time for o in outs]
        pos = [o.source_position for o in outs]
        for a, b in zip(times, times[1:]):
            self.assertLess(a, b)  # strictly increasing, no duplicates
        for a, b in zip(pos, pos[1:]):
            self.assertLess(a, b)
        # the switch anchors the new grid exactly on the switch point
        self.assertIn(switch_at, pos)
        # positions after the switch live on the new (off-grid) phase
        after = [p for p in pos if p > switch_at]
        self.assertTrue(any((p - switch_at).denominator == 3 for p in after))
        # no output before the switch uses the new ratio grid
        before = [p for p in pos if p < switch_at]
        self.assertTrue(all(p.denominator in (1, 2) for p in before))

    def test_switch_output_time_base_continuous(self):
        h = fixtures.lowpass_kernel(25, 3, 2)
        r = Resampler(h, 3, 2, fs_in=fixtures.FS_VOICE)
        r.set_ratio(1, 2, at_source_index=40)
        x = fixtures.tone_samples(90, fixtures.FS_VOICE, [300.0])
        outs = run_all(r, [(x, ())])
        deltas = [b.time - a.time for a, b in zip(outs, outs[1:])]
        self.assertTrue(all(d > 0 for d in deltas))
        # spacing before/after the switch matches the declared ratios
        pre = [d for o, d in zip(outs, deltas)
               if o.source_position < 40 - 10]
        self.assertTrue(all(abs(d - Fraction(2, 3) / fixtures.FS_VOICE) == 0
                            for d in pre))


class ChunkingAndCheckpointTest(unittest.TestCase):
    """Random/empty/short chunks + tail checkpoint restore => identical."""

    def setUp(self):
        self.L, self.M, self.taps = 3, 2, 25
        self.h = fixtures.lowpass_kernel(self.taps, self.L, self.M)
        self.x = fixtures.tone_samples(500, fixtures.FS_AUDIO,
                                       [1000.0, 5000.0])

    def one_shot(self):
        r = Resampler(self.h, self.L, self.M, fs_in=fixtures.FS_AUDIO)
        return run_all(r, [(self.x, ())])

    @staticmethod
    def signature(outs):
        return [(o.time, o.value, o.flags) for o in outs]

    def test_random_chunking_identical(self):
        ref = self.signature(self.one_shot())
        for seed in (1, 2, 3):
            r = Resampler(self.h, self.L, self.M, fs_in=fixtures.FS_AUDIO)
            chunks, off = [], 0
            for s in fixtures.random_chunk_sizes(len(self.x), seed):
                chunks.append((self.x[off:off + s], ()))
                off += s
            got = self.signature(run_all(r, chunks))
            self.assertEqual(ref, got)  # bit-exact, not just within tolerance

    def test_empty_and_short_chunks(self):
        ref = self.signature(self.one_shot())
        r = Resampler(self.h, self.L, self.M, fs_in=fixtures.FS_AUDIO)
        outs = []
        outs.extend(r.process([]))            # empty block: no-op
        for i in range(len(self.x)):          # 1-sample blocks
            outs.extend(r.process(self.x[i:i + 1]))
            if i % 97 == 0:
                outs.extend(r.process([]))    # interleaved empty blocks
        outs.extend(r.flush())
        self.assertTrue(r.done)
        self.assertEqual(ref, self.signature(outs))

    def test_checkpoint_restore_in_filter_tail(self):
        ref = self.signature(self.one_shot())
        r = Resampler(self.h, self.L, self.M, fs_in=fixtures.FS_AUDIO)
        outs = list(r.process(self.x))
        outs.extend(r.flush(limit=3))         # inside the filter tail now
        self.assertFalse(r.done)
        state = r.checkpoint()
        # restore into a fresh object and finish the tail there
        r2 = Resampler.restore(state)
        tail2 = []
        while not r2.done:
            tail2.extend(r2.flush(limit=2))
        self.assertEqual(ref, self.signature(outs + tail2))

    def test_checkpoint_restore_mid_stream(self):
        ref = self.signature(self.one_shot())
        r = Resampler(self.h, self.L, self.M, fs_in=fixtures.FS_AUDIO)
        outs = list(r.process(self.x[:123]))
        r2 = Resampler.restore(r.checkpoint())
        outs.extend(self._finish(r2, self.x[123:]))
        self.assertEqual(ref, self.signature(outs))

    def _finish(self, r, rest):
        outs = list(r.process(rest))
        while not r.done:
            outs.extend(r.flush())
        return outs


class GapMarkingTest(unittest.TestCase):
    """Gaps are zero-filled (never held) and contaminated outputs flagged."""

    def test_gap_zero_fill_and_flags(self):
        L, M, taps = 2, 1, 17
        h = fixtures.lowpass_kernel(taps, L, M)
        n = 120
        x = fixtures.tone_samples(n, fixtures.FS_HIRES, [800.0])
        g0, glen = 50, 10

        # (a) gap-marked channel
        ra = Resampler(h, L, M, fs_in=fixtures.FS_HIRES)
        out_a = run_all(ra, [(x[:40], ()), (x[40:80], [(g0, glen)]),
                             (x[80:], ())])
        # (b) same data with zeros written into the gap, no marking
        xz = list(x)
        for i in range(g0, g0 + glen):
            xz[i] = 0.0
        rb = Resampler(h, L, M, fs_in=fixtures.FS_HIRES)
        out_b = run_all(rb, [(xz, ())])

        # gap samples are treated as zeros, not as held previous values
        self.assertEqual([o.value for o in out_a], [o.value for o in out_b])
        self.assertNotEqual([o.value for o in out_a],
                            [o.value for o in run_all(
                                Resampler(h, L, M, fs_in=fixtures.FS_HIRES),
                                [(x, ())])])

        # flagged outputs == outputs whose convolution window hits the gap
        flagged = {o.index for o in out_a if o.flags & FLAG_CONTAMINATED}
        expect = set()
        gd = Fraction(taps - 1, 2 * L)
        t = Fraction(0)
        idx = 0
        end = Fraction(n) + gd
        while t <= end:
            k = math.floor(t)
            lo = math.floor(t - Fraction(taps, L)) + 1
            if g0 <= k and g0 + glen > lo:
                expect.add(idx)
            t += Fraction(M, L)
            idx += 1
        self.assertEqual(flagged, expect)
        self.assertGreater(len(flagged), 0)
        # merged contaminated ranges are reported and gap-free outside
        self.assertEqual(len(ra.contaminated_ranges), 1)
        r0, r1 = ra.contaminated_ranges[0]
        self.assertLessEqual(r0, r1)

    def test_gap_flags_survive_checkpoint(self):
        L, M, taps = 2, 1, 17
        h = fixtures.lowpass_kernel(taps, L, M)
        x = fixtures.tone_samples(90, fixtures.FS_HIRES, [800.0])
        r = Resampler(h, L, M, fs_in=fixtures.FS_HIRES)
        r.process(x[:60], [(30, 5)])
        self.assertTrue(r.contaminated_ranges)
        r2 = Resampler.restore(r.checkpoint())
        self.assertEqual(r2.contaminated_ranges, r.contaminated_ranges)
        outs = run_all(r2, [(x[60:], ())])
        # later outputs are clean again: gap does not leak past its window
        self.assertFalse(any(o.flags & FLAG_CONTAMINATED for o in outs))


class SharedClockTest(unittest.TestCase):
    """Channels on one clock: different processing rates, zero drift."""

    def test_no_drift_across_channels_and_chunkings(self):
        fs = fixtures.FS_HIRES
        clock = SharedClock(fs)
        h = fixtures.lowpass_kernel(25, 3, 2)
        x = fixtures.tone_samples(600, fs, [1000.0, 7000.0])

        ch_a = clock.channel(h, 3, 1)   # 48k -> 144k
        ch_b = clock.channel(h, 3, 2)   # 48k -> 72k
        out_a = run_all(ch_a, [(x, ())])
        # channel B fed with awkward 1..7-sample blocks: different "rate"
        chunks, off, sizes = [], 0, fixtures.random_chunk_sizes(len(x), 9)
        for s in sizes:
            chunks.append((x[off:off + s], ()))
            off += s
        out_b = run_all(ch_b, chunks)

        # same source position => exactly the same timestamp (Fraction-equal)
        ta = {o.source_position: o.time for o in out_a}
        tb = {o.source_position: o.time for o in out_b}
        common = set(ta) & set(tb)
        self.assertGreater(len(common), 100)
        for p in common:
            self.assertEqual(ta[p], tb[p])

        # chunked processing must not shift B's time base at all
        ch_b2 = clock.channel(h, 3, 2)
        out_b2 = run_all(ch_b2, [(x, ())])
        self.assertEqual([o.time for o in out_b], [o.time for o in out_b2])
        self.assertEqual([o.value for o in out_b], [o.value for o in out_b2])


class BoundedResourcesTest(unittest.TestCase):
    def test_delay_line_stays_bounded(self):
        L, M, taps = 3, 2, 25
        h = fixtures.lowpass_kernel(taps, L, M)
        r = Resampler(h, L, M, fs_in=fixtures.FS_AUDIO)
        x = fixtures.tone_samples(4096, fixtures.FS_AUDIO, [1000.0])
        bound = taps // L + 2 * (M // L) + 8
        off = 0
        for s in fixtures.random_chunk_sizes(len(x), 5):
            r.process(x[off:off + s])
            off += s
            self.assertLessEqual(len(r.buf), bound)

    def test_tail_is_emitted_not_dropped(self):
        L, M, taps = 3, 2, 25
        h = fixtures.lowpass_kernel(taps, L, M)
        n = 100
        r = Resampler(h, L, M, fs_in=fixtures.FS_AUDIO)
        outs = run_all(r, [(fixtures.impulse(n), ())])
        gd = Fraction(taps - 1, 2 * L)
        # last output time reaches the end of the input (tail not dropped)
        last_t = outs[-1].source_position
        self.assertLessEqual(last_t, Fraction(n) + gd)
        self.assertGreater(last_t + Fraction(M, L), Fraction(n) + gd)
        self.assertTrue(any(o.flags & FLAG_TAIL for o in outs))
        # tail length matches the declared latency: ~ gd / step outputs
        n_tail = sum(1 for o in outs if o.flags & FLAG_TAIL)
        self.assertGreaterEqual(n_tail, int(gd / Fraction(M, L)) - 1)


if __name__ == "__main__":
    unittest.main()
