"""Direct time-domain reference tests for kernel_switch.

Numerical policy: float64 throughout; reference comparisons use absolute
tolerance ABS_TOL = 1e-9; block-partitioning and snapshot/restore
continuations must be bit-exact.
"""

import json
import math
import unittest

from fixtures import SAMPLE_RATE, constant, impulse, noise
from kernel_switch import KernelSwitchFilter, TransitionGraph

ABS_TOL = 1e-9

KERNEL_A = [1.0, 0.5, 0.25]                       # short, length 3
KERNEL_B = [0.5, -0.5, 0.25, -0.125]              # length 4, signed
KERNEL_C = [0.125] * 8                            # length 8, unity gain
KERNEL_LONG = [math.sin(i) / 8.0 for i in range(1, 65)]  # length 64


def convolve(x, h):
    """Reference direct-form convolution, zero initial state."""
    y = []
    for n in range(len(x)):
        acc = 0.0
        for k, coeff in enumerate(h):
            if 0 <= n - k:
                acc += coeff * x[n - k]
        y.append(acc)
    return y


def fade_weight(n, start, length):
    return min(1.0, max(0.0, (n - start + 1) / length))


def make_graph(fade=16, long_kernel=False):
    graph = TransitionGraph()
    graph.add_version("A", KERNEL_A)
    graph.add_version("B", KERNEL_B)
    graph.add_version("C", KERNEL_C)
    if long_kernel:
        graph.add_version("L", KERNEL_LONG)
        graph.add_transition("A", "L", fade_samples=fade, bidirectional=True)
    graph.add_transition("A", "B", fade_samples=fade, bidirectional=True)
    graph.add_transition("B", "C", fade_samples=fade, bidirectional=True)
    graph.add_transition("A", "C", fade_samples=fade, bidirectional=True)
    return graph


def run(filter_, signal, schedule):
    """Run signal through filter_, firing {index: (target, fade)} requests."""
    out, metas = [], []
    for n, x in enumerate(signal):
        if n in schedule:
            target, fade = schedule[n]
            filter_.request_switch(target, fade_samples=fade)
        y, meta = filter_.process_sample(x)
        out.append(y)
        metas.append(meta)
    return out, metas


class ImpulseSwitchTest(unittest.TestCase):
    """Impulse response before, during and after the switch point."""

    def test_impulse_matches_reference(self):
        n_total, switch_at, fade = 200, 40, 16
        signal = impulse(n_total)
        filt = KernelSwitchFilter(make_graph(fade), "A", SAMPLE_RATE)
        out, _ = run(filt, signal, {switch_at: ("B", fade)})

        ref_a = convolve(signal, KERNEL_A)
        ref_b = convolve(signal, KERNEL_B)
        for n in range(n_total):
            w = fade_weight(n, switch_at, fade)
            expected = (1.0 - w) * ref_a[n] + w * ref_b[n]
            self.assertAlmostEqual(out[n], expected, delta=ABS_TOL,
                                   msg="mismatch at n=%d" % n)

    def test_old_tail_fades_not_cut(self):
        # Impulse late in the fade: old kernel tail must decay smoothly.
        n_total, switch_at, fade = 64, 10, 8
        signal = impulse(n_total, at=switch_at + fade - 2)
        filt = KernelSwitchFilter(make_graph(fade), "A", SAMPLE_RATE)
        out, _ = run(filt, signal, {switch_at: ("B", fade)})
        ref_a = convolve(signal, KERNEL_A)
        ref_b = convolve(signal, KERNEL_B)
        for n in range(n_total):
            w = fade_weight(n, switch_at, fade)
            self.assertAlmostEqual(out[n], (1 - w) * ref_a[n] + w * ref_b[n],
                                   delta=ABS_TOL)


class ShortToLongKernelTest(unittest.TestCase):
    """Short kernel -> much longer kernel: no out-of-bounds reads."""

    def test_short_to_long_matches_reference(self):
        n_total, switch_at, fade = 300, 7, 32  # switch before history fills
        signal = noise(n_total, seed=7)
        filt = KernelSwitchFilter(make_graph(fade, long_kernel=True),
                                  "A", SAMPLE_RATE)
        out, _ = run(filt, signal, {switch_at: ("L", fade)})
        ref_a = convolve(signal, KERNEL_A)
        ref_l = convolve(signal, KERNEL_LONG)
        for n in range(n_total):
            w = fade_weight(n, switch_at, fade)
            self.assertAlmostEqual(out[n], (1 - w) * ref_a[n] + w * ref_l[n],
                                   delta=ABS_TOL)
        self.assertLessEqual(filt.history_length, filt.max_history_length)


class SwitchDuringFadeTest(unittest.TestCase):
    """A second switch mid-fade composes old state instead of dropping it."""

    def test_composition_matches_reference(self):
        n_total = 200
        s1, f1 = 50, 32   # A -> B fade
        s2, f2 = 60, 16   # mid-fade request B -> C (composes A+B mix)
        signal = impulse(n_total)
        filt = KernelSwitchFilter(make_graph(), "A", SAMPLE_RATE)
        out, _ = run(filt, signal, {s1: ("B", f1), s2: ("C", f2)})

        ref_a = convolve(signal, KERNEL_A)
        ref_b = convolve(signal, KERNEL_B)
        ref_c = convolve(signal, KERNEL_C)
        # Weight frozen at the compose instant (last applied: sample s2-1).
        w_frozen = fade_weight(s2 - 1, s1, f1)
        for n in range(n_total):
            if n < s2:
                w = fade_weight(n, s1, f1)
                expected = (1 - w) * ref_a[n] + w * ref_b[n]
            else:
                w = fade_weight(n, s2, f2)
                composite = ((1 - w_frozen) * ref_a[n]
                             + w_frozen * ref_b[n])
                expected = (1 - w) * composite + w * ref_c[n]
            self.assertAlmostEqual(out[n], expected, delta=ABS_TOL,
                                   msg="mismatch at n=%d" % n)

    def test_cancel_returns_to_source_kernel(self):
        n_total = 300
        s1, f1 = 100, 64  # A -> B
        s2, f2 = 120, 32  # cancel: B -> A mid-fade
        signal = noise(n_total, seed=11)
        filt = KernelSwitchFilter(make_graph(), "A", SAMPLE_RATE)
        out, metas = run(filt, signal, {s1: ("B", f1), s2: ("A", f2)})
        ref_a = convolve(signal, KERNEL_A)
        settled = s2 + f2
        for n in range(settled, n_total):
            self.assertAlmostEqual(out[n], ref_a[n], delta=ABS_TOL)
            self.assertAlmostEqual(metas[n].weights["A"], 1.0, delta=ABS_TOL)

    def test_constant_signal_cancel_has_no_step(self):
        n_total = 400
        signal = constant(n_total, 1.0)
        filt = KernelSwitchFilter(make_graph(), "C", SAMPLE_RATE)
        # C and the equal-gain kernel D both have unity DC gain.
        filt._graph.add_version("D", [0.25] * 4)
        filt._graph.add_transition("C", "D", 64, bidirectional=True)
        out, _ = run(filt, signal, {100: ("D", 64), 130: ("C", 32)})
        for n in range(16, n_total):
            self.assertAlmostEqual(out[n], 1.0, delta=ABS_TOL,
                                   msg="step at n=%d" % n)


class ConstantSignalTest(unittest.TestCase):
    """Equal-gain kernels on a constant signal: no visible step."""

    def test_no_step_between_equal_gain_kernels(self):
        n_total, switch_at, fade = 256, 100, 32
        signal = constant(n_total, 1.0)
        graph = TransitionGraph()
        graph.add_version("half2", [0.5, 0.5])
        graph.add_version("quarter4", [0.25] * 4)
        graph.add_transition("half2", "quarter4", fade, bidirectional=True)
        filt = KernelSwitchFilter(graph, "half2", SAMPLE_RATE)
        out, _ = run(filt, signal, {switch_at: ("quarter4", fade)})
        for n in range(2, n_total):  # after initial ramp of kernel half2
            self.assertAlmostEqual(out[n], 1.0, delta=ABS_TOL,
                                   msg="step at n=%d" % n)


class BlockInvarianceTest(unittest.TestCase):
    """Block partitioning must not move switch sample points."""

    def test_block_size_invariance_bit_exact(self):
        n_total = 1000
        signal = noise(n_total, seed=23)
        schedule = {100: ("B", 64), 150: ("C", 48), 400: ("A", 16)}
        reference = None
        for block in (1, 3, 7, 64, 257, 1000):
            filt = KernelSwitchFilter(make_graph(), "A", SAMPLE_RATE)
            out = []
            for start in range(0, n_total, block):
                end = min(start + block, n_total)
                # Split the chunk at scheduled switch points so each
                # request lands exactly on its sample boundary.
                cuts = [n for n in sorted(schedule) if start < n < end]
                seg_start = start
                for seg_end in cuts + [end]:
                    if seg_start in schedule:
                        filt.request_switch(
                            schedule[seg_start][0],
                            fade_samples=schedule[seg_start][1])
                    ys, _ = filt.process_block(signal[seg_start:seg_end])
                    out.extend(ys)
                    seg_start = seg_end
            if reference is None:
                reference = out
            else:
                self.assertEqual(out, reference,
                                 "block size %d changed output" % block)


class SnapshotRestoreTest(unittest.TestCase):
    """Any sample boundary is a safe point for save/restore."""

    def test_restore_mid_fade_bit_exact(self):
        n_total, snap_at = 300, 137
        signal = noise(n_total, seed=31)
        schedule = {50: ("B", 64), 120: ("C", 32)}
        filt = KernelSwitchFilter(make_graph(), "A", SAMPLE_RATE)
        for n in range(snap_at):
            if n in schedule:
                filt.request_switch(*schedule[n])
            filt.process_sample(signal[n])
        snap = json.loads(json.dumps(filt.snapshot()))  # JSON round-trip
        self.assertTrue(filt.fade_in_progress)

        tail_ref = [filt.process_sample(x)[0] for x in signal[snap_at:]]
        restored = KernelSwitchFilter.restore(make_graph(), snap)
        tail_new = [restored.process_sample(x)[0] for x in signal[snap_at:]]
        self.assertEqual(tail_ref, tail_new)
        self.assertEqual(restored.sample_index, n_total)


class BoundedResourcesTest(unittest.TestCase):
    """Working memory and per-sample cost stay bounded under rapid switches."""

    def test_rapid_switching_stays_bounded(self):
        graph = make_graph()
        filt = KernelSwitchFilter(graph, "A", SAMPLE_RATE)
        signal = noise(500, seed=41)
        targets = ["B", "C", "A"]
        max_instances = 0
        for n, x in enumerate(signal):
            if n % 3 == 0:
                filt.request_switch(targets[(n // 3) % 3], fade_samples=64)
            filt.process_sample(x)
            max_instances = max(max_instances, filt.active_instance_count)
            self.assertLessEqual(filt.history_length,
                                 filt.max_history_length)
        self.assertLessEqual(max_instances, filt.MAX_ACTIVE_INSTANCES)


class MetadataTest(unittest.TestCase):
    """Output carries source kernel weights and exact sample time."""

    def test_weights_sum_to_one_and_time_is_exact(self):
        n_total = 200
        signal = noise(n_total, seed=53)
        filt = KernelSwitchFilter(make_graph(), "A", SAMPLE_RATE)
        _, metas = run(filt, signal, {40: ("B", 32), 60: ("C", 16)})
        for n, meta in enumerate(metas):
            self.assertEqual(meta.sample_index, n)
            self.assertEqual(meta.time_seconds, n / SAMPLE_RATE)
            total = math.fsum(meta.weights.values())
            self.assertAlmostEqual(total, 1.0, delta=1e-12)
            for name in meta.weights:
                self.assertIn(name, ("A", "B", "C"))
        # Mid-fade of A->B at n=40..55: both sources present at n=48.
        self.assertIn("A", metas[48].weights)
        self.assertIn("B", metas[48].weights)


class GraphValidationTest(unittest.TestCase):
    def test_invalid_transition_rejected(self):
        graph = TransitionGraph()
        graph.add_version("A", KERNEL_A)
        graph.add_version("B", KERNEL_B)
        filt = KernelSwitchFilter(graph, "A", SAMPLE_RATE)
        with self.assertRaises(KeyError):
            filt.request_switch("B")
        with self.assertRaises(KeyError):
            filt.request_switch("missing")
        self.assertFalse(filt.request_switch("A"))  # no-op on current

    def test_latency_is_zero(self):
        # Output at n depends only on inputs <= n: an impulse at n=0
        # produces a nonzero output already at n=0.
        filt = KernelSwitchFilter(make_graph(), "A", SAMPLE_RATE)
        y, meta = filt.process_sample(1.0)
        self.assertEqual(meta.sample_index, 0)
        self.assertAlmostEqual(y, KERNEL_A[0], delta=ABS_TOL)


if __name__ == "__main__":
    unittest.main(verbosity=2)
