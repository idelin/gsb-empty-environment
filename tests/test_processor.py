"""Processor: block-size invariance, state save/restore, multichannel
sidechain modes, cascade vs merged parameters, latency, silence safety."""
import math
import os
import random
import sys
import unittest

sys.path.insert(0, os.path.join(os.path.dirname(__file__), ".."))

import fixtures
from dyncurve import (LATENCY_SAMPLES, DetectorConfig, DynamicsProcessor,
                      Segment, SidechainConfig, SmootherConfig, compile_curve)

FS = fixtures.SAMPLE_RATE


def make_proc(curve, channels=1, sidechain=None):
    return DynamicsProcessor(
        FS, channels, curve,
        detector=DetectorConfig("peak", 1.0, 50.0),
        smoother=SmootherConfig(1.0, 30.0),
        sidechain=sidechain or SidechainConfig("max"))


def comp_curve():
    return compile_curve([Segment(-30.0, 4.0, knee_db=6.0)])


class TestBlockInvariance(unittest.TestCase):
    def test_random_block_sizes_match_single_block(self):
        sig = fixtures.demo_signal(FS)
        rng = random.Random(7)
        ref = make_proc(comp_curve()).process_block([sig])[0]
        # random blocking
        proc = make_proc(comp_curve())
        out, pos = [], 0
        while pos < len(sig):
            n = min(rng.randint(1, 700), len(sig) - pos)
            out.extend(proc.process_block([sig[pos:pos + n]])[0])
            pos += n
        self.assertEqual(ref, out)  # bit-identical, per declared tolerance 0.0

    def test_state_save_restore_matches_continuous_run(self):
        sig = fixtures.demo_signal(FS)
        split = 4321
        ref_proc = make_proc(comp_curve())
        ref = ref_proc.process_block([sig])[0]
        ref_state = ref_proc.get_state()

        proc = make_proc(comp_curve())
        first = proc.process_block([sig[:split]])[0]
        snap = proc.get_state()
        # simulate persistence: round-trip through plain copies
        snap = {k: (list(v) if isinstance(v, list) else v) for k, v in snap.items()}
        rest = proc.process_block([sig[split:]])[0]
        self.assertEqual(ref, first + rest)

        proc2 = make_proc(comp_curve())
        proc2.process_block([sig[:split]])
        proc2.set_state(snap)
        rest2 = proc2.process_block([sig[split:]])[0]
        self.assertEqual(rest, rest2)
        # final runtime state identical to the continuous run
        s1, s2 = ref_state, proc2.get_state()
        self.assertEqual(s1["sample"], s2["sample"])
        self.assertEqual(s1["detectors"], s2["detectors"])
        self.assertEqual(s1["smoothers"], s2["smoothers"])


class TestSidechain(unittest.TestCase):
    def setUp(self):
        self.block = fixtures.multichannel_sine(3, 220.0, 0.1, FS)

    def test_linked_max_shares_gain(self):
        proc = make_proc(comp_curve(), 3, SidechainConfig("max"))
        out = proc.process_block(self.block)
        # linked gain: per-sample ratio out/in identical across channels
        for i in (1000, 3000, 4000):
            ratios = [out[ch][i] / self.block[ch][i] for ch in range(3)
                      if abs(self.block[ch][i]) > 1e-6]
            for r in ratios[1:]:
                self.assertAlmostEqual(ratios[0], r, places=12)

    def test_independent_channels_differ(self):
        proc = make_proc(comp_curve(), 3, SidechainConfig("independent"))
        out = proc.process_block(self.block)
        i = 4000
        gains = []
        for ch in range(3):
            if abs(self.block[ch][i]) > 1e-6:
                gains.append(out[ch][i] / self.block[ch][i])
        self.assertGreater(len(set(round(g, 9) for g in gains)), 1)

    def test_mean_and_weighted_modes(self):
        out_mean = make_proc(comp_curve(), 3, SidechainConfig("mean")
                             ).process_block(self.block)
        out_w = make_proc(comp_curve(), 3,
                          SidechainConfig("weighted", (0.8, 0.1, 0.1))
                          ).process_block(self.block)
        out_max = make_proc(comp_curve(), 3, SidechainConfig("max")
                            ).process_block(self.block)
        # different combining rules -> different gains somewhere
        self.assertNotEqual(out_mean[0][4000], out_max[0][4000])
        self.assertNotEqual(out_w[0][4000], out_max[0][4000])
        with self.assertRaises(ValueError):
            DynamicsProcessor(FS, 3, comp_curve(),
                              sidechain=SidechainConfig("weighted", (1.0,)))


class TestCascadeVsMerged(unittest.TestCase):
    def test_cascade_differs_from_merged_parameters(self):
        """Compressor -> expander cascade is NOT equivalent to a single
        stage with merged curve parameters: two envelope/smoother chains
        react differently than one."""
        sig = fixtures.demo_signal(FS)
        comp = compile_curve([Segment(-24.0, 4.0)])
        expn = compile_curve([Segment(-60.0, 2.0, kind="expand")])
        merged = compile_curve([Segment(-60.0, 2.0, kind="expand"),
                                Segment(-24.0, 4.0)])
        stage1 = make_proc(comp)
        stage2 = make_proc(expn)
        cascaded = stage2.process_block(stage1.process_block([sig]))[0]
        merged_out = make_proc(merged).process_block([sig])[0]
        self.assertEqual(len(cascaded), len(merged_out))
        diffs = [abs(a - b) for a, b in zip(cascaded, merged_out)]
        self.assertGreater(max(diffs), 1e-3)
        # and the merged curve's static gain equals the cascade's static gain
        # (the difference is purely dynamic, not a curve miscompile)
        for x in (-80.0, -50.0, -30.0, -10.0):
            self.assertAlmostEqual(comp.gain_db(x) + expn.gain_db(x),
                                   merged.gain_db(x), delta=1e-9)


class TestLatencyAndSilence(unittest.TestCase):
    def test_declared_latency(self):
        proc = make_proc(comp_curve())
        self.assertEqual(proc.latency_samples, LATENCY_SAMPLES)
        self.assertEqual(LATENCY_SAMPLES, 0)

    def test_silence_in_silence_out_finite(self):
        proc = make_proc(compile_curve([Segment(-60.0, 3.0, kind="expand"),
                                        Segment(-20.0, 8.0)]))
        out = proc.process_block([fixtures.silence(0.05, FS)])[0]
        self.assertTrue(all(v == 0.0 for v in out))
        self.assertTrue(all(math.isfinite(v) for v in out))

    def test_ratio_one_processor_is_identity(self):
        sig = fixtures.demo_signal(FS)
        proc = make_proc(compile_curve([Segment(-40.0, 1.0)]))
        out = proc.process_block([sig])[0]
        # identity up to float round-off in the dB-domain gain path (~1 ulp)
        for a, b in zip(sig, out):
            self.assertAlmostEqual(a, b, places=12)


if __name__ == "__main__":
    unittest.main()
