import random
import unittest
from decimal import Decimal
from fractions import Fraction

from resampler import (Resampler, compute_reference, MODE_STEP, MODE_LINEAR,
                       REASON_GAP, REASON_BREAKPOINT, REASON_NO_BRACKET,
                       REASON_NO_SAMPLE)

D = Decimal


class LinearBasicTest(unittest.TestCase):
    def test_grid_point_between_two_samples(self):
        r = Resampler(dt=1, mode=MODE_LINEAR, max_gap=10)
        r.add_sample(0, 0, sample_id="a")
        r.add_sample(4, 8, sample_id="b")
        pts = r.grid_range(0, 4)
        self.assertEqual([str(p.value) for p in pts],
                         ["0.000000", "2.000000", "4.000000", "6.000000", "8.000000"])
        mid = pts[2]
        self.assertEqual(mid.t, Fraction(2))
        self.assertEqual(mid.sample_ids, ("a", "b"))
        self.assertEqual(mid.mode, MODE_LINEAR)
        self.assertTrue(mid.valid)

    def test_breakpoint_blocks_interpolation(self):
        r = Resampler(dt=1, mode=MODE_LINEAR, max_gap=10)
        r.add_sample(0, 0, sample_id="a")
        r.add_sample(4, 8, sample_id="b")
        r.add_breakpoint(2)
        pts = r.grid_range(0, 4)
        # grid 2 must NOT be 4: the segment is cut by the breakpoint
        for k in (1, 2, 3):
            self.assertFalse(pts[k].valid)
            self.assertEqual(pts[k].reason, REASON_BREAKPOINT)
            self.assertIsNone(pts[k].value)
        self.assertTrue(pts[0].valid)
        self.assertTrue(pts[4].valid)
        # removing the breakpoint restores interpolation
        r.remove_breakpoint(2)
        self.assertEqual(r.point_at(2).value, D("4.000000"))

    def test_breakpoint_at_sample_time_does_not_block(self):
        r = Resampler(dt=1, mode=MODE_LINEAR, max_gap=10)
        r.add_sample(0, 0, sample_id="a")
        r.add_sample(4, 8, sample_id="b")
        r.add_breakpoint(4)  # coincides with a sample: segment not crossed
        self.assertEqual(r.point_at(2).value, D("4.000000"))

    def test_retract_right_endpoint_turns_outputs_unknown(self):
        r = Resampler(dt=1, mode=MODE_LINEAR, max_gap=10)
        r.add_sample(0, 0, sample_id="a")
        r.add_sample(4, 8, sample_id="b")
        r.grid_range(0, 4)
        events = r.retract("b")
        changed = {e.index: e.new for e in events}
        for k in (1, 2, 3, 4):
            self.assertFalse(changed[k].valid)
            self.assertIsNone(changed[k].value)
            self.assertEqual(changed[k].reason, REASON_NO_BRACKET)
        self.assertNotIn(0, changed)  # exact sample at 0 unaffected
        pts = r.grid_range(0, 4)
        self.assertTrue(pts[0].valid)
        self.assertFalse(any(p.valid for p in pts[1:]))

    def test_max_gap_blocks_interpolation(self):
        r = Resampler(dt=1, mode=MODE_LINEAR, max_gap=3)
        r.add_sample(0, 0, sample_id="a")
        r.add_sample(4, 8, sample_id="b")  # gap 4 > 3
        pts = r.grid_range(0, 4)
        self.assertTrue(pts[0].valid)
        self.assertTrue(pts[4].valid)
        for k in (1, 2, 3):
            self.assertFalse(pts[k].valid)
            self.assertEqual(pts[k].reason, REASON_GAP)


class StepModeTest(unittest.TestCase):
    def test_right_continuous(self):
        r = Resampler(dt=1, mode=MODE_STEP, max_gap=100)
        r.add_sample(0, 1, sample_id="a")
        r.add_sample(2, 5, sample_id="b")
        vals = [p.value for p in r.grid_range(0, 4)]
        self.assertEqual(vals, [D("1.000000"), D("1.000000"), D("5.000000"),
                                D("5.000000"), D("5.000000")])
        self.assertEqual(r.point_at(1).sample_ids, ("a",))

    def test_gap_limit(self):
        r = Resampler(dt=1, mode=MODE_STEP, max_gap="1.5")
        r.add_sample(0, 1, sample_id="a")
        self.assertTrue(r.point_at(1).valid)          # 1 <= 1.5
        p = r.point_at(3)
        self.assertFalse(p.valid)                     # 3 > 1.5
        self.assertEqual(p.reason, REASON_GAP)

    def test_breakpoint_blocks_carry(self):
        r = Resampler(dt=1, mode=MODE_STEP, max_gap=100)
        r.add_sample(0, 1, sample_id="a")
        r.add_breakpoint(2)
        self.assertTrue(r.point_at(1).valid)
        p = r.point_at(2)
        self.assertFalse(p.valid)
        self.assertEqual(p.reason, REASON_BREAKPOINT)

    def test_before_first_sample_unknown(self):
        r = Resampler(dt=1, mode=MODE_STEP, max_gap=100)
        r.add_sample(2, 1, sample_id="a")
        p = r.point_at(1)
        self.assertFalse(p.valid)
        self.assertEqual(p.reason, REASON_NO_SAMPLE)


class PrecisionAndGridTest(unittest.TestCase):
    def test_exact_grid_times_with_noninteger_period(self):
        r = Resampler(dt="0.1", mode=MODE_STEP, max_gap=10, t0="-0.5")
        r.add_sample("-0.5", 1, sample_id="a")
        p = r.point_at(8)  # -0.5 + 8*0.1 = 0.3 exactly
        self.assertEqual(p.t, Fraction(3, 10))
        self.assertNotEqual(float(p.t), 0.30000000000000004)

    def test_fixed_rounding_half_up(self):
        r = Resampler(dt=1, mode=MODE_STEP, max_gap=10)
        r.add_sample(0, "0.1234565", sample_id="a")
        self.assertEqual(r.point_at(0).value, D("0.123457"))  # half up
        r2 = Resampler(dt=1, mode=MODE_STEP, max_gap=10)
        r2.add_sample(0, "0.1234564", sample_id="a")
        self.assertEqual(r2.point_at(0).value, D("0.123456"))

    def test_noninteger_sampling_period_linear(self):
        r = Resampler(dt="0.25", mode=MODE_LINEAR, max_gap=10)
        r.add_sample("0.1", 1, sample_id="a")
        r.add_sample("0.6", 2, sample_id="b")
        p = r.point_at(1)  # t = 0.25 -> 1 + (2-1)*(0.15/0.5) = 1.3
        self.assertEqual(p.t, Fraction(1, 4))
        self.assertEqual(p.value, D("1.300000"))

    def test_negative_times(self):
        r = Resampler(dt=1, mode=MODE_LINEAR, max_gap=10)
        r.add_sample(-3, 6, sample_id="a")
        r.add_sample(-1, 2, sample_id="b")
        p = r.point_at(-2)
        self.assertEqual(p.t, Fraction(-2))
        self.assertEqual(p.value, D("4.000000"))
        self.assertEqual(p.sample_ids, ("a", "b"))

    def test_grid_endpoint_exactly_on_sample(self):
        for mode in (MODE_STEP, MODE_LINEAR):
            r = Resampler(dt="0.5", mode=mode, max_gap=10, t0="-1")
            r.add_sample("-0.5", 7, sample_id="a")
            p = r.point_at(1)  # t == -0.5 exactly
            self.assertTrue(p.valid)
            self.assertEqual(p.value, D("7.000000"))
            self.assertEqual(p.sample_ids, ("a",))


class SameTimeTest(unittest.TestCase):
    def test_last_arrival_wins_and_retract_reveals(self):
        r = Resampler(dt=1, mode=MODE_STEP, max_gap=10)
        r.add_sample(1, 10, sample_id="a")
        r.add_sample(1, 20, sample_id="b")  # same time, later arrival
        self.assertEqual(r.point_at(1).value, D("20.000000"))
        self.assertEqual(r.point_at(1).sample_ids, ("b",))
        r.grid_range(0, 2)
        events = r.retract("b")
        changed = {e.index: e.new for e in events}
        self.assertEqual(changed[1].value, D("10.000000"))  # a resurfaces
        self.assertEqual(changed[1].sample_ids, ("a",))
        self.assertEqual(r.point_at(1).value, D("10.000000"))


class RevisionTest(unittest.TestCase):
    def test_late_sample_produces_revision(self):
        r = Resampler(dt=1, mode=MODE_LINEAR, max_gap=10)
        r.add_sample(0, 0, sample_id="a")
        r.add_sample(8, 8, sample_id="b")
        r.grid_range(0, 8)
        v = r.version
        _, events = r.add_sample(4, 40, sample_id="late")
        self.assertTrue(all(e.version == v + 1 for e in events))
        changed = {e.index: e.new for e in events}
        self.assertEqual(changed[2].value, D("20.000000"))  # (0,0)->(4,40)
        self.assertEqual(changed[6].value, D("24.000000"))  # (4,40)->(8,8)
        self.assertEqual(changed[4].sample_ids, ("late",))

    def test_consumer_recovers_full_grid_from_revisions(self):
        r = Resampler(dt=1, mode=MODE_LINEAR, max_gap=10)
        r.add_sample(0, 0, sample_id="a")
        r.add_sample(10, 100, sample_id="b")

        class Consumer:
            def __init__(self):
                self.cells = {}
                self.version = 0

            def sync(self, k0, k1):
                for p in r.grid_range(k0, k1):
                    self.cells[p.index] = p
                self.version = r.version

            def apply_revisions(self):
                for e in r.revisions_since(self.version):
                    self.cells[e.index] = e.new
                    self.version = max(self.version, e.version)

        c = Consumer()
        c.sync(0, 10)
        # mutations: late sample, breakpoint, retraction of breakpoint, retract
        r.add_sample(5, 55, sample_id="late")
        r.add_breakpoint(3)
        r.remove_breakpoint(3)
        r.retract("late")
        c.apply_revisions()
        fresh = {p.index: p for p in r.grid_range(0, 10)}
        self.assertEqual(c.cells, fresh)

    def test_revision_events_carry_old_and_new(self):
        r = Resampler(dt=1, mode=MODE_LINEAR, max_gap=10)
        r.add_sample(0, 0, sample_id="a")
        r.add_sample(4, 8, sample_id="b")
        r.grid_range(0, 4)
        v = r.version
        r.retract("b")
        events = r.revisions_since(v)
        e2 = next(e for e in events if e.index == 2)
        self.assertEqual(e2.old.value, D("4.000000"))
        self.assertIsNone(e2.new.value)


class ChunkedVsWholeTest(unittest.TestCase):
    def test_random_chunked_ingestion_matches_reference(self):
        rng = random.Random(20260921)
        for mode in (MODE_STEP, MODE_LINEAR):
            for trial in range(30):
                n = rng.randint(5, 60)
                times = [Fraction(rng.randint(-40, 40), rng.choice([1, 2, 4]))
                         for _ in range(n)]
                samples = [(t, Decimal(rng.randint(-100, 100)) / 7, f"id{i}")
                           for i, t in enumerate(times)]
                breaks = sorted({Fraction(rng.randint(-40, 40), 2)
                                 for _ in range(rng.randint(0, 4))})
                retract_ids = {s[2] for s in rng.sample(samples, rng.randint(0, n // 3))}

                k0, k1 = -45, 45
                cfg = dict(dt="0.5", mode=mode, max_gap="7.5", t0="-0.25",
                           k0=k0, k1=k1)

                # chunked: random split, random chunk order, retractions inline
                order = list(samples)
                rng.shuffle(order)
                chunks, i = [], 0
                while i < len(order):
                    j = min(len(order), i + rng.randint(1, 9))
                    chunks.append(order[i:j])
                    i = j
                rng.shuffle(chunks)
                arrival = [s for chunk in chunks for s in chunk]

                # whole-segment reference ingests the same arrival order
                # (same-time samples resolve by arrival order)
                ref = compute_reference(arrival, breaks,
                                        retract_ids=retract_ids, **cfg)

                r = Resampler(dt=cfg["dt"], mode=mode, max_gap=cfg["max_gap"],
                              t0=cfg["t0"])
                for b in breaks:
                    r.add_breakpoint(b)
                for chunk in chunks:
                    for s in chunk:
                        r.add_sample(*s)
                    for s in chunk:  # retract some as soon as they arrive
                        if s[2] in retract_ids and rng.random() < 0.5:
                            r.retract(s[2])
                for s in arrival:  # retract the rest
                    if s[2] in retract_ids:
                        try:
                            r.retract(s[2])
                        except ValueError:
                            pass

                got = r.grid_range(k0, k1)
                self.assertEqual(got, ref,
                                 f"mode={mode} trial={trial} mismatch")


class CloseUntilTest(unittest.TestCase):
    def test_close_releases_history_and_rejects_late_mutations(self):
        r = Resampler(dt=1, mode=MODE_LINEAR, max_gap=10)
        for i, t in enumerate(range(0, 20, 2)):
            r.add_sample(t, i, sample_id=f"s{t}")
        r.add_breakpoint(1)
        r.grid_range(0, 20)
        info = r.close_until(10)
        self.assertEqual(info["anchor"], Fraction(10))
        self.assertGreater(info["released_samples"], 0)
        self.assertGreaterEqual(info["released_breakpoints"], 1)
        # closed interval rejects late arrivals and retractions
        with self.assertRaises(ValueError):
            r.add_sample(5, 0)
        with self.assertRaises(ValueError):
            r.retract("s4")
        with self.assertRaises(ValueError):
            r.retract("s0")  # released id is gone
        # future queries still work and use the anchor (10,5) as left edge
        self.assertEqual(r.point_at(11).value, D("5.500000"))  # (10,5)->(12,6)
        r.add_sample(20, 100, sample_id="s20")
        self.assertEqual(r.point_at(19).value, D("54.500000"))  # (18,9)->(20,100)
        # anchor itself cannot be retracted (it is <= closed horizon)
        with self.assertRaises(ValueError):
            r.retract("s10")

    def test_close_preserves_future_results(self):
        r = Resampler(dt=1, mode=MODE_STEP, max_gap=100)
        for t in range(0, 30):
            r.add_sample(t, t * 2, sample_id=f"s{t}")
        before = r.grid_range(15, 35)
        r.close_until(15)
        after = r.grid_range(15, 35)
        self.assertEqual(before, after)


if __name__ == "__main__":
    unittest.main(verbosity=2)
