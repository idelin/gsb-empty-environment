"""unittest suite for the multi-resolution min/max/energy index.

The index result of every operation is cross-checked against a plain
sample-by-sample reference list.  Random splice / insert / withdraw / delete
sequences exercise incremental repair; version rollback and rational rate
changes are covered as well.
"""

from __future__ import annotations

import math
import random
import unittest
from fractions import Fraction

from multires_index import MultiResolutionIndex
from multires_index.fixtures import (
    ALT_RATE,
    DEFAULT_RATE,
    tied_waveform,
    waveform,
)

ENERGY_REL_TOL = 1e-12
ENERGY_ABS_TOL = 1e-9


def present(values):
    return [v for v in values if not (isinstance(v, float) and math.isnan(v))]


def ref_summary(values, lo, hi):
    seg = values[lo:hi]
    live = present(seg)
    return {
        "samples": hi - lo,
        "present": len(live),
        "missing": len(seg) - len(live),
        "min": min(live) if live else None,
        "max": max(live) if live else None,
        "energy": sum(v * v for v in live),
    }


def assert_summary_match(testcase, summary, ref):
    testcase.assertEqual(summary.samples, ref["samples"])
    testcase.assertEqual(summary.present, ref["present"])
    testcase.assertEqual(summary.missing, ref["missing"])
    testcase.assertEqual(summary.minimum, ref["min"])
    testcase.assertEqual(summary.maximum, ref["max"])
    testcase.assertAlmostEqual(
        summary.energy, ref["energy"],
        delta=max(ENERGY_ABS_TOL, abs(ref["energy"]) * ENERGY_REL_TOL),
    )
    testcase.assertEqual(summary.complete, ref["missing"] == 0)
    if ref["present"]:
        testcase.assertAlmostEqual(
            summary.mean_energy, ref["energy"] / ref["present"],
            delta=ENERGY_ABS_TOL,
        )


def build_index(values, rate=DEFAULT_RATE):
    idx = MultiResolutionIndex(rate)
    # Append in several chunks so block packing/merging is exercised.
    step = 37
    for i in range(0, len(values), step):
        idx.append(values[i:i + step])
    return idx


class BuildAndQueryTests(unittest.TestCase):
    def setUp(self):
        self.data = waveform()
        self.idx = build_index(self.data)

    def test_full_range_matches_reference(self):
        n = len(self.data)
        assert_summary_match(self, self.idx.query(0, n),
                             ref_summary(self.data, 0, n))

    def test_aligned_and_unaligned_ranges(self):
        for lo, hi in [(0, 1), (0, 64), (1, 65), (33, 100),
                       (128, 257), (64, 192), (200, 257), (130, 131)]:
            assert_summary_match(self, self.idx.query(lo, hi),
                                 ref_summary(self.data, lo, hi))

    def test_edges_are_exact_no_outside_peak(self):
        # Range excluding the spike must not report it.
        spike = len(self.data) // 3
        left = self.idx.query(0, spike)
        right = self.idx.query(spike + 1, len(self.data))
        self.assertLess(left.maximum, 9.0)
        self.assertLess(right.maximum, 9.0)
        only = self.idx.query(spike, spike + 1)
        self.assertEqual(only.maximum, 9.0)
        self.assertEqual(only.minimum, 9.0)

    def test_single_sample_spike_visible_at_every_level(self):
        for level in range(9):
            nodes = self.idx.level_nodes(level)
            if not nodes:
                continue
            highs = [row[3] for row in nodes]
            self.assertIn(9.0, highs, f"spike missing at level {level}")
        # Bucket containing the spike: mean energy must not mask peak.
        s = self.idx.query(0, len(self.data))
        self.assertEqual(s.maximum, 9.0)

    def test_empty_range_has_no_phantom_peak(self):
        pk = self.idx.peak(10, 10)
        self.assertIsNone(pk.peak)
        self.assertEqual(pk.ranges, ())
        self.assertEqual(pk.sample_ranges, ())
        s = self.idx.query(10, 10)
        self.assertEqual(s.present, 0)
        self.assertIsNone(s.minimum)
        self.assertIsNone(s.maximum)
        self.assertEqual(s.energy, 0.0)

    def test_missing_samples_reported(self):
        spike = len(self.data) // 3
        s = self.idx.query(spike + 4, spike + 7)  # includes gap at +5
        self.assertEqual(s.missing, 1)
        self.assertEqual(s.present, 2)
        self.assertFalse(s.complete)
        self.assertEqual(s.partial_buckets, 1)
        self.assertEqual(sum(b.missing for b in s.buckets), 1)


class PeakTieTests(unittest.TestCase):
    def test_all_tied_sources_returned(self):
        data = tied_waveform()
        idx = build_index(data)
        pk = idx.peak(0, len(data))
        self.assertEqual(pk.peak, 5.0)
        self.assertEqual(pk.sample_indices, (3, 17, 40, 41))
        self.assertEqual(pk.sample_ranges, ((3, 4), (17, 18), (40, 42)))
        # Provenance ranges cover exactly the tied samples, no extras.
        covered = [i for (a, b) in pk.ranges for i in range(a, b)]
        self.assertEqual(sorted(covered), [3, 17, 40, 41])

    def test_non_aligned_range_excludes_outside_peak(self):
        data = tied_waveform()
        idx = build_index(data)
        pk = idx.peak(4, 17)  # excludes ties at 3, 17, 40 and 41
        self.assertLess(pk.peak, 5.0)
        pk2 = idx.peak(18, 41)  # includes 40 but not 41
        self.assertEqual(pk2.peak, 5.0)
        self.assertEqual(pk2.sample_indices, (40,))


class BucketAccountingTests(unittest.TestCase):
    def test_partial_edge_buckets_are_not_full(self):
        data = waveform(128)
        idx = build_index(data)
        s = idx.query(3, 125)
        self.assertGreater(s.partial_buckets, 0)
        # Edge buckets must touch the boundaries.
        edge = [b for b in s.buckets if b.touches_boundary]
        self.assertTrue(edge)
        self.assertTrue(all(
            b.present + b.missing == (b.end - b.start) for b in s.buckets
        ))
        self.assertEqual(s.full_buckets + s.partial_buckets, len(s.buckets))

    def test_aligned_range_all_full(self):
        data = [1.0] * 128
        idx = build_index(data)
        s = idx.query(0, 128)
        self.assertEqual(s.partial_buckets, 0)
        self.assertGreaterEqual(s.full_buckets, 1)
        self.assertTrue(s.complete)

    def test_missing_bucket_never_counted_full(self):
        data = [1.0] * 64
        data[30] = math.nan
        idx = build_index(data)
        s = idx.query(0, 64)
        self.assertGreaterEqual(s.partial_buckets, 1)
        self.assertEqual(s.full_buckets + s.partial_buckets, len(s.buckets))
        self.assertEqual(s.present, 63)
        self.assertEqual(s.missing, 1)


class RandomEditReferenceTests(unittest.TestCase):
    def test_random_splice_insert_withdraw_delete(self):
        rng = random.Random(99)
        ref = waveform(120, seed=7)
        idx = MultiResolutionIndex(DEFAULT_RATE)
        idx.append(ref)

        def check_all():
            n = len(ref)
            for _ in range(40):
                lo = rng.randrange(0, n + 1)
                hi = rng.randrange(lo, n + 1)
                s = idx.query(lo, hi)
                assert_summary_match(self, s, ref_summary(ref, lo, hi))
                pk = idx.peak(lo, hi)
                live = present(ref[lo:hi])
                if live:
                    top = max(live)
                    self.assertEqual(pk.peak, top)
                    expected = [lo + i for i, v in enumerate(ref[lo:hi])
                                if not (isinstance(v, float) and math.isnan(v))
                                and v == top]
                    self.assertEqual(pk.sample_indices, tuple(expected))
                else:
                    self.assertIsNone(pk.peak)
                    self.assertEqual(pk.sample_indices, ())

        check_all()
        for step in range(300):
            kind = rng.choice(("replace", "insert", "delete", "withdraw"))
            n = len(ref)
            lo = rng.randrange(0, n + 1)
            hi = rng.randrange(lo, n + 1)
            if kind == "replace":
                k = rng.randrange(0, 6)
                vals = [rng.uniform(-3, 3) for _ in range(k)]
                ref[lo:hi] = vals
                idx.replace_range(lo, hi, vals)
            elif kind == "insert":
                vals = [rng.uniform(-3, 3) for _ in range(rng.randrange(1, 5))]
                ref[lo:lo] = vals
                idx.insert(lo, vals)
            elif kind == "delete":
                del ref[lo:hi]
                idx.delete(lo, hi)
            else:
                if hi > lo:
                    for i in range(lo, hi):
                        ref[i] = math.nan
                    idx.withdraw(lo, hi)
            self.assertEqual(idx.length, len(ref))
            if step % 25 == 0:
                check_all()
        check_all()

    def test_local_maintenance_footprint(self):
        # A tiny edit deep in a long timeline must copy only local nodes.
        data = [float(i % 7) for i in range(4096)]
        idx = MultiResolutionIndex(DEFAULT_RATE)
        idx.append(data)
        vid = idx.replace_range(2000, 2001, [42.0])
        v = idx.version(vid)
        self.assertLessEqual(v.copied, 4 * math.ceil(math.log2(4096)) + 8)
        self.assertGreater(v.reused, 0)
        self.assertLess(v.visits, 200)
        # Query visits stay logarithmic.
        s = idx.query(100, 4000)
        self.assertLess(s.visits, 4 * math.ceil(math.log2(4096)) + 4)


class VersioningTests(unittest.TestCase):
    def test_interval_replacement_is_versioned_and_persistent(self):
        data = waveform(64)
        idx = build_index(data)
        v0 = idx.head
        vid = idx.replace_range(10, 14, [8.0, 8.0, 8.0, 8.0])
        self.assertEqual(idx.query(10, 14).maximum, 8.0)
        # Old version unchanged.
        self.assertEqual(idx.version(v0).length, 64)
        old = idx.query(10, 14, version_id=v0)
        self.assertNotEqual(old.maximum, 8.0)
        # Rollback republishes old state.
        rb = idx.rollback(v0)
        self.assertNotEqual(idx.query(10, 14).maximum, 8.0)
        self.assertEqual(idx.version(rb).parent_id, vid)

    def test_insert_and_delete_preserve_other_samples(self):
        idx = MultiResolutionIndex(DEFAULT_RATE)
        idx.append([1.0, 2.0, 3.0])
        idx.insert(1, [9.0, 9.0])
        self.assertEqual(idx.length, 5)
        s = idx.query(0, 5)
        self.assertEqual(s.maximum, 9.0)
        idx.delete(1, 3)
        s2 = idx.query(0, 3)
        self.assertAlmostEqual(s2.energy, 1 + 4 + 9)

    def test_withdraw_then_restore(self):
        idx = MultiResolutionIndex(DEFAULT_RATE)
        idx.append([1.0, 2.0, 3.0, 4.0])
        idx.withdraw(1, 3)
        s = idx.query(0, 4)
        self.assertEqual(s.present, 2)
        self.assertEqual(s.missing, 2)
        self.assertEqual(s.maximum, 4.0)
        idx.restore(1, 3, [5.0, 5.0])
        s2 = idx.query(0, 4)
        self.assertEqual(s2.present, 4)
        self.assertEqual(s2.maximum, 5.0)


class RateTests(unittest.TestCase):
    def test_rational_rate_change_is_exact_and_cheap(self):
        idx = MultiResolutionIndex(Fraction(3))
        idx.append([1.0] * 10)
        self.assertEqual(idx.time_at_index(3), Fraction(1, 1))
        vid = idx.change_rate(Fraction(1000, 1001))
        self.assertEqual(idx.rate, Fraction(1000, 1001))
        # No data nodes are copied for a pure rate change.
        self.assertEqual(idx.version(vid).copied, 0)
        # Exact rational mapping, no float drift.
        self.assertEqual(idx.time_at_index(10), Fraction(1001, 100))
        # Samples themselves do not move.
        self.assertEqual(idx.length, 10)

    def test_time_query_maps_through_rate(self):
        idx = MultiResolutionIndex(DEFAULT_RATE)
        idx.append(waveform(257))
        pk_samples = self._find_spike(idx)
        t0 = idx.time_at_index(pk_samples)
        t1 = idx.time_at_index(pk_samples + 1)
        s = idx.query_time(t0, t1)
        self.assertEqual(s.samples, 1)
        self.assertEqual(s.maximum, 9.0)

    @staticmethod
    def _find_spike(idx):
        for i in range(idx.length):
            if idx.query(i, i + 1).maximum == 9.0:
                return i
        self.fail("spike not found")

    def test_rate_rejected_when_non_positive(self):
        idx = MultiResolutionIndex(DEFAULT_RATE)
        with self.assertRaises(ValueError):
            idx.change_rate(0)


class EnergyTests(unittest.TestCase):
    def test_energy_uses_true_coverage(self):
        data = [2.0, math.nan, 3.0, math.nan, 4.0]
        idx = MultiResolutionIndex(DEFAULT_RATE)
        idx.append(data)
        s = idx.query(0, 5)
        self.assertAlmostEqual(s.energy, 4 + 9 + 16)
        self.assertEqual(s.present, 3)
        self.assertAlmostEqual(s.mean_energy, (4 + 9 + 16) / 3)
        self.assertNotAlmostEqual(s.mean_energy, (4 + 9 + 16) / 5)

    def test_energy_random_ranges(self):
        data = waveform(200, seed=55)
        idx = build_index(data)
        rng = random.Random(8)
        for _ in range(60):
            lo = rng.randrange(0, 201)
            hi = rng.randrange(lo, 201)
            assert_summary_match(self, idx.query(lo, hi),
                                 ref_summary(data, lo, hi))


if __name__ == "__main__":
    unittest.main(verbosity=2)
