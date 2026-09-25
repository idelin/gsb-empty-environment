import random
import unittest

from fixtures import load_tracks
from plan import EditPlan
from render import (
    LATENCY_SAMPLES,
    LOOKAHEAD_SOURCE_SAMPLES,
    TOLERANCE,
    render_chunked,
    render_direct,
)
from timemap import Kind


def assert_renderings_close(tc, a, b):
    tc.assertEqual(len(a.positions), len(b.positions))
    for pa, pb in zip(a.positions, b.positions):
        tc.assertAlmostEqual(pa, pb, delta=TOLERANCE)
    tc.assertEqual(a.kinds, b.kinds)
    tc.assertEqual(a.mask, b.mask)  # masks must match exactly
    for ta, tb in zip(a.values, b.values):
        for va, vb in zip(ta, tb):
            tc.assertAlmostEqual(va, vb, delta=TOLERANCE)


class TestChunkedVsDirect(unittest.TestCase):
    def setUp(self):
        self.tracks, self.sr = load_tracks()

    def test_fixture_plan_all_segment_kinds(self):
        # forward x1 -> freeze -> reverse -> forward x2 -> repeated region
        plan = EditPlan.from_control_points(
            [
                (0.000, 0.0),
                (0.020, 160.0),   # forward, slope 1 in samples/sr units
                (0.030, 160.0),   # freeze at source position 160
                (0.045, 40.0),    # reverse down to 40
                (0.055, 120.0),   # forward again (replays 40..120 partially)
                (0.065, 40.0),    # reverse, repeats the 40..120 region
                (0.075, 120.0),   # forward, second replay of the region
            ]
        )
        num_output = int(0.075 * self.sr)
        direct = render_direct(plan, self.tracks, self.sr, num_output)
        for chunk in (1, 7, 256, 4096):
            chunked = render_chunked(plan, self.tracks, self.sr, num_output, chunk)
            assert_renderings_close(self, direct, chunked)

    def test_randomized_plans_and_chunks(self):
        rng = random.Random(1234)
        for trial in range(25):
            points = [(0.0, rng.uniform(0, 50))]
            out_t, src = points[0]
            for _ in range(rng.randint(2, 6)):
                out_t += rng.uniform(0.5, 3.0) / self.sr * 100  # 50..300 samples
                choice = rng.random()
                if choice < 0.25:
                    src = src  # freeze
                elif choice < 0.5:
                    src = max(0.0, src - rng.uniform(5, 200))  # reverse
                else:
                    src = min(399.0, src + rng.uniform(5, 200))  # forward
                points.append((out_t, src))
            plan = EditPlan.from_control_points(points)
            num_output = rng.randint(1, int(plan.out_end * self.sr))
            direct = render_direct(plan, self.tracks, self.sr, num_output)
            for _ in range(3):
                chunk = rng.randint(1, 500)
                chunked = render_chunked(
                    plan, self.tracks, self.sr, num_output, chunk
                )
                assert_renderings_close(self, direct, chunked)


class TestRenderSemantics(unittest.TestCase):
    def setUp(self):
        self.tracks, self.sr = load_tracks()

    def test_speed_2_output_length(self):
        # Slope 2: covering 200 source positions takes 100 output samples.
        plan = EditPlan.from_control_points([(0.0, 0.0), (100.0 / self.sr, 200.0)])
        res = render_direct(plan, self.tracks, self.sr, 100)
        self.assertAlmostEqual(res.positions[-1], 198.0, delta=TOLERANCE)
        self.assertEqual(len(res.positions), 100)

    def test_reverse_turning_point_single_replay(self):
        # Turning point at source position 100, output sample 100.
        plan = EditPlan.from_control_points(
            [(0.0, 0.0), (100.0 / self.sr, 100.0), (200.0 / self.sr, 0.0)]
        )
        res = render_chunked(plan, self.tracks, self.sr, 201, chunk_size=32)
        hits = sum(1 for p in res.positions if round(p) == 100)
        self.assertEqual(hits, 1)

    def test_freeze_repeats_position_and_is_marked(self):
        plan = EditPlan.from_control_points(
            [(0.0, 0.0), (0.010, 80.0), (0.020, 80.0), (0.030, 160.0)]
        )
        res = render_direct(plan, self.tracks, self.sr, int(0.030 * self.sr))
        freeze_idx = [
            i for i, k in enumerate(res.kinds) if k is Kind.FREEZE
        ]
        self.assertTrue(freeze_idx)
        for i in freeze_idx:
            self.assertAlmostEqual(res.positions[i], 80.0, delta=TOLERANCE)
        # Non-freeze samples are never mislabelled as freeze.
        for i, k in enumerate(res.kinds):
            if i not in set(freeze_idx):
                self.assertIsNot(k, Kind.FREEZE)

    def test_missing_data_mask_multitrack(self):
        # Track 0 has a hole at source indices 50..59 (see fixtures).
        plan = EditPlan.from_control_points([(0.0, 0.0), (0.050, 400.0)])
        res = render_direct(plan, self.tracks, self.sr, int(0.050 * self.sr))
        # Output samples whose interpolation taps touch the hole are masked.
        self.assertFalse(res.mask[0][50])
        self.assertFalse(res.mask[0][55])
        self.assertTrue(res.mask[0][40])
        # Track 1 is intact at those positions: same mapping, valid data.
        self.assertTrue(res.mask[1][50])
        # All tracks share the same positions/kinds (synchrony by construction).
        self.assertEqual(len(res.values), len(self.tracks))

    def test_filter_tail_beyond_source_end(self):
        # Map runs past the last source index (399): the interpolation
        # kernel's tail reaches beyond available data and must be flagged.
        plan = EditPlan.from_control_points([(0.0, 390.0), (0.002, 406.0)])
        res = render_direct(plan, self.tracks, self.sr, int(0.002 * self.sr))
        last_valid = max(
            i for i, p in enumerate(res.positions) if p <= 399.0
        )
        self.assertTrue(res.mask[0][last_valid])
        for i in range(last_valid + 1, len(res.positions)):
            self.assertFalse(res.mask[0][i])  # tail flagged invalid
            # Value is clamped to the final source sample.
            self.assertAlmostEqual(
                res.values[0][i], self.tracks[0][-1], delta=TOLERANCE
            )

    def test_latency_and_tolerance_declared(self):
        self.assertEqual(LATENCY_SAMPLES, 0)
        self.assertEqual(LOOKAHEAD_SOURCE_SAMPLES, 1)
        self.assertGreater(TOLERANCE, 0.0)


if __name__ == "__main__":
    unittest.main()
