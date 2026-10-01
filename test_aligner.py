"""unittest suite for the multi-channel delay aligner.

Covers: impulse placement (integer + fractional delay), channel isolation,
chunking invariance, common valid interval + zero-fill marks, smooth delay
changes (no sudden repeat/skip of undeclared samples), retraction of
unconfirmed regions, checkpoint round-trips, missing-data contamination,
memory bound, tail flush and overlapping changes vs. the whole-segment
reference convolution.
"""

import math
import pickle
import unittest

import fixture
import reference
from aligner import MultiChannelAligner

TOL = fixture.TOLERANCE


def run_stream(inputs, chunk_size, base_delays=None, missing=None,
               schedule=None, retract=(), checkpoint_at=None):
    """Feed inputs through an aligner in chunks; return concatenated output.

    schedule:  list of (channel, position, target, transition)
    retract:   versions to retract right after scheduling (still tentative)
    checkpoint_at: if set, save a checkpoint after this many input samples,
                   restore it into a fresh aligner, and continue with that one.
    Returns (samples, zero_filled, contaminated, aligner).
    """
    num_channels = len(inputs)
    if base_delays is None:
        base_delays = fixture.DECLARED_DELAYS[:num_channels]
    aligner = MultiChannelAligner(num_channels, base_delays)
    versions = [aligner.schedule_change(*s) for s in (schedule or [])]
    for v in retract:
        aligner.retract(v)

    length = len(inputs[0])
    cols = [[] for _ in range(num_channels)]
    zf = [[] for _ in range(num_channels)]
    ct = [[] for _ in range(num_channels)]
    pos = 0
    while pos < length:
        stop = min(pos + chunk_size, length)
        chunks = [inputs[c][pos:stop] for c in range(num_channels)]
        miss = ([missing[c][pos:stop] for c in range(num_channels)]
                if missing is not None else None)
        block = aligner.process(chunks, miss)
        pos = stop
        if checkpoint_at is not None and pos >= checkpoint_at:
            payload = aligner.checkpoint()
            restored = MultiChannelAligner(num_channels, base_delays)
            restored.load_checkpoint(payload)
            aligner = restored
            checkpoint_at = None
        for c in range(num_channels):
            cols[c].extend(block.samples[c])
            zf[c].extend(block.zero_filled[c])
            ct[c].extend(block.contaminated[c])
    block = aligner.finish()
    for c in range(num_channels):
        cols[c].extend(block.samples[c])
        zf[c].extend(block.zero_filled[c])
        ct[c].extend(block.contaminated[c])
    return cols, zf, ct, aligner


def centroid(y):
    w = [v * v for v in y]
    total = sum(w)
    assert total > 0.0
    return sum(i * wi for i, wi in enumerate(w)) / total


class ImpulsePlacementTest(unittest.TestCase):
    def test_impulse_moves_to_target_position(self):
        # integer and fractional declared delays, single channel
        for delay in (12.0, 9.25, 30.5):
            pos, length = 100, 400
            cols, _, _, _ = run_stream([fixture.impulse_input(pos, length)],
                                       chunk_size=13, base_delays=[delay])
            y = cols[0]
            group = fixture.KERNEL_GROUP_DELAY
            i_int = math.floor(delay - group)
            frac = delay - group - i_int
            h = fixture.fractional_kernel(frac)
            # the fixture kernel must appear exactly at pos + I, i.e. the
            # impulse lands at pos + I + group + frac == pos + delay
            start = pos + i_int
            for k in range(fixture.KERNEL_LENGTH):
                self.assertAlmostEqual(y[start + k], h[k], delta=TOL,
                                       msg=f"delay={delay} tap={k}")
            # and nowhere else: no repeated or skipped undeclared samples
            outside = sum(v * v for v in y[:start] + y[start + fixture.KERNEL_LENGTH:])
            self.assertEqual(outside, 0.0)
            # energy centroid matches the kernel's own centroid at pos + delay
            hw = sum(c * c for c in h)
            h_centroid = sum(k * c * c for k, c in enumerate(h)) / hw
            self.assertAlmostEqual(centroid(y), pos + i_int + h_centroid,
                                   delta=TOL, msg=f"delay={delay}")

    def test_no_channel_crosstalk(self):
        pos, length = 50, 300
        inputs = [fixture.impulse_input(pos, length),
                  [0.0] * length, [0.0] * length]
        cols, _, _, _ = run_stream(inputs, chunk_size=7)
        self.assertGreater(sum(v * v for v in cols[0]), 0.5)
        for c in (1, 2):
            self.assertTrue(all(v == 0.0 for v in cols[c]),
                            f"channel {c} must stay exactly silent")


class ChunkingInvarianceTest(unittest.TestCase):
    def test_chunking_does_not_change_output(self):
        inputs = fixture.make_multichannel_input()
        schedule = [(1, 150, 20.0, 40), (2, 300, 12.5, 30)]
        results = []
        for chunk_size in (1, 3, 7, 64, 10_000):
            cols, zf, ct, _ = run_stream(inputs, chunk_size, schedule=schedule)
            results.append((cols, zf, ct))
        for got in results[1:]:
            for c in range(fixture.NUM_CHANNELS):
                self.assertEqual(results[0][0][c], got[0][c])   # exact
                self.assertEqual(results[0][1][c], got[1][c])
                self.assertEqual(results[0][2][c], got[2][c])

    def test_matches_whole_segment_reference(self):
        inputs = fixture.make_multichannel_input()
        schedule = [(1, 150, 20.0, 40), (2, 300, 12.5, 30)]
        cols, zf, ct, _ = run_stream(inputs, 17, schedule=schedule)
        changes = [[] for _ in range(fixture.NUM_CHANNELS)]
        for ch, pos, target, trans in schedule:
            changes[ch].append((pos, target, trans))
        rs, rz, rc = reference.reference_run(inputs, fixture.DECLARED_DELAYS,
                                             changes_per_channel=changes)
        for c in range(fixture.NUM_CHANNELS):
            self.assertEqual(len(rs[c]), len(cols[c]))
            for a, b in zip(rs[c], cols[c]):
                self.assertAlmostEqual(a, b, delta=TOL)
            self.assertEqual(rz[c], zf[c])
            self.assertEqual(rc[c], ct[c])


class ValidRegionTest(unittest.TestCase):
    def test_common_valid_interval_and_zero_fill_marks(self):
        length = 600
        inputs = fixture.make_multichannel_input(length)
        cols, zf, ct, aligner = run_stream(inputs, 23)
        group = fixture.KERNEL_GROUP_DELAY
        klen = fixture.KERNEL_LENGTH
        i_int = [math.floor(d - group) for d in fixture.DECLARED_DELAYS]
        # leading zero-filled region per channel: n < I + K - 1
        for c in range(fixture.NUM_CHANNELS):
            expect = i_int[c] + klen - 1
            self.assertEqual(sum(zf[c]), expect)
            self.assertTrue(all(zf[c][:expect]))
            self.assertTrue(not any(zf[c][expect:]))
        # common valid interval: starts at max(I) + K - 1, ends at last output
        first = max(i_int) + klen - 1
        total = length + min(i_int)
        self.assertEqual(aligner.common_valid_interval(), (first, total - 1))
        self.assertEqual(len(cols[0]), total)
        self.assertFalse(any(any(row) for row in ct))  # no missing data here


class SmoothChangeTest(unittest.TestCase):
    def test_delay_trajectory_is_continuous_and_bounded(self):
        length = 700
        inputs = [fixture.make_multichannel_input(length)[0]]
        aligner = MultiChannelAligner(1, [12.0])
        aligner.schedule_change(0, 200, 20.0, 64)   # slope +0.125
        aligner.schedule_change(0, 400, 10.0, 50)   # slope -0.2
        max_slope = max(8.0 / 64, 10.0 / 50)
        prev = aligner.delay_at(0, 0)
        for n in range(1, length):
            cur = aligner.delay_at(0, n)
            step = cur - prev
            # continuous, never steeper than declared -> read position
            # rho(n) = n - d(n) advances by 1 - step in (0.8, 1.125]:
            # it never repeats (<= 0) and never skips undeclared samples.
            self.assertLessEqual(abs(step), max_slope + 1e-12)
            self.assertGreater(1.0 - step, 0.0)
            prev = cur
        pos = 0
        while pos < length:  # stream still works through the same aligner
            stop = min(pos + 11, length)
            aligner.process([inputs[0][pos:stop]])
            pos = stop
        aligner.finish()

    def test_delay_change_moves_impulse_to_declared_positions(self):
        pos_a, pos_b, length = 60, 400, 700
        x = [0.0] * length
        x[pos_a] = 1.0
        x[pos_b] = 1.0
        schedule = [(0, 200, 20.0, 64)]
        cols, _, _, _ = run_stream([x], 9, base_delays=[12.0],
                                   schedule=schedule)
        y = cols[0]
        # impulse A left the stream before the ramp: delay 12.0
        seg_a = y[:200]
        self.assertAlmostEqual(centroid(seg_a), pos_a + 12.0,
                               delta=fixture.CENTROID_TOLERANCE)
        # impulse B arrived after the ramp (ends at 264): delay 20.0
        seg_b = y[300:]
        self.assertAlmostEqual(centroid(seg_b) + 300, pos_b + 20.0,
                               delta=fixture.CENTROID_TOLERANCE)
        # energy preserved: each impulse appears exactly once
        energy = sum(v * v for v in y)
        h = fixture.fractional_kernel(0.5)
        self.assertAlmostEqual(energy, 2.0 * sum(c * c for c in h),
                               delta=0.02)

    def test_unsmooth_change_rejected(self):
        aligner = MultiChannelAligner(1, [12.0])
        with self.assertRaises(ValueError):
            aligner.schedule_change(0, 100, 20.0, 0)     # step change
        with self.assertRaises(ValueError):
            aligner.schedule_change(0, 100, 40.0, 10)    # slope > 1
        with self.assertRaises(ValueError):
            aligner.schedule_change(0, 100, 200.0, 50)   # beyond max delay


class RetractionTest(unittest.TestCase):
    def test_retract_unconfirmed_region(self):
        inputs = fixture.make_multichannel_input()
        # schedule a change far ahead of the frontier, then withdraw it
        cols, zf, ct, aligner = run_stream(
            inputs, 31, schedule=[(1, 500, 20.0, 40)], retract=(1,))
        rs, rz, rc = reference.reference_run(inputs, fixture.DECLARED_DELAYS)
        for c in range(fixture.NUM_CHANNELS):
            for a, b in zip(rs[c], cols[c]):
                self.assertAlmostEqual(a, b, delta=TOL)
        self.assertEqual(aligner.delay_version, 2)  # schedule + retract

    def test_retract_confirmed_region_raises(self):
        inputs = fixture.make_multichannel_input()
        aligner = MultiChannelAligner(fixture.NUM_CHANNELS,
                                      fixture.DECLARED_DELAYS)
        version = aligner.schedule_change(1, 50, 20.0, 40)
        aligner.process([ch[:200] for ch in inputs])   # frontier passes 50
        self.assertGreater(aligner.emit_frontier, 50)
        with self.assertRaises(RuntimeError):
            aligner.retract(version)
        with self.assertRaises(RuntimeError):
            aligner.schedule_change(0, 10, 20.0, 10)   # also in the past


class CheckpointTest(unittest.TestCase):
    def test_checkpoint_roundtrip_bit_identical(self):
        inputs = fixture.make_multichannel_input()
        schedule = [(1, 150, 20.0, 40), (2, 300, 12.5, 30)]
        straight, sz, sc, _ = run_stream(inputs, 29, schedule=schedule)
        restored, rz, rc, aligner = run_stream(inputs, 29, schedule=schedule,
                                               checkpoint_at=200)
        for c in range(fixture.NUM_CHANNELS):
            self.assertEqual(straight[c], restored[c])   # exact
            self.assertEqual(sz[c], rz[c])
            self.assertEqual(sc[c], rc[c])
        self.assertEqual(aligner.delay_version, len(schedule))

    def test_checkpoint_carries_history_mask_and_version(self):
        inputs = fixture.make_multichannel_input()
        missing = [[False] * 600 for _ in range(fixture.NUM_CHANNELS)]
        missing[0][100] = True
        aligner = MultiChannelAligner(fixture.NUM_CHANNELS,
                                      fixture.DECLARED_DELAYS)
        aligner.schedule_change(1, 400, 20.0, 40)
        aligner.process([ch[:150] for ch in inputs],
                        [m[:150] for m in missing])
        state = pickle.loads(aligner.checkpoint())
        self.assertEqual(state["delay_version"], 1)
        self.assertIn("history", state["channels"][0])          # filter history
        self.assertIn("missing_mask", state["channels"][0])     # contamination
        self.assertTrue(any(state["channels"][0]["missing_mask"]))
        # restored aligner reproduces the contamination flags
        payload = aligner.checkpoint()
        twin = MultiChannelAligner(fixture.NUM_CHANNELS,
                                   fixture.DECLARED_DELAYS)
        twin.load_checkpoint(payload)
        b1 = aligner.process([ch[150:300] for ch in inputs],
                             [m[150:300] for m in missing])
        b2 = twin.process([ch[150:300] for ch in inputs],
                          [m[150:300] for m in missing])
        self.assertEqual(b1.samples, b2.samples)
        self.assertEqual(b1.contaminated, b2.contaminated)


class MissingDataTest(unittest.TestCase):
    def test_missing_data_contamination_mask(self):
        length = 400
        inputs = fixture.make_multichannel_input(length)
        missing = [[False] * length for _ in range(fixture.NUM_CHANNELS)]
        for idx in (50, 51, 120):
            missing[0][idx] = True
        missing[2][350] = True
        cols, zf, ct, _ = run_stream(inputs, 19, missing=missing)
        rs, rz, rc = reference.reference_run(inputs, fixture.DECLARED_DELAYS,
                                             missing=missing)
        for c in range(fixture.NUM_CHANNELS):
            self.assertEqual(rc[c], ct[c])
            for a, b in zip(rs[c], cols[c]):
                self.assertAlmostEqual(a, b, delta=TOL)
        # contamination really was flagged around the missing samples
        self.assertTrue(any(ct[0]))
        self.assertTrue(any(ct[2]))
        self.assertFalse(any(ct[1]))


class MemoryBoundTest(unittest.TestCase):
    def test_memory_bound_from_max_delay_and_kernel_length(self):
        group = fixture.KERNEL_GROUP_DELAY
        expect = math.ceil(fixture.MAX_DELAY - group) + fixture.KERNEL_LENGTH
        length = 5000
        inputs = fixture.make_multichannel_input(length)
        aligner = MultiChannelAligner(fixture.NUM_CHANNELS,
                                      fixture.DECLARED_DELAYS)
        self.assertEqual(aligner.history_bound, expect)
        pos = 0
        while pos < length:
            stop = min(pos + 97, length)
            aligner.process([ch[pos:stop] for ch in inputs])
            for got in aligner.history_lengths():
                self.assertLessEqual(got, aligner.history_bound)
            pos = stop
        aligner.finish()
        self.assertLessEqual(aligner.max_history_observed,
                             aligner.history_bound)


class TailFlushTest(unittest.TestCase):
    def test_tail_flush_drains_everything(self):
        inputs = fixture.make_multichannel_input()
        cols, _, _, aligner = run_stream(inputs, 41)
        self.assertTrue(aligner.finished)
        # nothing left pending after the flush
        self.assertEqual(len(aligner.finish().samples[0]), 0)
        # total count matches the reference exactly
        rs, _, _ = reference.reference_run(inputs, fixture.DECLARED_DELAYS)
        self.assertEqual(len(cols[0]), len(rs[0]))
        with self.assertRaises(RuntimeError):
            aligner.process([[0.0]] * fixture.NUM_CHANNELS)

    def test_overlapping_changes_match_reference(self):
        inputs = fixture.make_multichannel_input()
        schedule = [(0, 100, 20.0, 50),    # ramps 100..150
                    (0, 120, 10.0, 40),    # overlaps the first ramp
                    (0, 200, 25.0, 80),
                    (2, 250, 40.0, 60)]
        cols, zf, ct, _ = run_stream(inputs, 7, schedule=schedule)
        changes = [[(100, 20.0, 50), (120, 10.0, 40), (200, 25.0, 80)],
                   [],
                   [(250, 40.0, 60)]]
        rs, rz, rc = reference.reference_run(inputs, fixture.DECLARED_DELAYS,
                                             changes_per_channel=changes)
        for c in range(fixture.NUM_CHANNELS):
            self.assertEqual(len(rs[c]), len(cols[c]))
            for a, b in zip(rs[c], cols[c]):
                self.assertAlmostEqual(a, b, delta=TOL)
            self.assertEqual(rz[c], zf[c])
            self.assertEqual(rc[c], ct[c])


if __name__ == "__main__":
    unittest.main()
