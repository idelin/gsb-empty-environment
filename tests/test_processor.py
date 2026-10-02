"""Processor tests: block-size invariance, state snapshot, sidechain
routing, hot reload (no gain jump, latency unchanged), envelope-state
policy, cascade != merged parameters, silence safety."""
import math
import random
import unittest

from dyncurve import (COMPRESS, EXPAND, DetectorConfig, DynamicsProcessor,
                      EnvelopePolicy, Segment, SidechainConfig, SmootherConfig,
                      amp_to_db, compile_curve, db_to_amp)
from dyncurve.fixtures import (SAMPLE_RATE, multichannel_demo, silence,
                               sine_burst, step_signal)

FS = SAMPLE_RATE
GAIN_JUMP_TOL = 1e-9  # linear-domain tolerance for "no gain step" checks


def make_curve():
    return compile_curve([
        Segment(-50.0, 2.0, knee_db=4.0, kind=EXPAND),
        Segment(-24.0, 4.0, knee_db=8.0, kind=COMPRESS),
    ])


def make_processor(curve=None, sidechain=None, **kw):
    cfg = dict(num_channels=2, sample_rate=FS,
               curve=curve or make_curve(),
               detector=DetectorConfig("peak", 0.003, 0.05),
               smoother=SmootherConfig(0.005, 0.1),
               sidechain=sidechain or SidechainConfig())
    cfg.update(kw)
    return DynamicsProcessor(**cfg)


class TestBlockInvariance(unittest.TestCase):
    """Random blockings of one stream must give identical output+state."""

    def test_random_block_sizes(self):
        block = multichannel_demo()
        ref = make_processor()
        ref_out = ref.process_block(block)

        rng = random.Random(99)
        proc = make_processor()
        outs = [[], []]
        pos = 0
        while pos < len(block[0]):
            n = rng.randint(1, 977)
            n = min(n, len(block[0]) - pos)
            chunk = [ch[pos:pos + n] for ch in block]
            part = proc.process_block(chunk)
            for ch in range(2):
                outs[ch].extend(part[ch])
            pos += n

        for ch in range(2):
            self.assertEqual(ref_out[ch], outs[ch])
        self.assertEqual(ref.get_state(), proc.get_state())

    def test_state_snapshot_restore(self):
        block = multichannel_demo()
        proc = make_processor()
        proc.process_block([ch[:1000] for ch in block])
        snap = proc.get_state()
        out_a = proc.process_block([ch[1000:2000] for ch in block])
        state_a = proc.get_state()
        proc.set_state(snap)
        out_b = proc.process_block([ch[1000:2000] for ch in block])
        self.assertEqual(out_a, out_b)
        # reprocessing from the restored snapshot reproduces the state too
        self.assertEqual(proc.get_state(), state_a)


class TestSidechain(unittest.TestCase):
    def test_linked_applies_one_gain_to_all_channels(self):
        block = multichannel_demo()
        proc = make_processor(sidechain=SidechainConfig(link="linked"))
        out = proc.process_block(block)
        # identical linear gain on both channels at every sample
        for i in (0, 500, 2500, 4799):
            if abs(block[0][i]) > 1e-12 and abs(block[1][i]) > 1e-12:
                g0 = out[0][i] / block[0][i]
                g1 = out[1][i] / block[1][i]
                self.assertAlmostEqual(g0, g1, places=12)

    def test_independent_channels_differ(self):
        block = multichannel_demo()
        proc = make_processor(sidechain=SidechainConfig(link="independent"))
        out = proc.process_block(block)
        i = 3000  # ch0 loud, ch1 silent there
        g0 = out[0][i] / block[0][i]
        self.assertNotAlmostEqual(g0, 1.0, places=3)
        # ch1 is silent at i=3000 -> output exactly 0, gain finite
        self.assertEqual(out[1][i], 0.0)

    def test_external_sidechain_drives_gain(self):
        main = [sine_burst(amp=0.5, n=2000), sine_burst(amp=0.5, n=2000)]
        sc = [[1.0] * 2000, [1.0] * 2000]  # hot sidechain -> deep gain cut
        proc = make_processor(
            sidechain=SidechainConfig(channels=(0,), link="linked"))
        out = proc.process_block(main, sidechain_block=sc)
        peak_out = max(abs(x) for x in out[0][-500:])
        self.assertLess(amp_to_db(peak_out), -12.0)  # strongly compressed

    def test_rms_detector_tracks_sine(self):
        from dyncurve.envelope import RMSDetector
        det = RMSDetector(0.001, 0.001, FS)
        envs = [det.next(x) for x in sine_burst(amp=0.5, n=4800)]
        # average over the last 11 whole periods to remove one-pole ripple
        env = sum(envs[-1200:]) / 1200.0
        self.assertAlmostEqual(env, 0.5 / math.sqrt(2.0), delta=2e-3)


class TestHotReload(unittest.TestCase):
    def setUp(self):
        self.block = multichannel_demo()
        self.new_curve = compile_curve([Segment(-24.0, 2.0, knee_db=6.0)])

    def _gains(self, proc, out, block):
        return [out[0][i] / block[0][i] if block[0][i] != 0.0 else None
                for i in range(len(block[0]))]

    def test_crossfade_no_gain_jump_and_latency_unchanged(self):
        # Constant signal + settled envelope: during the fade the only
        # gain movement is the crossfade ramp itself.  Instant smoother
        # means any gain step would show up directly.
        block = [[0.5] * 9600, [0.5] * 9600]
        proc = make_processor(smoother=SmootherConfig(0.0, 0.0))
        self.assertEqual(proc.latency_samples, 0)
        out1 = proc.process_block([ch[:] for ch in block])
        g_old_db = 20.0 * math.log10(out1[0][-1] / 0.5)
        proc.update_curve(self.new_curve, fade_samples=256,
                          envelope_policy=EnvelopePolicy.RETAIN)
        self.assertEqual(proc.latency_samples, 0)  # latency never changes
        out2 = proc.process_block([ch[:] for ch in block])
        g_db = [20.0 * math.log10(v / 0.5) for v in out2[0]]
        lvl = amp_to_db(0.5)
        new_db = self.new_curve.gain_db(lvl)
        old_db = make_curve().gain_db(lvl)
        ramp = abs(new_db - old_db) / 256.0
        # no step at the switch point: first faded sample is ~1/256 along
        self.assertLessEqual(abs(g_db[0] - g_old_db), 2.0 * ramp)
        # during the fade: smooth ramp, never a larger per-sample delta
        for i in range(1, 256):
            self.assertLessEqual(abs(g_db[i] - g_db[i - 1]),
                                 ramp + GAIN_JUMP_TOL)
        # after the fade the new curve's static gain is reached exactly
        for v in g_db[256:]:
            self.assertAlmostEqual(v, new_db, places=9)
        self.assertAlmostEqual(g_old_db, old_db, places=9)

    def test_fade_starts_at_block_boundary(self):
        # constant signal, long enough for the detector to fully settle
        block = [[0.5] * 9600, [0.25] * 9600]
        proc = make_processor(smoother=SmootherConfig(0.0, 0.0))
        proc.process_block([ch[:] for ch in block])
        proc.update_curve(self.new_curve, fade_samples=128)
        out = proc.process_block([ch[:] for ch in block])
        # first sample after the switch point: 1/128 of the way to the
        # new curve -> still close to the old static gain
        x = block[0][0]
        lvl = amp_to_db(abs(x))
        g_old = db_to_amp(make_curve().gain_db(lvl))
        g_new = db_to_amp(self.new_curve.gain_db(lvl))
        g0 = out[0][0] / x
        self.assertLess(abs(g0 - g_old), abs(g_new - g_old) / 64.0 + 1e-12)

    def test_envelope_state_retained_on_switch(self):
        ref = make_processor()          # never switches: detector reference
        proc = make_processor()
        ref.process_block([ch[:] for ch in self.block])
        proc.process_block([ch[:] for ch in self.block])
        proc.update_curve(self.new_curve, fade_samples=64,
                          envelope_policy=EnvelopePolicy.RETAIN)
        self.assertEqual(ref.get_state()["detectors"],
                         proc.get_state()["detectors"])
        self.assertEqual(ref.get_state()["smoothers"],
                         proc.get_state()["smoothers"])

    def test_envelope_state_reset_policy(self):
        proc = make_processor()
        proc.process_block([ch[:] for ch in self.block])
        smoother_state = proc.get_state()["smoothers"]
        proc.update_curve(self.new_curve, fade_samples=64,
                          envelope_policy=EnvelopePolicy.RESET)
        st = proc.get_state()
        # detectors re-initialised ...
        self.assertTrue(all(s == 0.0 for s in st["detectors"]))
        # ... but smoother gain state retained: applied gain cannot jump
        self.assertEqual(smoother_state, st["smoothers"])


class TestCascade(unittest.TestCase):
    def test_cascade_differs_from_merged_parameters(self):
        comp = compile_curve([Segment(-20.0, 4.0)])
        expn = compile_curve([Segment(-40.0, 2.0, kind=EXPAND)])
        merged = compile_curve([Segment(-20.0, 2.0)])  # naive param merge
        worst = 0.0
        for i in range(1000):
            x = -100.0 + i * 0.12
            cascaded = expn.output_db(comp.output_db(x))
            worst = max(worst, abs(cascaded - merged.output_db(x)))
        self.assertGreater(worst, 1.0)  # dB: clearly not equivalent


class TestSilence(unittest.TestCase):
    def test_silence_no_inf_no_nan(self):
        proc = make_processor()
        out = proc.process_block([silence(4096), silence(4096)])
        for ch in out:
            for v in ch:
                self.assertTrue(math.isfinite(v))
                self.assertEqual(v, 0.0)
        st = proc.get_state()
        self.assertTrue(all(math.isfinite(s) for s in st["smoothers"]))


if __name__ == "__main__":
    unittest.main()
