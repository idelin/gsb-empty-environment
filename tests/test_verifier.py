"""unittest suite for the streamable-filter verifier (stdlib only)."""
from __future__ import annotations

import json
import math
import os
import random
import tempfile
import unittest

from verifier import (FilterSpec, verify, StreamingFIR, design_lowpass,
                      generate_fixtures)
from verifier import bad_impls
from verifier.dsp import fft, ifft, dft_naive
from verifier.fir_bounds import (certify_fir, fir_response,
                                 lipschitz_constant)
from verifier.metrics import quadrature_tone, estimate_delay
from verifier.shrink import shrink_chunk_counterexample
from verifier.signals import tone, snap_to_bin
from verifier.streaming import (run_streaming, find_chunk_counterexample,
                                find_shift_counterexample, ChunkCounterexample)
from verifier.windows import hann, amplitude_spectrum

FS = 48000.0


def small_spec():
    return FilterSpec(sample_rate=FS, passband=(0.0, 8000.0),
                      stopband=(14000.0, 24000.0), stopband_atten_db=30.0)


def small_fixtures(spec):
    return generate_fixtures(FS, spec.passband, spec.stopband,
                             n_impulse=1024, n_chirp=4096, n_multi=2048,
                             n_seg=1024, n_probe=512)


class TestFFT(unittest.TestCase):
    def test_fft_matches_naive(self):
        rng = random.Random(7)
        x = [complex(rng.uniform(-1, 1), rng.uniform(-1, 1)) for _ in range(64)]
        got = fft(x)
        want = dft_naive(x)
        err = max(abs(a - b) for a, b in zip(got, want))
        self.assertLess(err, 1e-9)

    def test_ifft_roundtrip(self):
        rng = random.Random(11)
        x = [rng.uniform(-1, 1) for _ in range(128)]
        back = ifft(fft(x))
        err = max(abs(a - b) for a, b in zip(back, x))
        self.assertLess(err, 1e-12)


class TestNormalization(unittest.TestCase):
    def test_windowed_unity_tone_reads_unity(self):
        n = 1024
        f = snap_to_bin(1000.7, n, FS)
        x = tone(f, 1.0, n, FS)
        _, amps = amplitude_spectrum(x, FS, hann(n))
        k = int(round(f * n / FS))
        self.assertAlmostEqual(amps[k], 1.0, places=3,
                               msg="coherent-gain normalisation broken: "
                                   "unity gain misread as attenuation")

    def test_quadrature_recovers_amplitude_and_phase(self):
        n = 1024
        f = snap_to_bin(3000.0, n, FS)
        x = tone(f, 0.4, n, FS, phase=0.7)
        amp, phase = quadrature_tone(x, f, FS)
        self.assertAlmostEqual(amp, 0.4, places=12)
        self.assertAlmostEqual(phase, 0.7, places=9)

    def test_identity_filter_measures_zero_db(self):
        spec = small_spec()
        fx = small_fixtures(spec)
        x = fx.signals["passband_multitone"]
        y = run_streaming(StreamingFIR([1.0]), x, [])
        n_seg = fx.params["analysis_segment"]
        start = len(x) - n_seg
        for f in fx.tone_sets["passband"]:
            a_y, _ = quadrature_tone(y, f, FS, start, n_seg)
            a_x, _ = quadrature_tone(x, f, FS, start, n_seg)
            self.assertAlmostEqual(a_y / a_x, 1.0, places=9)


class TestNyquist(unittest.TestCase):
    def test_tone_one_bin_below_nyquist(self):
        n = 1024
        f = FS / 2.0 - FS / n
        x = tone(f, 0.6, n, FS)
        amp, _ = quadrature_tone(x, f, FS)
        self.assertAlmostEqual(amp, 0.6, places=9)

    def test_exact_nyquist_cosine_only(self):
        n = 1024
        x = tone(FS / 2.0, 0.5, n, FS)
        amp, phase = quadrature_tone(x, FS / 2.0, FS)
        self.assertAlmostEqual(amp, 0.5, places=12)
        self.assertIsNone(phase)

    def test_fixture_tones_respect_guard(self):
        spec = small_spec()
        fx = small_fixtures(spec)
        for f in fx.tone_sets["stopband"]:
            self.assertLessEqual(f, FS / 2.0 - spec.nyquist_guard_hz + 1e-9)


class TestDelay(unittest.TestCase):
    def test_group_delay_recovered(self):
        h = design_lowpass(65, 11000.0, FS)
        x = [0.0] * 1024
        x[0] = 1.0
        y = run_streaming(StreamingFIR(h), x, [])
        d = estimate_delay(x, y)
        self.assertAlmostEqual(d, 32.0, places=6)


class TestStreamingChecks(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.h = design_lowpass(65, 11000.0, FS)
        cls.probe = generate_fixtures(
            FS, (0.0, 8000.0), (14000.0, 24000.0),
            n_impulse=256, n_chirp=1024, n_multi=1024, n_seg=512,
            n_probe=512).signals["stream_probe"]

    def test_good_impl_is_chunk_invariant(self):
        cex, worst = find_chunk_counterexample(
            lambda: StreamingFIR(self.h), self.probe, 8, 99, 1e-9)
        self.assertIsNone(cex)
        self.assertLessEqual(worst, 1e-9)

    def test_state_reset_detected_shrunk_and_replayable(self):
        cex, _ = find_chunk_counterexample(
            lambda: bad_impls.StateResetFIR(self.h), self.probe, 8, 99, 1e-9)
        self.assertIsNotNone(cex)
        shrunk = shrink_chunk_counterexample(
            lambda: bad_impls.StateResetFIR(self.h), cex)
        self.assertLessEqual(len(shrunk.input), 4)
        self.assertEqual(len(shrunk.boundaries), 1)
        fails, diff = shrunk.still_fails(lambda: bad_impls.StateResetFIR(self.h))
        self.assertTrue(fails)
        self.assertGreater(diff, 1e-9)
        ok, _ = shrunk.still_fails(lambda: StreamingFIR(self.h))
        self.assertFalse(ok, "counterexample must not fire on the good impl")
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "cex.json")
            shrunk.save(path)
            loaded = ChunkCounterexample.load(path)
            self.assertEqual(loaded.input, shrunk.input)
            self.assertEqual(loaded.boundaries, shrunk.boundaries)
            fails2, _ = loaded.still_fails(
                lambda: bad_impls.StateResetFIR(self.h))
            self.assertTrue(fails2)

    def test_chunk_delay_detected(self):
        cex, _ = find_chunk_counterexample(
            lambda: bad_impls.ChunkDelayFIR(self.h), self.probe, 8, 99, 1e-9)
        self.assertIsNotNone(cex)

    def test_shift_misalignment_detected_at_minimal_shift(self):
        sce, _ = find_shift_counterexample(
            lambda: bad_impls.AbsoluteIndexFIR(self.h, period=97),
            self.probe, 8, 1e-9)
        self.assertIsNotNone(sce)
        self.assertEqual(sce.shift, 1)
        fails, _ = sce.still_fails(
            lambda: bad_impls.AbsoluteIndexFIR(self.h, period=97))
        self.assertTrue(fails)

    def test_good_impl_is_shift_invariant(self):
        sce, worst = find_shift_counterexample(
            lambda: StreamingFIR(self.h), self.probe, 8, 1e-9)
        self.assertIsNone(sce)
        self.assertLessEqual(worst, 1e-9)


class TestFIRBounds(unittest.TestCase):
    def test_lipschitz_bound_is_conservative(self):
        rng = random.Random(3)
        h = [rng.uniform(-0.5, 0.5) for _ in range(9)]
        L = lipschitz_constant(h, FS)
        f_lo, f_hi = 5000.0, 5100.0
        v0 = abs(fir_response(h, f_lo, FS))
        v1 = abs(fir_response(h, f_hi, FS))
        bound = max(v0, v1) + L * (f_hi - f_lo) / 2.0
        dense_max = max(abs(fir_response(h, f_lo + i * 1.0, FS))
                        for i in range(101))
        self.assertLessEqual(dense_max, bound + 1e-12)

    def test_good_filter_certified(self):
        spec = small_spec()
        h = design_lowpass(65, 11000.0, FS)
        certs = certify_fir(h, spec)
        self.assertEqual(certs["passband"].status, "certified")
        self.assertEqual(certs["stopband"].status, "certified")

    def test_underspec_filter_violated(self):
        spec = small_spec()
        bad = bad_impls.make_underspec_stopband(spec)
        certs = certify_fir(bad.h, spec)
        self.assertEqual(certs["stopband"].status, "violated")


class TestVerifierEndToEnd(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.spec = small_spec()
        cls.fx = small_fixtures(cls.spec)
        cls.h = design_lowpass(65, 11000.0, FS)

    def run_verify(self, factory, **kw):
        kw.setdefault("chunk_trials", 8)
        return verify(factory, self.spec, fixtures=self.fx, **kw)

    def test_good_impl_passes_all_checks(self):
        rep = self.run_verify(lambda: StreamingFIR(self.h),
                              fir_coefficients=self.h)
        failed = [c.name for c in rep.checks if not c.passed]
        self.assertEqual(failed, [], f"good impl failed: {failed}")
        self.assertTrue(rep.overall_passed)

    def test_report_has_methods_and_sample_counts(self):
        rep = self.run_verify(lambda: StreamingFIR(self.h))
        for c in rep.checks:
            self.assertGreater(c.n_samples, 0, c.name)
            self.assertTrue(c.method, c.name)
        json.dumps(rep.to_dict())

    def test_resolution_reported_separately_from_error(self):
        rep = self.run_verify(lambda: StreamingFIR(self.h))
        swept = next(c for c in rep.checks if c.name == "swept_response")
        self.assertIn("passband_ripple_db", swept.measured)
        self.assertIn("grid_spacing_hz", swept.measured)
        self.assertIn("NOT certified", swept.detail)

    def test_bad_impls_detected(self):
        h = self.h
        cases = {
            "state_reset": (lambda: bad_impls.StateResetFIR(h),
                            "chunk_invariance"),
            "chunk_delay": (lambda: bad_impls.ChunkDelayFIR(h),
                            "chunk_invariance"),
            "absolute_index": (lambda: bad_impls.AbsoluteIndexFIR(h, period=97),
                               "shift_invariance"),
            "gain_error": (lambda: bad_impls.GainErrorFIR(h, 0.5),
                           "multitone_passband"),
            "latency_liar": (lambda: bad_impls.LatencyLiarFIR(h),
                             "declared_latency"),
            "underspec_stopband": (lambda: bad_impls.make_underspec_stopband(self.spec),
                                   "stopband_leakage"),
            "ripply_passband": (lambda: bad_impls.make_ripply_passband(self.spec),
                                "swept_response"),
        }
        for name, (factory, check) in cases.items():
            with self.subTest(bad_impl=name):
                rep = self.run_verify(factory)
                self.assertFalse(rep.overall_passed)
                failed = [c.name for c in rep.checks if not c.passed]
                self.assertIn(check, failed)

    def test_artifacts_are_replayable(self):
        h = self.h
        with tempfile.TemporaryDirectory() as tmp:
            rep = verify(lambda: bad_impls.StateResetFIR(h), self.spec,
                         fixtures=self.fx, chunk_trials=8, artifact_dir=tmp)
            self.assertFalse(rep.overall_passed)
            path = os.path.join(tmp, "chunk_counterexample.json")
            self.assertTrue(os.path.exists(path))
            cex = ChunkCounterexample.load(path)
            fails, _ = cex.still_fails(lambda: bad_impls.StateResetFIR(h))
            self.assertTrue(fails)
            ok, _ = cex.still_fails(lambda: StreamingFIR(h))
            self.assertFalse(ok)


class TestFixtures(unittest.TestCase):
    def test_save_load_roundtrip_regenerates_identical_signals(self):
        spec = small_spec()
        fx = small_fixtures(spec)
        from verifier.fixtures import FixtureSet
        with tempfile.TemporaryDirectory() as tmp:
            path = os.path.join(tmp, "fixtures.json")
            fx.save(path)
            loaded = FixtureSet.load(path)
        self.assertEqual(loaded.signals, fx.signals)
        self.assertEqual(loaded.tone_sets, fx.tone_sets)
        self.assertEqual(loaded.sample_rate, fx.sample_rate)


if __name__ == "__main__":
    unittest.main()
