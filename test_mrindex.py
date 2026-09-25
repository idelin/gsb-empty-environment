"""Unittest suite for the multi-resolution index (stdlib only)."""

import math
import random
import unittest
from fractions import Fraction

from fixtures import RATES, impulse, noise, sine_wave, with_dropouts
from mrindex import MultiResIndex, TOLERANCE


def brute(samples, s, e):
    """Per-sample reference over [s, e)."""
    vals = samples[s:e]
    valid = [float(v) for v in vals if not (v is None or math.isnan(v))]
    return {
        "count": len(valid),
        "missing": len(vals) - len(valid),
        "min": min(valid) if valid else None,
        "max": max(valid) if valid else None,
        "energy": sum(v * v for v in valid),
    }


def same_samples(a, b):
    if len(a) != len(b):
        return False
    for x, y in zip(a, b):
        x_nan = x is None or (isinstance(x, float) and math.isnan(x))
        y_nan = y is None or (isinstance(y, float) and math.isnan(y))
        if x_nan or y_nan:
            if not (x_nan and y_nan):
                return False
        elif x != y:
            return False
    return True


def check_avl(t):
    """Validate balance + aggregate consistency; returns height."""
    if t is None:
        return 0
    if t.height == 1:
        assert t.height == 1
        return 1
    lh = check_avl(t.left)
    rh = check_avl(t.right)
    assert abs(lh - rh) <= 1, "AVL balance violated"
    assert t.height == max(lh, rh) + 1
    m = t.left.agg.merge(t.right.agg)
    assert m.count == t.agg.count and m.missing == t.agg.missing
    assert math.isclose(m.sumsq, t.agg.sumsq, rel_tol=TOLERANCE)
    return t.height


class TestQueries(unittest.TestCase):
    def setUp(self):
        rng = random.Random(42)
        self.samples = [rng.uniform(-1, 1) for _ in range(2000)]
        for p in (0, 5, 999, 1999):
            self.samples[p] = float("nan")
        self.idx = MultiResIndex(self.samples, sample_rate=RATES["cd"])

    def test_random_ranges_match_brute_force(self):
        rng = random.Random(1)
        n = len(self.samples)
        for _ in range(300):
            s = rng.randrange(0, n)
            e = rng.randrange(s, n + 1)
            got = self.idx.summary(s, e)
            ref = brute(self.samples, s, e)
            self.assertEqual(got.count, ref["count"])
            self.assertEqual(got.missing, ref["missing"])
            self.assertEqual(got.minimum, ref["min"])
            self.assertEqual(got.maximum, ref["max"])
            self.assertTrue(math.isclose(got.energy, ref["energy"],
                                         rel_tol=TOLERANCE))

    def test_empty_interval_has_no_fake_peak(self):
        s = self.idx.summary(100, 100)
        self.assertEqual(s.count, 0)
        self.assertEqual(s.missing, 0)
        self.assertIsNone(s.minimum)
        self.assertIsNone(s.maximum)
        self.assertEqual(s.energy, 0.0)
        self.assertIsNone(s.first_index)
        value, ranges = self.idx.peaks(100, 100)
        self.assertIsNone(value)
        self.assertEqual(ranges, [])

    def test_summary_boundary_info(self):
        s = self.idx.summary(7, 900)
        self.assertEqual((s.first_index, s.last_index), (7, 899))
        self.assertFalse(s.covers_full_series)
        full = self.idx.summary()
        self.assertTrue(full.covers_full_series)
        self.assertEqual(full.missing, 4)

    def test_energy_uses_true_coverage(self):
        samples = with_dropouts([1.0, 2.0, 3.0, 4.0, 5.0], [1, 3])
        idx = MultiResIndex(samples)
        s = idx.summary(0, 5)
        self.assertEqual(s.count, 3)
        self.assertEqual(s.missing, 2)
        self.assertTrue(math.isclose(s.energy, 1 + 9 + 25, rel_tol=TOLERANCE))
        # partial edge: only samples 2..4 covered
        s = idx.summary(2, 4)
        self.assertEqual(s.count, 1)
        self.assertEqual(s.missing, 1)
        self.assertTrue(math.isclose(s.energy, 9.0, rel_tol=TOLERANCE))

    def test_nonaligned_query_excludes_out_of_range_peaks(self):
        sp = [0.0] * 33
        sp[9] = 100.0
        sp[20] = 100.0
        sp[15] = 3.0
        idx = MultiResIndex(sp)
        s = idx.summary(10, 20)
        self.assertEqual(s.maximum, 3.0)
        value, ranges = idx.peaks(10, 20)
        self.assertEqual(value, 3.0)
        self.assertEqual(ranges, [(15, 16)])
        for a, b in ranges:
            self.assertGreaterEqual(a, 10)
            self.assertLessEqual(b, 20)

    def test_tied_peaks_return_all_source_ranges(self):
        idx = MultiResIndex([0.0, 5.0, 1.0, 5.0, 5.0, -2.0, 5.0, 0.0])
        value, ranges = idx.peaks()
        self.assertEqual(value, 5.0)
        self.assertEqual(ranges, [(1, 2), (3, 5), (6, 7)])
        value, ranges = idx.peaks(kind="min")
        self.assertEqual(value, -2.0)
        self.assertEqual(ranges, [(5, 6)])

    def test_single_sample_spike_visible_at_all_levels(self):
        n, at = 257, 123
        idx = MultiResIndex(impulse(n, at, height=50.0))
        width = 1
        while width <= 2 * n:
            buckets = idx.buckets(0, n, width)
            self.assertEqual(max(b.maximum for b in buckets), 50.0)
            width *= 2
        self.assertLess(idx.summary(0, at).maximum, 50.0)
        self.assertLess(idx.summary(at + 1, n).maximum, 50.0)
        self.assertEqual(idx.summary(at, at + 1).maximum, 50.0)

    def test_partial_buckets_not_treated_as_full(self):
        samples = noise(24, seed=3)
        idx = MultiResIndex(samples)
        buckets = idx.buckets(3, 19, 8)
        self.assertEqual(len(buckets), 3)
        first, mid, last = buckets
        self.assertTrue(first.partial)
        self.assertEqual(first.coverage, 5)
        self.assertFalse(mid.partial)
        self.assertEqual(mid.coverage, 8)
        self.assertTrue(last.partial)
        self.assertEqual(last.coverage, 3)
        # bucket stats must reflect only the covered samples
        self.assertEqual(first.maximum, max(samples[3:8]))
        self.assertEqual(last.maximum, max(samples[16:19]))
        self.assertTrue(math.isclose(
            first.energy, sum(v * v for v in samples[3:8]), rel_tol=TOLERANCE))
        self.assertTrue(math.isclose(
            last.energy, sum(v * v for v in samples[16:19]), rel_tol=TOLERANCE))


class TestEdits(unittest.TestCase):
    def test_versioned_replace_and_undo(self):
        base = noise(200, seed=9)
        idx = MultiResIndex(base)
        self.assertEqual(idx.version, 0)
        idx.replace(50, 60, [7.0] * 10)
        self.assertEqual(idx.version, 1)
        idx.insert(10, [1.0, 2.0])
        self.assertEqual(idx.version, 2)
        idx.cut(0, 5)
        self.assertEqual(idx.version, 3)
        self.assertEqual(idx.size, 200 + 2 - 5)
        self.assertTrue(idx.undo())
        self.assertTrue(idx.undo())
        self.assertTrue(idx.undo())
        self.assertEqual(idx.version, 0)
        self.assertTrue(same_samples(idx.to_list(), base))
        self.assertFalse(idx.undo())

    def test_random_edits_match_per_sample_reference(self):
        rng = random.Random(2024)
        ref = noise(500, seed=11)
        ref = with_dropouts(ref, [3, 100, 499])
        idx = MultiResIndex(ref)
        history = [list(ref)]
        for step in range(300):
            op = rng.random()
            n = len(ref)
            if op < 0.35:  # insert
                pos = rng.randrange(0, n + 1)
                chunk = [rng.uniform(-1, 1) for _ in range(rng.randrange(0, 12))]
                idx.insert(pos, chunk)
                ref[pos:pos] = chunk
                history.append(list(ref))
            elif op < 0.6:  # cut
                if n == 0:
                    continue
                s = rng.randrange(0, n)
                e = rng.randrange(s + 1, min(n, s + 12) + 1)
                idx.cut(s, e)
                del ref[s:e]
                history.append(list(ref))
            elif op < 0.85:  # replace
                if n == 0:
                    continue
                s = rng.randrange(0, n)
                e = rng.randrange(s, min(n, s + 12) + 1)
                chunk = [rng.uniform(-1, 1) for _ in range(rng.randrange(0, 12))]
                idx.replace(s, e, chunk)
                ref[s:e] = chunk
                history.append(list(ref))
            else:  # undo
                if len(history) > 1 and idx.undo():
                    history.pop()
                    ref = list(history[-1])
            self.assertTrue(same_samples(idx.to_list(), ref),
                            f"sample mismatch at step {step}")
            if step % 10 == 0 and ref:
                for _ in range(5):
                    s = rng.randrange(0, len(ref))
                    e = rng.randrange(s, len(ref) + 1)
                    got = idx.summary(s, e)
                    exp = brute(ref, s, e)
                    self.assertEqual(got.count, exp["count"])
                    self.assertEqual(got.missing, exp["missing"])
                    self.assertEqual(got.minimum, exp["min"])
                    self.assertEqual(got.maximum, exp["max"])
                    self.assertTrue(math.isclose(got.energy, exp["energy"],
                                                 rel_tol=TOLERANCE))
            if step % 25 == 0:
                check_avl(idx.root)

    def test_local_maintenance_copy_and_visit_counts(self):
        n = 65536
        idx = MultiResIndex(noise(n, seed=5))
        total_nodes = idx.node_count()
        idx.replace(1000, 1010, [0.5] * 10)
        self.assertLess(idx.last_op.copies, 500)
        self.assertLess(idx.last_op.copies, total_nodes // 50)
        idx.summary(2000, 2100)
        self.assertLess(idx.last_op.visits, 1000)
        idx.summary(0, n)  # full range touches only the root
        self.assertEqual(idx.last_op.visits, 1)


class TestResample(unittest.TestCase):
    def test_identity_and_integer_factors_are_exact(self):
        src = noise(1000, seed=17)
        idx = MultiResIndex(src, sample_rate=RATES["cd"])
        self.assertEqual(idx.resample(1, 1), src)
        down = idx.resample(1, 2)
        self.assertEqual(len(down), 500)
        self.assertEqual(down, src[::2])

    def test_rational_rate_change(self):
        src = sine_wave(1000, rate=RATES["cd"])
        idx = MultiResIndex(src, sample_rate=RATES["cd"])
        out = idx.resample(160, 147)  # 44100 -> 48000
        self.assertEqual(len(out), (1000 * 160) // 147)
        # independent reference with exact rational positions
        for i in (0, 1, 500, len(out) - 1):
            pos = Fraction(i * 147, 160)
            j = pos.numerator // pos.denominator
            if j >= len(src) - 1:
                exp = src[-1]
            else:
                exp = src[j] + (src[j + 1] - src[j]) * float(pos - j)
            self.assertTrue(math.isclose(out[i], exp, abs_tol=TOLERANCE))
        rs = idx.change_rate(160, 147)
        self.assertEqual(rs.sample_rate, Fraction(48000))
        self.assertEqual(rs.size, len(out))

    def test_resample_propagates_missing(self):
        idx = MultiResIndex(with_dropouts([1.0, 2.0, 3.0, 4.0], [2]))
        out = idx.resample(2, 1)  # upsample: neighbours of NaN become NaN
        self.assertTrue(math.isnan(out[4]))
        self.assertTrue(math.isnan(out[5]))


class TestLatencyDeclaration(unittest.TestCase):
    def test_declared_tolerance_is_consistent(self):
        self.assertEqual(TOLERANCE, 1e-9)


if __name__ == "__main__":
    unittest.main()
