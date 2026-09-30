"""Validation suite for the fractional-delay alignment engine.

The streaming engine is cross-checked against the full-array reference
convolution under arbitrary chunking, tail flushing and overlapping delay
changes. Sample arrays, sample rate, FIR coefficients and the numeric
tolerance all come from the local fixture; no audio device is involved.
"""
import math
import unittest

from fractional_align import fixture
from fractional_align.engine import CONTAMINATED, ZERO_PAD, Aligner
from fractional_align.harness import drive
from fractional_align.reference import render_channel
from fractional_align.schedule import DelaySchedule

L = fixture.KERNEL_LENGTH
COEFF = fixture.fractional_coefficients


def make_aligner():
    return Aligner(
        sample_rate=fixture.SAMPLE_RATE,
        coefficients=COEFF,
        kernel_length=L,
        max_delay=fixture.MAX_DELAY,
    )


def assert_matches_reference(tc, out, inputs, schedules, missing=None):
    missing = missing or {}
    for name, samples in inputs.items():
        data, flags = out[name]
        ref_data, ref_flags = render_channel(
            samples, schedules[name], missing.get(name, ()), COEFF, L, 0, len(data)
        )
        tc.assertEqual(list(flags), list(ref_flags), f"flag mismatch on {name}")
        worst = max((abs(a - b) for a, b in zip(data, ref_data)), default=0.0)
        tc.assertLessEqual(
            worst, fixture.TOLERANCE, f"sample mismatch on {name}: {worst}"
        )


def scenario_inputs():
    return {
        "a": fixture.swept_sine(400, 200.0, 2000.0),
        "b": fixture.sine(400, 997.0, amplitude=0.6),
    }


def scenario_aligner():
    al = make_aligner()
    al.add_channel("a", initial_delay=5.25)
    al.add_channel("b", initial_delay=40.0)
    al.declare_delay("a", 120, 18.5)
    al.declare_delay("a", 260, 9.75)
    al.declare_missing("b", [(100, 105)])
    return al


class FixtureTests(unittest.TestCase):
    def test_declared_constants(self):
        self.assertEqual(fixture.SAMPLE_RATE, 48_000.0)
        self.assertGreater(fixture.TOLERANCE, 0.0)
        self.assertEqual(fixture.KERNEL_LENGTH % 2, 0)

    def test_coefficients_are_dc_normalized(self):
        for mu in (0.0, 0.25, 0.5, 0.75, 0.999999):
            taps = COEFF(mu)
            self.assertEqual(len(taps), L)
            self.assertAlmostEqual(sum(taps), 1.0, delta=1e-12)

    def test_integer_phase_is_identity(self):
        taps = COEFF(0.0)
        center = L // 2 - 1
        self.assertAlmostEqual(taps[center], 1.0, delta=1e-12)
        off = max(abs(v) for k, v in enumerate(taps) if k != center)
        self.assertLess(off, 1e-12)


class ImpulseTests(unittest.TestCase):
    def test_integer_delay_moves_impulse_exactly(self):
        n_impulse, delay = 90, 12
        al = make_aligner()
        al.add_channel("mono", initial_delay=float(delay))
        samples = fixture.impulse(256, n_impulse)
        data, flags = drive(al, {"mono": samples}, block_size=5, feed_chunk=31)["mono"]
        peak = max(range(len(data)), key=lambda i: abs(data[i]))
        self.assertEqual(peak, n_impulse + delay)
        self.assertAlmostEqual(data[peak], 1.0, delta=1e-9)
        self.assertEqual(flags[peak], 0)
        off = max(abs(v) for i, v in enumerate(data) if i != peak)
        self.assertLess(off, 1e-9)

    def test_fractional_delay_moves_impulse_to_target(self):
        n_impulse, delay = 120, 33.4
        al = make_aligner()
        al.add_channel("mono", initial_delay=delay)
        samples = fixture.impulse(512, n_impulse)
        out = drive(al, {"mono": samples}, block_size=7, feed_chunk=29)
        data, flags = out["mono"]
        assert_matches_reference(self, out, {"mono": samples},
                                 {"mono": al.schedule("mono")})
        valid = [i for i, f in enumerate(flags) if not f & ZERO_PAD]
        centroid = (sum(i * data[i] ** 2 for i in valid)
                    / sum(data[i] ** 2 for i in valid))
        # windowed-sinc truncation biases the centroid by ~0.1 sample
        self.assertAlmostEqual(centroid, n_impulse + delay, delta=0.15)
        peak = max(valid, key=lambda i: abs(data[i]))
        self.assertLessEqual(abs(peak - (n_impulse + delay)), 1.0)


class ChunkInvarianceTests(unittest.TestCase):
    def test_arbitrary_chunking_identical_output(self):
        reference_out = None
        for block_size, feed_chunk in [(1, 1), (3, 7), (16, 64), (97, 250), (511, 400)]:
            al = scenario_aligner()
            out = drive(al, scenario_inputs(),
                        block_size=block_size, feed_chunk=feed_chunk)
            if reference_out is None:
                reference_out = out
            else:
                self.assertEqual(reference_out, out)
        # committing along the way only pins trajectory knots; the stream
        # may differ by float-rounding of the re-interpolated delays (<= 1e-12)
        al = scenario_aligner()
        out = drive(al, scenario_inputs(),
                    block_size=16, feed_chunk=64, commit_every=13)
        for name in reference_out:
            ref_data, ref_flags = reference_out[name]
            data, flags = out[name]
            self.assertEqual(ref_flags, flags)
            worst = max(abs(a - b) for a, b in zip(ref_data, data))
            self.assertLessEqual(worst, 1e-12)
        # and the stream matches the full-array reference convolution
        al = scenario_aligner()
        out = drive(al, scenario_inputs(), block_size=32, feed_chunk=64)
        schedules = {name: al.schedule(name) for name in out}
        assert_matches_reference(self, out, scenario_inputs(), schedules,
                                 {"b": [(100, 105)]})


class SmoothChangeTests(unittest.TestCase):
    def test_ramp_trajectory_has_no_repeat_or_skip(self):
        al = make_aligner()
        al.add_channel("ch", initial_delay=4.0)
        al.declare_delay("ch", 80, 30.0)
        al.declare_delay("ch", 160, 6.0)
        sched = al.schedule("ch")
        max_slope = 26.0 / 80.0  # steepest declared segment
        previous_p = 0.0 - sched.delay_at(0)
        for n in range(1, 400):
            p = n - sched.delay_at(n)
            step = p - previous_p
            self.assertGreater(step, 0.0)  # never repeats an input position
            self.assertLess(step, 2.0)     # never skips undeclared input
            previous_p = p
            self.assertLessEqual(
                abs(sched.delay_at(n) - sched.delay_at(n - 1)),
                max_slope + 1e-12,
            )
        samples = fixture.swept_sine(500, 150.0, 2500.0)
        out = drive(al, {"ch": samples}, block_size=9, feed_chunk=41)
        assert_matches_reference(self, out, {"ch": samples}, {"ch": sched})

    def test_overlapping_change_retracts_uncommitted(self):
        samples = fixture.swept_sine(600, 100.0, 3000.0)
        al = make_aligner()
        al.add_channel("ch", initial_delay=5.0)
        al.declare_delay("ch", 300, 45.0)  # first ramp, still in progress below
        al.feed("ch", samples)
        al.finish("ch")
        blocks = []
        while al.cursor < 160:
            blocks.append(al.process(16))
        # change of mind mid-ramp: retract the uncommitted future, re-declare
        al.retract_delay_after("ch", 160)
        al.declare_delay("ch", 260, 40.0)
        al.declare_delay("ch", 420, 12.0)
        blocks.extend(al.flush(16))
        data = [v for b in blocks for v in b.data["ch"]]
        flags = [f for b in blocks for f in b.flags["ch"]]
        assert_matches_reference(self, {"ch": (data, flags)}, {"ch": samples},
                                 {"ch": al.schedule("ch")})
        sched = al.schedule("ch")
        for n in range(1, 500):
            self.assertLessEqual(
                abs(sched.delay_at(n) - sched.delay_at(n - 1)), 0.2 + 1e-12
            )

    def test_declare_inside_emitted_region_rejected(self):
        al = make_aligner()
        al.add_channel("ch", initial_delay=2.0)
        al.feed("ch", [0.0] * 128)
        al.finish("ch")
        al.process(16)
        with self.assertRaises(ValueError):
            al.declare_delay("ch", 8, 10.0)
        with self.assertRaises(ValueError):
            al.retract_delay_after("ch", 8)


class CheckpointTests(unittest.TestCase):
    def test_restore_reproduces_output_and_state(self):
        inputs = scenario_inputs()
        al = scenario_aligner()
        for name, samples in inputs.items():
            al.feed(name, samples)
            al.finish(name)
        for _ in range(5):
            al.process(16)  # cursor 80
        cp = al.checkpoint()
        version_a = al.schedule("a").version
        mask_len = len(al.contamination_mask("b"))
        first = [al.process(16) for _ in range(3)]
        self.assertGreater(len(al.contamination_mask("b")), mask_len)
        al.restore(cp)
        self.assertEqual(al.cursor, 80)
        self.assertEqual(al.schedule("a").version, version_a)
        self.assertEqual(len(al.contamination_mask("b")), mask_len)
        second = [al.process(16) for _ in range(3)]
        for b1, b2 in zip(first, second):
            self.assertEqual(b1.data, b2.data)
            self.assertEqual(b1.flags, b2.flags)

    def test_retract_uncommitted_region(self):
        samples = fixture.swept_sine(500, 120.0, 2800.0)
        al = make_aligner()
        al.add_channel("ch", initial_delay=6.0)
        al.feed("ch", samples)
        al.finish("ch")
        blocks = []

        def run(count):
            for _ in range(count):
                blocks.append(al.process(16))

        run(4)  # 0..64
        al.commit()
        with self.assertRaises(ValueError):
            al.declare_delay("ch", 32, 20.0)  # committed history is locked
        run(4)  # 64..128
        cp = al.checkpoint()
        al.declare_delay("ch", 200, 30.0)
        run(4)  # 128..192, about to be retracted
        del blocks[8:]
        al.restore(cp)
        al.declare_delay("ch", 200, 10.0)  # replacement change
        run(4)
        blocks.extend(al.flush(16))
        data = [v for b in blocks for v in b.data["ch"]]
        flags = [f for b in blocks for f in b.flags["ch"]]
        assert_matches_reference(self, {"ch": (data, flags)}, {"ch": samples},
                                 {"ch": al.schedule("ch")})
        ref_data, ref_flags = render_channel(
            samples, al.schedule("ch"), (), COEFF, L, 0, 64
        )
        self.assertEqual(flags[:64], ref_flags)  # committed prefix untouched


class MissingDataTests(unittest.TestCase):
    def test_missing_interval_marks_contamination(self):
        samples = fixture.sine(300, 1234.0)
        al = make_aligner()
        al.add_channel("ch", initial_delay=10.0)
        al.declare_missing("ch", [(50, 60)])
        out = drive(al, {"ch": samples}, block_size=16, feed_chunk=50)
        data, flags = out["ch"]
        contaminated = [n for n, f in enumerate(flags) if f & CONTAMINATED]
        # window [n-10-15, n-10+16] overlaps missing [50, 59] for n in [44, 84]
        self.assertEqual(contaminated, list(range(44, 85)))
        self.assertEqual(al.contamination_mask("ch"), contaminated)
        assert_matches_reference(self, out, {"ch": samples},
                                 {"ch": al.schedule("ch")},
                                 {"ch": [(50, 60)]})


class ValidIntervalTests(unittest.TestCase):
    def test_common_valid_interval_and_zero_pad_flags(self):
        inputs = {"a": fixture.sine(200, 500.0), "b": fixture.sine(200, 1500.0)}
        al = make_aligner()
        al.add_channel("a", initial_delay=4.0)
        al.add_channel("b", initial_delay=9.5)
        out = drive(al, inputs, block_size=16, feed_chunk=200)
        self.assertEqual(al.common_valid_interval(), (25, 187))
        _, flags_a = out["a"]
        _, flags_b = out["b"]
        self.assertTrue(all(f & ZERO_PAD for f in flags_a[:19]))
        self.assertTrue(all(not f & ZERO_PAD for f in flags_a[19:188]))
        self.assertTrue(all(f & ZERO_PAD for f in flags_a[188:]))
        self.assertTrue(all(f & ZERO_PAD for f in flags_b[:25]))
        self.assertTrue(all(not f & ZERO_PAD for f in flags_b[25:194]))
        self.assertTrue(all(f & ZERO_PAD for f in flags_b[194:]))
        assert_matches_reference(
            self, out, inputs, {"a": al.schedule("a"), "b": al.schedule("b")}
        )


class TailFlushTests(unittest.TestCase):
    def test_flush_drains_tail_exactly(self):
        samples = fixture.sine(180, 880.0)
        al = make_aligner()
        al.add_channel("ch", initial_delay=7.0)
        out = drive(al, {"ch": samples}, block_size=16, feed_chunk=25)
        data, flags = out["ch"]
        self.assertTrue(al.done)
        self.assertEqual(len(data), 202)  # last interesting output + 1
        valid = [n for n, f in enumerate(flags) if not f & ZERO_PAD]
        self.assertEqual(valid[0], 22)    # L/2 - 1 + delay
        self.assertEqual(valid[-1], 170)  # 180 - 1 - L/2 + delay
        assert_matches_reference(self, out, {"ch": samples},
                                 {"ch": al.schedule("ch")})


class MemoryBoundTests(unittest.TestCase):
    def test_retained_history_within_derived_bound(self):
        inputs = {
            "a": fixture.swept_sine(600, 100.0, 2000.0),
            "b": fixture.sine(600, 700.0),
        }
        al = make_aligner()
        al.add_channel("a", initial_delay=30.0)
        al.add_channel("b", initial_delay=2.5)
        high_water = {"a": 0, "b": 0}

        def probe(aligner, _block):
            for name in high_water:
                high_water[name] = max(
                    high_water[name], aligner.retained_history(name)
                )

        drive(al, inputs, block_size=16, feed_chunk=16,
              commit_every=16, on_block=probe)
        bound = al.memory_bound(uncommitted_span=32)
        self.assertEqual(bound, math.ceil(fixture.MAX_DELAY) + L + 32)
        for name, observed in high_water.items():
            self.assertGreater(observed, 0)
            self.assertLessEqual(observed, bound)


class ScheduleTests(unittest.TestCase):
    def test_slope_beyond_one_sample_per_sample_rejected(self):
        sched = DelaySchedule(0.0, 64.0)
        with self.assertRaises(ValueError):
            sched.declare(10, 15.0)  # slope +1.5 would skip input
        sched = DelaySchedule(20.0, 64.0)
        with self.assertRaises(ValueError):
            sched.declare(10, 5.0)  # slope -1.5 would repeat input

    def test_delay_bounds_rejected(self):
        sched = DelaySchedule(0.0, 64.0)
        with self.assertRaises(ValueError):
            sched.declare(10, -1.0)
        with self.assertRaises(ValueError):
            sched.declare(10, 65.0)

    def test_committed_history_locked(self):
        sched = DelaySchedule(1.0, 64.0)
        sched.declare(100, 10.0)
        sched.commit_up_to(50)
        with self.assertRaises(ValueError):
            sched.declare(40, 5.0)
        with self.assertRaises(ValueError):
            sched.retract_after(40)
        sched.retract_after(50)  # uncommitted knots removed
        self.assertEqual(sched.delay_at(100), sched.delay_at(50))

    def test_version_and_snapshot(self):
        sched = DelaySchedule(1.0, 64.0)
        self.assertEqual(sched.version, 0)
        sched.declare(100, 10.0)
        sched.declare(200, 20.0)
        self.assertEqual(sched.version, 2)
        snap = sched.snapshot()
        sched.retract_after(100)
        self.assertEqual(sched.version, 3)
        sched.restore(snap)
        self.assertEqual(sched.version, 2)
        self.assertEqual(sched.delay_at(200), 20.0)


if __name__ == "__main__":
    unittest.main()
