"""unittest suite; cross-checks the index against an independent
per-sample brute-force reference on small data, including retractions."""

from __future__ import annotations

import random
import unittest
from fractions import Fraction

from timeline.core import (
    TOLERANCE_SECONDS,
    Calibration,
    SampleBlock,
    Timeline,
    build_index,
)
from timeline.fixtures import make_blocks


# ---------------------------------------------------------------------------
# Independent per-sample reference (deliberately naive: O(samples * blocks)).
# ---------------------------------------------------------------------------

def reference_effective_samples(blocks, calibration):
    """Set of (block_id, sample_index) whose instant is won by its own block."""
    spans = {}
    for b in blocks:
        t0 = b.base_time + calibration.offset_for(b.block_id)
        spans[b.block_id] = (t0, t0 + Fraction(len(b.samples), b.sample_rate))
    effective = set()
    for b in blocks:
        t0 = spans[b.block_id][0]
        for i in range(len(b.samples)):
            t = t0 + Fraction(i, b.sample_rate)
            winner_key = (b.version, b.block_id)
            for c in blocks:
                c0, c1 = spans[c.block_id]
                if c0 <= t < c1 and (c.version, c.block_id) > winner_key:
                    winner_key = (c.version, c.block_id)
            if winner_key == (b.version, b.block_id):
                effective.add((b.block_id, i))
    return effective


def index_effective_samples(index):
    return {
        (s.block_id, i)
        for s in index.slices
        for i in range(s.sample_start, s.sample_end)
    }


def assert_tiling(testcase, index):
    """Slices and holes must tile [min_start, max_end) without overlap."""
    intervals = sorted(
        [(s.t_start, s.t_end) for s in index.slices]
        + [(h.t_start, h.t_end) for h in index.holes]
    )
    for (s0, e0), (s1, e1) in zip(intervals, intervals[1:]):
        testcase.assertLessEqual(e0, s1, "intervals must not overlap")
        testcase.assertEqual(e0, s1, "slices+holes must tile contiguously")


class FixtureTimelineTest(unittest.TestCase):
    def setUp(self):
        self.blocks = make_blocks()
        self.index = build_index(self.blocks, Calibration())

    def test_rate_switch_uses_new_rate(self):
        # delta runs at 8 Hz starting at 9/2; its sample 1 must be at
        # 9/2 + 1/8, not 9/2 + 1/2 (the previous block's rate).
        t0, t1 = self.index.time_range_of("delta", 1, 2)
        self.assertEqual(t0, Fraction(9, 2) + Fraction(1, 8))
        self.assertEqual(t1, Fraction(9, 2) + Fraction(2, 8))

    def test_overlap_version_priority(self):
        # gamma (v2) covers [3/2, 5/2) and beats alpha (v1) on [3/2, 2).
        winners = {
            (s.block_id, s.t_start, s.t_end)
            for s in self.index.slices
            if s.t_start < 2 and s.t_end > Fraction(3, 2)
        }
        self.assertEqual(winners, {("gamma", Fraction(3, 2), Fraction(2))})
        # alpha keeps only samples with instants < 3/2: i in [0, 6).
        alpha = [s for s in self.index.slices if s.block_id == "alpha"]
        self.assertEqual(len(alpha), 1)
        self.assertEqual((alpha[0].sample_start, alpha[0].sample_end), (0, 6))

    def test_gap_reported(self):
        gaps = [h for h in self.index.holes if h.reason == "gap"]
        self.assertEqual(
            [(h.t_start, h.t_end) for h in gaps],
            [(Fraction(5, 2), Fraction(3))],
        )

    def test_boundary_query_no_duplicate_samples(self):
        # Partition the whole span at exact block boundaries; concatenated
        # reads must equal one big read, with no sample repeated or lost.
        boundaries = [Fraction(0), Fraction(3, 2), Fraction(2), Fraction(5, 2),
                      Fraction(3), Fraction(9, 2), Fraction(11, 2)]
        pieces = [
            self.index.read(a, b) for a, b in zip(boundaries, boundaries[1:])
        ]
        whole = self.index.read(boundaries[0], boundaries[-1])
        piece_samples = [
            (seg.block_id, i)
            for r in pieces
            for seg in r.segments
            for i in range(seg.sample_start, seg.sample_end)
        ]
        whole_samples = [
            (seg.block_id, i)
            for seg in whole.segments
            for i in range(seg.sample_start, seg.sample_end)
        ]
        self.assertEqual(len(piece_samples), len(set(piece_samples)))
        self.assertEqual(sorted(piece_samples), sorted(whole_samples))
        # A read ending exactly at a boundary excludes the next sample.
        left = self.index.read(Fraction(0), Fraction(3, 2))
        self.assertEqual(left.sample_count, 6)  # alpha samples 0..5 only

    def test_bidirectional_roundtrip(self):
        for block_id, i0, i1 in [("alpha", 0, 8), ("beta", 1, 3), ("delta", 2, 5)]:
            t0, t1 = self.index.time_range_of(block_id, i0, i1)
            self.assertEqual(
                self.index.sample_range_of(block_id, t0, t1), (i0, i1)
            )

    def test_read_reports_holes_and_samples(self):
        result = self.index.read(Fraction(2), Fraction(4))
        self.assertEqual(
            [(h.t_start, h.t_end, h.reason) for h in result.holes],
            [(Fraction(5, 2), Fraction(3), "gap")],
        )
        self.assertEqual(
            [seg.block_id for seg in result.segments], ["gamma", "beta"]
        )

    def test_float_times_within_declared_tolerance(self):
        t0, _ = self.index.time_range_of("delta", 1, 2)
        self.assertAlmostEqual(float(t0), 4.625, delta=TOLERANCE_SECONDS)


class TieBreakTest(unittest.TestCase):
    def test_equal_version_tie_break_is_explicit(self):
        a = SampleBlock("aaa", (1.0, 1.0), 1, version=1, base_time=Fraction(0))
        b = SampleBlock("bbb", (2.0, 2.0), 1, version=1, base_time=Fraction(0))
        index = build_index([a, b], Calibration())
        self.assertEqual([s.block_id for s in index.slices], ["bbb"])

    def test_same_time_overlap_higher_version_wins(self):
        low = SampleBlock("low", (1.0,) * 4, 2, version=1, base_time=Fraction(0))
        high = SampleBlock("high", (9.0,) * 4, 2, version=5, base_time=Fraction(0))
        index = build_index([low, high], Calibration())
        self.assertEqual([s.block_id for s in index.slices], ["high"])
        result = index.read(Fraction(0), Fraction(2))
        self.assertEqual(result.segments[0].samples, (9.0,) * 4)


class RoundingEvidenceTest(unittest.TestCase):
    def test_misaligned_boundaries_leave_evidence(self):
        # w: 1 Hz over [0, 10); hi v3 covers [1/4, 1/2); lo v0 covers [1/4, 3/4).
        wide = SampleBlock("w", tuple(range(10)), 1, version=1, base_time=Fraction(0))
        hi = SampleBlock("hi", (100.0,), 4, version=3, base_time=Fraction(1, 4))
        lo = SampleBlock("lo", (50.0,), 2, version=0, base_time=Fraction(1, 4))
        index = build_index([wide, hi, lo], Calibration())

        first = index.slices[0]
        self.assertEqual((first.t_start, first.t_end), (Fraction(0), Fraction(1, 4)))
        self.assertEqual(first.lead, 0)
        self.assertEqual(first.overshoot, Fraction(3, 4))  # sample 1 sits at t=1

        # [1/2, 3/4): winner is w (v1 > v0) but w has no sample instant there.
        rounding = [h for h in index.holes if h.reason == "rounding"]
        self.assertEqual(
            [(h.t_start, h.t_end) for h in rounding],
            [(Fraction(1, 2), Fraction(3, 4))],
        )
        assert_tiling(self, index)


class CalibrationAndCursorTest(unittest.TestCase):
    def test_calibration_shifts_history_and_old_cursor_is_stable(self):
        tl = Timeline()
        for block in make_blocks():
            tl.add_block(block)
        old_index = tl.index()
        old_cursor = tl.cursor()
        before = old_index.time_range_of("alpha", 0, 1)
        old_slice_starts = [s.t_start for s in old_index.slices]

        tl.apply_calibration(Calibration({"alpha": Fraction(1, 4)}))
        new_index = tl.index()

        after = new_index.time_range_of("alpha", 0, 1)
        self.assertEqual(after[0], before[0] + Fraction(1, 4))
        # Old snapshot and cursor are untouched (atomic swap, immutable data).
        self.assertEqual(old_index.time_range_of("alpha", 0, 1), before)
        self.assertEqual([s.t_start for s in old_cursor], old_slice_starts)
        self.assertNotEqual(
            [s.t_start for s in new_index.slices], old_slice_starts
        )

    def test_revision_increments_once_per_update(self):
        tl = Timeline()
        tl.add_block(make_blocks()[0])
        self.assertEqual(tl.revision, 1)
        tl.apply_calibration(Calibration({"alpha": Fraction(1)}))
        self.assertEqual(tl.revision, 2)
        tl.retract_block("alpha")
        self.assertEqual(tl.revision, 3)

    def test_non_monotonic_insertion_order(self):
        tl = Timeline()
        for block in reversed(make_blocks()):  # inserted back-to-front
            tl.add_block(block)
        starts = [s.t_start for s in tl.index().slices]
        self.assertEqual(starts, sorted(starts))
        # Slice lookup around the gap still works.
        self.assertIsNone(tl.index().slice_at(Fraction(11, 4)))
        self.assertEqual(
            tl.index().slice_at(Fraction(3)).block_id, "beta"
        )


class RetractionTest(unittest.TestCase):
    def test_retraction_restores_shadowed_samples(self):
        tl = Timeline()
        blocks = make_blocks()
        for block in blocks:
            tl.add_block(block)
        tl.retract_block("gamma")
        index = tl.index()
        remaining = [b for b in blocks if b.block_id != "gamma"]
        self.assertEqual(
            index_effective_samples(index),
            reference_effective_samples(remaining, Calibration()),
        )
        # alpha's tail (samples 6, 7 at t=3/2, 7/4) is effective again.
        self.assertIn(("alpha", 6), index_effective_samples(index))
        self.assertIn(("alpha", 7), index_effective_samples(index))


class RandomizedCrossCheckTest(unittest.TestCase):
    def _random_blocks(self, rng, count):
        blocks = []
        for k in range(count):
            rate = rng.choice([1, 2, 3, 4, 6, 8])
            n = rng.randint(1, 10)
            base = Fraction(rng.randint(0, 40), rng.choice([1, 2, 4]))
            blocks.append(
                SampleBlock(
                    f"blk{k}",
                    tuple(float(rng.randint(-9, 9)) for _ in range(n)),
                    rate,
                    version=rng.randint(1, 3),
                    base_time=base,
                )
            )
        return blocks

    def _random_calibration(self, rng, blocks):
        offsets = {}
        for b in blocks:
            if rng.random() < 0.4:
                offsets[b.block_id] = Fraction(rng.randint(-8, 8), 8)
        return Calibration(offsets)

    def test_small_data_matches_per_sample_reference(self):
        rng = random.Random(20260926)
        for trial in range(25):
            blocks = self._random_blocks(rng, rng.randint(2, 8))
            cal = self._random_calibration(rng, blocks)
            index = build_index(blocks, cal)
            with self.subTest(trial=trial, phase="build"):
                self.assertEqual(
                    index_effective_samples(index),
                    reference_effective_samples(blocks, cal),
                )
                assert_tiling(self, index)
                starts = [s.t_start for s in index.slices]
                self.assertEqual(starts, sorted(starts))
            # Retraction: withdraw a random subset and re-check everything.
            if blocks:
                doomed = set(rng.sample(blocks, rng.randint(1, len(blocks))))
                survivors = [b for b in blocks if b not in doomed]
                index2 = build_index(survivors, cal)
                with self.subTest(trial=trial, phase="retract"):
                    self.assertEqual(
                        index_effective_samples(index2),
                        reference_effective_samples(survivors, cal),
                    )
                    assert_tiling(self, index2)

    def test_sample_range_roundtrip_on_random_blocks(self):
        rng = random.Random(7)
        for _ in range(20):
            blocks = self._random_blocks(rng, 3)
            index = build_index(blocks, Calibration())
            for b in blocks:
                i0 = rng.randint(0, len(b.samples) - 1)
                i1 = rng.randint(i0 + 1, len(b.samples))
                t0, t1 = index.time_range_of(b.block_id, i0, i1)
                self.assertEqual(index.sample_range_of(b.block_id, t0, t1), (i0, i1))


if __name__ == "__main__":
    unittest.main()
