"""unittest suite for the multiband compressor/limiter pipeline.

Covers: bypass recombination tolerance, group-delay compensation, linked
multi-channel gain with independent audio, block-boundary peaks, threshold
hot-update inside the lookahead region, cancel/restore without missed
limiting or double delay, NaN marking/propagation, gain change rates,
band separation/phase, and declared latency.
"""

import math
import unittest

from dsp_crossover import Crossover
from dsp_dynamics import EnvelopeFollower
from dsp_limiter import LookaheadLimiter
from fixtures import (
    SAMPLE_RATE, TOL_RECON, TOL_PEAK, TOL_GAIN_RATE,
    impulse, make_processor, multitone, run_blocks, three_band_bands,
    with_unknown,
)


def lin(db):
    return 10.0 ** (db / 20.0)


def goertzel_amp(x, freq, sr):
    w = 2.0 * math.pi * freq / sr
    cw = 2.0 * math.cos(w)
    s1 = s2 = 0.0
    for v in x:
        s0 = v + cw * s1 - s2
        s2, s1 = s1, s0
    return math.sqrt(s1 * s1 + s2 * s2 - cw * s1 * s2) * 2.0 / len(x)


def max_abs(xs):
    return max(abs(v) for v in xs)


class CrossoverTests(unittest.TestCase):
    def test_group_delays_differ_and_recombine_exactly(self):
        bands = three_band_bands()
        gds = [b.group_delay for b in bands]
        self.assertEqual(gds, [31, 15, 31])
        xo = Crossover(bands)
        self.assertEqual(xo.latency_samples, 31)
        pos, n = 100, 512
        out_bands = xo.process(impulse(n, pos))
        total = [sum(bs) for bs in zip(*out_bands)]
        for i, v in enumerate(total):
            expect = 1.0 if i == pos + 31 else 0.0
            self.assertAlmostEqual(v, expect, delta=TOL_RECON,
                                   msg="recombined impulse at %d" % i)

    def test_multiband_separation_and_phase(self):
        bands = three_band_bands(n_low=255, n_mid=127)
        xo = Crossover(bands)
        tones = [100.0, 1500.0, 9000.0]
        n = 4096
        x = multitone(n, tones, [0.3, 0.3, 0.3])
        low, mid, high = xo.process(x)
        amps = [
            [goertzel_amp(b, f, SAMPLE_RATE) for f in tones]
            for b in (low, mid, high)
        ]
        for t in range(3):
            own = amps[t][t]
            for other in range(3):
                if other != t:
                    self.assertGreater(
                        own, 2.0 * amps[other][t],
                        "tone %d leaks into band %d" % (t, other))
        gd = xo.latency_samples
        total = [sum(bs) for bs in zip(low, mid, high)]
        err = max_abs([total[i] - x[i - gd] for i in range(gd, n)])
        self.assertLess(err, TOL_RECON, "band sum must be a pure delay")


class BypassPipelineTests(unittest.TestCase):
    def test_bypass_reconstructs_reference_within_tolerance(self):
        proc = make_processor(ratio=1.0, limiter_threshold_db=0.0)
        n = 4096
        left = multitone(n, [100.0, 1500.0, 9000.0], [0.2, 0.2, 0.2])
        right = multitone(n, [150.0, 900.0, 7000.0], [0.15, 0.15, 0.15])
        outs = run_blocks(proc, [left, right], 512)
        lat = proc.latency_samples
        self.assertEqual(lat, 31 + 240)
        self.assertEqual(proc.stage_latencies(),
                         {"crossover": 31, "compressors": 0, "limiter": 240})
        for ch, ref in enumerate((left, right)):
            err = max_abs([outs[ch][i] - ref[i - lat] for i in range(lat, n)])
            self.assertLess(err, TOL_RECON,
                            "channel %d bypass reconstruction error" % ch)

    def test_declared_latency_matches_measured_impulse_delay(self):
        proc = make_processor(channels=1, ratio=1.0, limiter_threshold_db=0.0)
        n, pos = 4096, 1000
        outs = run_blocks(proc, [impulse(n, pos, 0.5)], 512)
        got = max(range(n), key=lambda i: abs(outs[0][i]))
        self.assertEqual(got, pos + proc.latency_samples)


class LinkedGainTests(unittest.TestCase):
    def test_right_only_spike_drives_shared_gain_left_audio_independent(self):
        proc = make_processor()
        n, pos = 4096, 1000
        left = multitone(n, [100.0], [0.05])
        right = impulse(n, pos, 0.9)
        burst = multitone(480, [100.0], [0.8])
        right[2000:2480] = burst
        hf = multitone(480, [6000.0], [0.8])
        right[3000:3480] = hf
        outs = run_blocks(proc, [left, right], 512)
        lat = proc.latency_samples
        for name in ("band:low", "band:high", "limiter"):
            self.assertLess(min(proc.trajectories[name]), 1.0,
                            name + " must react to the right-only spike")
        right_peak = max_abs(outs[1])
        self.assertLess(right_peak, lin(-6.0) * (1.0 + TOL_PEAK))
        center = pos + lat
        ducked = max_abs(outs[0][center - 50:center + 50])
        clean = max_abs(outs[0][lat + 100:lat + 400])
        self.assertLess(ducked, 0.8 * clean,
                        "left must be ducked by the shared limiter gain")
        ducked2 = max_abs(outs[0][2000 + lat + 300:2000 + lat + 480])
        self.assertLess(ducked2, 0.8 * clean,
                        "left must be ducked by the shared low-band gain")
        self.assertGreater(clean, 0.04,
                           "left audio must survive (independent audio path)")


class LimiterEdgeTests(unittest.TestCase):
    def _check_no_clip_and_invariant(self, proc, ref, outs):
        lat = proc.latency_samples
        gains = proc.trajectories["limiter"]
        thrs = proc.trajectories["limiter_threshold_db"]
        self.assertEqual(len(gains), len(outs[0]))
        for i in range(lat, len(outs[0])):
            if math.isnan(gains[i]):
                continue
            self.assertLessEqual(abs(outs[0][i]), lin(thrs[i]) + 1e-9,
                                 "sample %d exceeds smoothed threshold" % i)
            self.assertAlmostEqual(outs[0][i], ref[i - lat] * gains[i],
                                   delta=1e-9,
                                   msg="waveform shaped by gain, not clipped")

    def test_peak_exactly_at_block_boundary_is_limited(self):
        proc = make_processor(channels=1, ratio=1.0)
        block = 256
        n, pos = 4096, 2 * block
        x = impulse(n, pos, 0.9)
        outs = run_blocks(proc, [x], block)
        lat = proc.latency_samples
        peak = max_abs(outs[0])
        self.assertAlmostEqual(peak, lin(-6.0), delta=1e-6)
        self.assertEqual(outs[0].index(peak), pos + lat)
        self.assertEqual(len(proc.evidence), 1)
        self.assertAlmostEqual(proc.evidence[0]["peak"], 0.9, delta=1e-9)
        self._check_no_clip_and_invariant(proc, x, outs)

    def test_threshold_change_mid_lookahead_still_limits(self):
        proc = make_processor(channels=1, ratio=1.0, threshold_smooth_ms=2.0)
        block, n, pos = 256, 2048, 255
        x = impulse(n, pos, 0.9)
        out = []
        out += proc.process_block([x[:block]])[0]
        proc.limiter.set_threshold_db(-20.0)  # spike is inside lookahead now
        for start in range(block, n, block):
            out += proc.process_block([x[start:start + block]])[0]
        out += proc.flush()[0]
        lat = proc.latency_samples
        self.assertEqual(proc.latency_samples, 271, "latency must not change")
        peak = max_abs(out)
        self.assertAlmostEqual(peak, lin(-20.0), delta=1e-6,
                               msg="in-flight peak must follow the new threshold")
        self.assertEqual(out.index(peak), pos + lat,
                         "no double delay after hot update")
        gains = proc.trajectories["limiter"]
        thrs = proc.trajectories["limiter_threshold_db"]
        self.assertTrue(any(t < -6.0 for t in thrs),
                        "threshold trajectory must show the hot update")
        for i in range(len(out)):
            self.assertLessEqual(abs(out[i]), lin(thrs[i]) + 1e-9)

    def test_cancel_restore_no_missed_limiting_no_double_delay(self):
        proc = make_processor(channels=1, ratio=1.0)
        block, n = 512, 52 * 512
        pa, pb, pc = 2000, 10000, 18000
        x = [0.0] * n
        for p in (pa, pb, pc):
            x[p] = 0.9
        out = []
        pos = 0

        def feed(count):
            nonlocal pos
            for _ in range(count):
                out.extend(proc.process_block([x[pos:pos + block]])[0])
                pos += block

        feed(9)                        # spike A, limiter enabled
        proc.limiter.set_enabled(False)
        feed(15)                       # spike B, limiter cancelled
        proc.limiter.set_enabled(True)
        feed(52 - 24)                  # spike C, limiter restored
        out.extend(proc.flush()[0])
        lat = proc.latency_samples
        thr = lin(-6.0)
        for p in (pa, pb, pc):
            self.assertAlmostEqual(out[p + lat - 1], 0.0, delta=1e-12)
            self.assertAlmostEqual(out[p + lat + 1], 0.0, delta=1e-12)
        self.assertAlmostEqual(out[pa + lat], thr, delta=1e-6)
        self.assertGreater(out[pb + lat], 0.85,
                           "cancelled limiter must pass the spike")
        self.assertAlmostEqual(out[pc + lat], thr, delta=1e-6,
                               msg="restored limiter must limit again")
        self.assertEqual(len(proc.evidence), 3,
                         "cancelled segment must still leave evidence")
        self.assertEqual(proc.evidence[1]["min_gain"], 1.0)


class UnknownSampleTests(unittest.TestCase):
    def test_nan_is_marked_and_propagated_then_recovers(self):
        proc = make_processor(channels=1, attack_ms=1.0, release_ms=5.0)
        n, k = 8192, 1000
        x = with_unknown(multitone(n, [300.0], [0.3]), [k])
        outs = run_blocks(proc, [x], 512)
        out = outs[0]
        nan_idx = [i for i, v in enumerate(out) if math.isnan(v)]
        self.assertTrue(nan_idx, "unknown sample must be marked on output")
        self.assertEqual(nan_idx[0], k)
        self.assertEqual(nan_idx[-1], k + 62 + 1200 + 240)
        self.assertEqual(nan_idx, list(range(nan_idx[0], nan_idx[-1] + 1)),
                         "unknown region must be contiguous")
        self.assertFalse(math.isnan(out[k - 1]))
        self.assertFalse(math.isnan(out[nan_idx[-1] + 1]))
        self.assertFalse(any(math.isnan(v) for v in out[nan_idx[-1] + 1:]),
                         "pipeline must recover after the taint window")
        traj = proc.trajectories["limiter"]
        self.assertTrue(math.isnan(traj[k]))
        self.assertFalse(math.isnan(traj[nan_idx[-1] + 1]))


class GainRateTests(unittest.TestCase):
    def test_envelope_follower_attack_release_curve(self):
        sr = SAMPLE_RATE
        fol = EnvelopeFollower(sr, attack_ms=10.0, release_ms=40.0)
        att = math.exp(-1.0 / (sr * 0.010))
        rel = math.exp(-1.0 / (sr * 0.040))
        for _ in range(16):
            fol.process(0.0)
        envs = [fol.process(1.0) for _ in range(480)]
        for k in (0, 100, 479):
            self.assertAlmostEqual(envs[k], 1.0 - att ** (k + 1), delta=1e-12)
        envs2 = [fol.process(0.0) for _ in range(480)]
        for k in (0, 100, 479):
            self.assertAlmostEqual(envs2[k], envs[-1] * rel ** (k + 1),
                                   delta=1e-12)

    def test_limiter_attack_instant_and_release_rate(self):
        sr = SAMPLE_RATE
        lim = LookaheadLimiter(sr, channels=1, lookahead_ms=5.0,
                               threshold_db=-6.0, release_ms=50.0)
        loud = multitone(4800, [1000.0], [0.9], sr)
        x = loud + [0.0] * 4800
        gains, g_reqs = [], []
        for start in range(0, len(x), 480):
            _, g, _, r = lim.process_block([x[start:start + 480]])
            gains += g
            g_reqs += r
        _, g, _, r = lim.flush()
        gains += g
        g_reqs += r
        rel = math.exp(-1.0 / (sr * 0.050))
        expect_min = lin(-6.0) / 0.9
        self.assertAlmostEqual(min(gains), expect_min, delta=1e-9)
        first = next(i for i, r0 in enumerate(g_reqs) if r0 < 0.99)
        self.assertEqual(gains[first], g_reqs[first],
                         "attack must be instant (lookahead covers it)")
        checked = 0
        for i in range(len(gains) - 1):
            if g_reqs[i + 1] == 1.0 and gains[i] < 0.999:
                expect = 1.0 - (1.0 - gains[i]) * rel
                self.assertAlmostEqual(gains[i + 1], expect,
                                       delta=TOL_GAIN_RATE)
                checked += 1
        self.assertGreater(checked, 1000, "need a long pure-release region")

    def test_compressor_steady_state_and_attack_speed(self):
        proc = make_processor(channels=1, comp_threshold_db=-20.0, ratio=4.0,
                              attack_ms=5.0, release_ms=80.0,
                              limiter_threshold_db=0.0)
        n, step_at = 4800, 480
        x = [0.0] * step_at + [0.5] * (n - step_at)
        run_blocks(proc, [x], 480)
        traj = proc.trajectories["band:low"]
        steady = sum(traj[2400:4800]) / 2400.0
        self.assertAlmostEqual(steady, (0.1 / 0.5) ** 0.75, delta=0.02)
        gd = proc.stage_latencies()["crossover"]
        first = next(i for i, g in enumerate(traj) if g < 0.35)
        self.assertLess(first - step_at - gd, 600, "compressor attack too slow")
        self.assertGreater(min(proc.trajectories["band:mid"][2400:]), 0.99,
                           "DC step must not keep the mid band engaged")
        self.assertEqual(min(proc.trajectories["limiter"]), 1.0)


if __name__ == "__main__":
    unittest.main()
