"""Direct time-domain reference tests for kernel_switcher.

All expected values are computed from the specification (linear
crossfade on shared input history) with plain Python convolution,
independent of the implementation under test.
"""

import json
import unittest

from kernel_switcher import KernelSwitcher, MAX_VOICES
from fixtures import SAMPLE_RATE, KERNELS, impulse, constant, sine

TOL = 1e-9  # declared numerical tolerance (float64; observed <= ~1e-13)


def ref_fir(kernel, history, x):
    """Direct-form FIR reference. `history` is most-recent-first."""
    acc = kernel[0] * x
    for j in range(1, len(kernel)):
        acc += kernel[j] * history[j - 1]
    return acc


class ImpulseResponseTest(unittest.TestCase):
    """Impulse checks before, at, and after the switch point."""

    def setUp(self):
        self.h1 = [1.0, 0.5]
        self.h2 = [0.25, -0.25, 0.125]
        self.switch_at = 100
        self.fade = 16

    def make(self, src):
        sw = KernelSwitcher(self.h1, "h1", SAMPLE_RATE)
        sw.schedule_switch("h2", self.h2, self.fade, at_sample=self.switch_at)
        return sw, sw.process_block(src)

    def test_zero_latency(self):
        sw = KernelSwitcher([1.0], "id", SAMPLE_RATE)
        out = sw.process_block(impulse(8, index=0)).samples
        self.assertEqual(out[0], 1.0)  # no delay: impulse answered immediately

    def test_impulse_before_switch(self):
        n = 300
        src = impulse(n, index=10)
        _, blk = self.make(src)
        out = blk.samples
        self.assertAlmostEqual(out[10], self.h1[0], delta=TOL)
        self.assertAlmostEqual(out[11], self.h1[1], delta=TOL)
        self.assertAlmostEqual(out[12], 0.0, delta=TOL)

    def test_impulse_after_switch(self):
        n = 300
        src = impulse(n, index=200)  # fade completes at sample 116
        _, blk = self.make(src)
        out = blk.samples
        for j, tap in enumerate(self.h2):
            self.assertAlmostEqual(out[200 + j], tap, delta=TOL)
        self.assertAlmostEqual(out[200 + len(self.h2)], 0.0, delta=TOL)

    def test_impulse_at_switch_point_blends(self):
        n = 300
        src = impulse(n, index=self.switch_at)
        _, blk = self.make(src)
        out = blk.samples
        for j in range(0, self.fade + 3):
            g = min(j / self.fade, 1.0)
            w1, w2 = 1.0 - g, g
            expected = 0.0
            if j < len(self.h1):
                expected += w1 * self.h1[j]
            if j < len(self.h2):
                expected += w2 * self.h2[j]
            self.assertAlmostEqual(
                out[self.switch_at + j], expected, delta=TOL,
                msg=f"blend mismatch at fade offset {j}",
            )

    def test_tail_survives_switch_point(self):
        # Impulse one sample before the switch: its old-kernel tail must
        # continue across the switch point and decay smoothly, not be cut.
        h1 = [1.0, 0.5, 0.25, 0.125]
        h2 = [0.25, -0.25, 0.125]
        n = 300
        sw = KernelSwitcher(h1, "h1", SAMPLE_RATE)
        sw.schedule_switch("h2", h2, self.fade, at_sample=self.switch_at)
        src = impulse(n, index=self.switch_at - 2)
        blk = sw.process_block(src)
        out = blk.samples
        self.assertAlmostEqual(out[self.switch_at - 2], h1[0], delta=TOL)
        self.assertAlmostEqual(out[self.switch_at - 1], h1[1], delta=TOL)
        # Tail sample exactly at the switch point still has full old weight
        # (h2's own response to the impulse ends here with zero weight).
        self.assertAlmostEqual(out[self.switch_at], h1[2], delta=TOL)
        # One sample later the tail is faded by 1/fade, not zeroed.
        g = 1.0 / self.fade
        self.assertAlmostEqual(
            out[self.switch_at + 1], (1.0 - g) * h1[3], delta=TOL)
        self.assertGreater(out[self.switch_at + 1], 0.1)
        self.assertAlmostEqual(out[self.switch_at + 2], 0.0, delta=TOL)


class LengthChangeTest(unittest.TestCase):
    """Short -> long and long -> short kernel changes stay in bounds."""

    def test_short_to_long_to_short(self):
        h_short = KERNELS["identity"]
        h_long = KERNELS["long64"]
        n = 600
        sw = KernelSwitcher(h_short, "short", SAMPLE_RATE, max_kernel_length=64)
        sw.schedule_switch("long", h_long, 16, at_sample=50)
        sw.schedule_switch("short2", h_short, 16, at_sample=300)
        src = impulse(n, index=200)
        src[500] = 1.0
        out = sw.process_block(src).samples
        # After the first fade: full 64-tap response of the long kernel.
        for j in range(64):
            self.assertAlmostEqual(out[200 + j], 1.0 / 64.0, delta=TOL)
        self.assertAlmostEqual(out[264], 0.0, delta=TOL)
        # After switching back: single-tap response, no stale long tail.
        self.assertAlmostEqual(out[500], 1.0, delta=TOL)
        self.assertAlmostEqual(out[501], 0.0, delta=TOL)
        # Shared delay line is fixed at the declared bound.
        self.assertEqual(sw.history_length, 64 - 1)

    def test_overlong_kernel_rejected(self):
        sw = KernelSwitcher([1.0], "id", SAMPLE_RATE, max_kernel_length=8)
        with self.assertRaises(ValueError):
            sw.schedule_switch("toolong", [0.1] * 9, 16)
        with self.assertRaises(ValueError):
            KernelSwitcher([0.1] * 9, "bad", SAMPLE_RATE, max_kernel_length=8)


class ConstantSignalTest(unittest.TestCase):
    """Equal-gain kernels on a constant input must not produce steps."""

    def test_no_step_across_switches(self):
        n = 2000
        sw = KernelSwitcher(KERNELS["identity"], "identity", SAMPLE_RATE)
        sw.schedule_switch("lp4", KERNELS["lp4"], 64, at_sample=200)
        sw.schedule_switch("long32", KERNELS["long32"], 128, at_sample=500)
        sw.schedule_switch("identity2", KERNELS["identity"], 32, at_sample=900)
        out = sw.process_block(constant(0.75, n)).samples
        worst = max(abs(y - 0.75) for y in out)
        self.assertLess(worst, TOL)

    def test_cancel_mid_fade(self):
        # Switch identity -> lp4, then cancel back to identity mid-fade.
        n = 600
        sw = KernelSwitcher(KERNELS["identity"], "identity", SAMPLE_RATE)
        sw.schedule_switch("lp4", KERNELS["lp4"], 64, at_sample=100)
        sw.schedule_switch("identity", KERNELS["identity"], 64, at_sample=120)
        src = constant(2.0, n)
        src[500] = 3.0  # impulse on top, after everything settled
        blk = sw.process_block(src)
        for i in range(0, 500):
            self.assertAlmostEqual(blk.samples[i], 2.0, delta=TOL,
                                   msg=f"step at sample {i}")
        # Final kernel is identity again.
        self.assertAlmostEqual(blk.samples[500], 3.0, delta=TOL)
        self.assertAlmostEqual(blk.samples[501], 2.0, delta=TOL)


class SwitchDuringSwitchTest(unittest.TestCase):
    """Switches inside fades: weights stay normalised, tails composed."""

    def test_weights_sum_to_one_and_voices_bounded(self):
        n = 1200
        sw = KernelSwitcher(KERNELS["identity"], "v0", SAMPLE_RATE)
        versions = ["lp2", "lp4", "echo", "long32"]
        for k in range(8):
            ver = versions[k % len(versions)]
            sw.schedule_switch(ver, KERNELS[ver], 200, at_sample=100 + 20 * k)
        blk = sw.process_block(sine(440.0, n))
        self.assertLessEqual(sw.active_voice_count, MAX_VOICES)
        for i, weights in enumerate(blk.weights):
            self.assertAlmostEqual(sum(weights.values()), 1.0, delta=TOL,
                                   msg=f"weights not normalised at {i}")
        for y in blk.samples:
            self.assertLessEqual(abs(y), 1.0 + TOL)

    def test_composition_preserves_constant_signal(self):
        # Force merges with rapid switches on equal-gain kernels; the
        # composed voices must still reproduce the constant exactly.
        n = 1500
        sw = KernelSwitcher(KERNELS["identity"], "v0", SAMPLE_RATE)
        pool = ["lp2", "lp4", "long32", "identity"]
        for k in range(12):
            ver = pool[k % len(pool)]
            sw.schedule_switch(ver, KERNELS[ver], 300, at_sample=50 + 15 * k)
        blk = sw.process_block(constant(1.25, n))
        worst = max(abs(y - 1.25) for y in blk.samples)
        self.assertLess(worst, TOL)
        merges = [e for e in sw.graph["edges"] if e["type"] == "merge"]
        self.assertTrue(merges, "expected at least one composed voice")
        self.assertLessEqual(sw.active_voice_count, MAX_VOICES)

    def test_per_sample_cost_bound(self):
        sw = KernelSwitcher(KERNELS["identity"], "v0", SAMPLE_RATE)
        for k in range(6):
            sw.schedule_switch("long64", KERNELS["long64"], 100,
                               at_sample=10 + 10 * k)
        sw.process_block(sine(100.0, 400))
        self.assertLessEqual(sw.worst_case_macs, MAX_VOICES * 64)


class BlockPartitionTest(unittest.TestCase):
    """Block partitioning must not move switch sample points."""

    def test_block_size_invariance(self):
        n = 1000
        src = sine(330.0, n)

        def run(block_sizes):
            sw = KernelSwitcher(KERNELS["lp2"], "a", SAMPLE_RATE)
            sw.schedule_switch("b", KERNELS["echo"], 48, at_sample=257)
            sw.schedule_switch("c", KERNELS["long32"], 96, at_sample=613)
            out, wts = [], []
            pos = 0
            for size in block_sizes:
                blk = sw.process_block(src[pos:pos + size])
                out.extend(blk.samples)
                wts.extend(blk.weights)
                pos += size
            return out, wts

        ref_out, ref_w = run([n])
        for sizes in ([1] * n, [7] * (n // 7) + [n % 7], [64, 13, 500, 423]):
            out, wts = run([s for s in sizes if s])
            self.assertEqual(out, ref_out)  # bit-identical
            self.assertEqual(wts, ref_w)


class SnapshotRestoreTest(unittest.TestCase):
    """Any sample boundary is a safe point for save/restore."""

    def test_restore_mid_fade_is_bit_exact(self):
        n1, n2 = 180, 220
        src = sine(220.0, n1 + n2)

        sw = KernelSwitcher(KERNELS["lp4"], "a", SAMPLE_RATE)
        sw.schedule_switch("b", KERNELS["long32"], 200, at_sample=100)
        sw.schedule_switch("c", KERNELS["echo"], 150, at_sample=150)
        first = sw.process_block(src[:n1])
        snap = json.loads(json.dumps(sw.snapshot()))  # prove serialisable
        tail_ref = sw.process_block(src[n1:])

        sw2 = KernelSwitcher.restore(snap)
        self.assertEqual(sw2.n, n1)
        tail = sw2.process_block(src[n1:])
        self.assertEqual(tail.samples, tail_ref.samples)  # bit-identical
        self.assertEqual(tail.weights, tail_ref.weights)
        self.assertEqual(first.start_sample, 0)
        self.assertEqual(tail.start_sample, n1)


class MetadataTest(unittest.TestCase):
    """Output carries source-kernel weights and exact sample times."""

    def test_weights_and_times(self):
        n = 300
        sw = KernelSwitcher(KERNELS["identity"], "id", SAMPLE_RATE)
        sw.schedule_switch("lp2", KERNELS["lp2"], 100, at_sample=50)
        blk1 = sw.process_block(constant(1.0, 100))
        blk2 = sw.process_block(constant(1.0, n - 100))
        self.assertEqual(blk1.start_sample, 0)
        self.assertEqual(blk2.start_sample, 100)
        self.assertEqual(blk1.end_sample, 100)
        times = blk1.times
        self.assertEqual(times[0], 0.0)
        self.assertAlmostEqual(times[99], 99.0 / SAMPLE_RATE, delta=1e-15)
        # Mid-fade weights reference both source kernels.
        mid = blk1.weights[75]  # sample 75, fade runs 50..150
        self.assertAlmostEqual(mid["id"], 1.0 - 25.0 / 100.0, delta=TOL)
        self.assertAlmostEqual(mid["lp2"], 25.0 / 100.0, delta=TOL)
        # After the fade only the new kernel remains.
        self.assertEqual(set(blk2.weights[-1]), {"lp2"})


class TransitionGraphTest(unittest.TestCase):
    def test_graph_records_switches_and_merges(self):
        sw = KernelSwitcher(KERNELS["identity"], "v0", SAMPLE_RATE)
        pool = ["lp2", "lp4", "echo", "long32", "hp2"]
        for k in range(6):
            ver = pool[k % len(pool)]
            sw.schedule_switch(ver, KERNELS[ver], 100, at_sample=10 + 10 * k)
        sw.process_block(sine(100.0, 400))
        nodes = sw.graph["nodes"]
        for ver in ["v0"] + pool:
            self.assertIn(ver, nodes)
        switches = [e for e in sw.graph["edges"] if e["type"] == "switch"]
        self.assertEqual(len(switches), 6)
        self.assertEqual(switches[0]["sample"], 10)
        self.assertEqual(switches[0]["fade_samples"], 100)
        self.assertEqual(switches[0]["to"], "lp2")
        merges = [e for e in sw.graph["edges"] if e["type"] == "merge"]
        self.assertTrue(merges)
        for edge in merges:
            self.assertEqual(len(edge["from"]), 2)
            self.assertEqual(len(edge["weights"]), 2)


class SchedulingTest(unittest.TestCase):
    def test_switch_in_past_rejected(self):
        sw = KernelSwitcher([1.0], "id", SAMPLE_RATE)
        sw.process_block([0.0] * 10)
        with self.assertRaises(ValueError):
            sw.schedule_switch("x", [1.0], 10, at_sample=5)

    def test_empty_kernel_rejected(self):
        sw = KernelSwitcher([1.0], "id", SAMPLE_RATE)
        with self.assertRaises(ValueError):
            sw.schedule_switch("x", [], 10)

    def test_restore_rejects_bad_format(self):
        with self.assertRaises(ValueError):
            KernelSwitcher.restore({"format": "other"})


if __name__ == "__main__":
    unittest.main()
