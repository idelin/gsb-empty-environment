"""Runnable unittest suite for the specband package (Python 3.11, stdlib only)."""

import time
import unittest

from specband import (
    DECLARED_FRAME_LATENCY_MS,
    NUMERICAL_TOLERANCE,
    Band,
    BandSet,
    Engine,
    SpectrumFrame,
    integrate,
    power_spectrum,
)
from specband import fixtures

TOL = NUMERICAL_TOLERANCE
FS = fixtures.SAMPLE_RATE
N = fixtures.N_SAMPLES
DF = FS / N  # 2.0 Hz
N_BINS = N // 2 + 1  # 17
FULL_SPAN = N_BINS * DF  # 34.0 Hz


def fixture_spectrum(samples):
    return power_spectrum(samples)


class EnergyConservationTest(unittest.TestCase):
    """Non-overlapping full coverage must conserve energy."""

    def test_energy_conservation_full_coverage(self):
        spectrum = fixture_spectrum(fixtures.SAMPLES_CALM)
        partition = BandSet("partition-v1", (
            Band("low", 0.0, 10.0),
            Band("mid", 10.0, 20.0),
            Band("high", 20.0, FULL_SPAN),
        ))
        result = integrate(spectrum, sample_rate=FS, n_samples=N, band_set=partition)
        band_sum = sum(r.absolute_energy for r in result.reports)
        self.assertAlmostEqual(band_sum, result.total_energy, delta=TOL)
        # No overlap anywhere: repeated coverage and shared energy vanish.
        self.assertAlmostEqual(result.shared_energy, 0.0, delta=TOL)
        for r in result.reports:
            self.assertAlmostEqual(r.repeated_coverage_energy, 0.0, delta=TOL)
            self.assertIsNotNone(r.relative_energy)
        # Relatives of a non-overlapping full partition do sum to 1.
        self.assertAlmostEqual(
            sum(r.relative_energy for r in result.reports), 1.0, delta=TOL)
        # Hand-check against analytic sine power (A**2 / 2 per component):
        # 8 Hz amp 1.0 -> bin 4 -> 0.5 ; 16 Hz amp 0.5 -> bin 8 -> 0.125.
        self.assertAlmostEqual(result.total_energy, 0.625, delta=TOL)
        self.assertAlmostEqual(
            result.report("low").absolute_energy, 0.5, delta=TOL)
        self.assertAlmostEqual(
            result.report("mid").absolute_energy, 0.125, delta=TOL)

    def test_declared_latency_budget(self):
        spectrum = fixture_spectrum(fixtures.SAMPLES_BRIGHT)
        band_set = BandSet("latency-v1", (Band("all", 0.0, FULL_SPAN),))
        start = time.perf_counter()
        integrate(spectrum, sample_rate=FS, n_samples=N, band_set=band_set)
        elapsed_ms = (time.perf_counter() - start) * 1000.0
        print(f"\n[latency] one frame integrated in {elapsed_ms:.3f} ms "
              f"(declared budget {DECLARED_FRAME_LATENCY_MS} ms)")
        # Generous guard (100x) so slow CI machines do not flake.
        self.assertLess(elapsed_ms, 100.0 * DECLARED_FRAME_LATENCY_MS)


class PartialWeightTest(unittest.TestCase):
    """Cross-boundary bins get partial weights; widening is local."""

    def spectrum(self):
        spectrum = [0.0] * N_BINS
        spectrum[1] = 2.0
        spectrum[2] = 4.0
        spectrum[3] = 8.0
        spectrum[12] = 3.0
        spectrum[15] = 5.0
        return spectrum

    def test_partial_weights_boundary_bins(self):
        # df = 2 Hz; band [3, 7) cuts bin 1 ([2,4)) and bin 3 ([6,8)).
        band_set = BandSet("pw-v1", (Band("cut", 3.0, 7.0),))
        result = integrate(self.spectrum(), sample_rate=FS, n_samples=N,
                           band_set=band_set)
        # 0.5*2.0 (bin1) + 1.0*4.0 (bin2) + 0.5*8.0 (bin3) = 9.0
        self.assertAlmostEqual(
            result.report("cut").absolute_energy, 9.0, delta=TOL)

    def test_band_widening_only_affects_related(self):
        v1 = BandSet("widen-v1", (
            Band("A", 3.0, 7.0),
            Band("B", 20.0, 28.0),
            Band("C", 30.0, FULL_SPAN),
        ))
        v2 = BandSet("widen-v2", (
            Band("A", 3.0, 11.0),  # only A widened
            Band("B", 20.0, 28.0),
            Band("C", 30.0, FULL_SPAN),
        ))
        spectrum = self.spectrum()
        spectrum[4] = 6.0
        r1 = integrate(spectrum, sample_rate=FS, n_samples=N, band_set=v1)
        r2 = integrate(spectrum, sample_rate=FS, n_samples=N, band_set=v2)
        # A = [3,7): 0.5*2 + 4 + 0.5*8 = 9.0.
        self.assertAlmostEqual(r1.report("A").absolute_energy, 9.0, delta=TOL)
        # A widened to [3,11): bin 3 now fully covered (+4.0) and bin 4
        # fully covered (+6.0) -> 19.0.
        self.assertAlmostEqual(r2.report("A").absolute_energy, 19.0, delta=TOL)
        # Unrelated bands bit-identical across the version switch.
        for name in ("B", "C"):
            self.assertEqual(
                r1.report(name).absolute_energy,
                r2.report(name).absolute_energy)
            self.assertEqual(
                r1.report(name).relative_energy,
                r2.report(name).relative_energy)
        self.assertEqual(r1.total_energy, r2.total_energy)

    def test_zero_total_energy_relative_unknown(self):
        spectrum = fixture_spectrum(fixtures.SAMPLES_SILENT)
        band_set = BandSet("zero-v1", (Band("all", 0.0, FULL_SPAN),))
        result = integrate(spectrum, sample_rate=FS, n_samples=N, band_set=band_set)
        self.assertEqual(result.total_energy, 0.0)
        report = result.report("all")
        self.assertEqual(report.absolute_energy, 0.0)
        self.assertIsNone(report.relative_energy)  # unknown, not 0 or NaN


class ManualGridTest(unittest.TestCase):
    """Small-grid hand integration; overlap must NOT be normalised to 1."""

    def test_small_grid_manual_integration(self):
        # n_samples=4, fs=4 Hz -> df=1 Hz, 3 bins, span [0,3).
        spectrum = [1.0, 2.0, 4.0]
        band_set = BandSet("grid-v1", (
            Band("P", 0.5, 2.5),  # weights 0.5, 1.0, 0.5
            Band("Q", 1.5, 3.0),  # weights 0.0, 0.5, 1.0
        ))
        result = integrate(spectrum, sample_rate=4.0, n_samples=4,
                           band_set=band_set)
        p = result.report("P")
        q = result.report("Q")
        self.assertAlmostEqual(p.absolute_energy, 0.5 * 1 + 2 + 0.5 * 4, delta=TOL)
        self.assertAlmostEqual(q.absolute_energy, 0.5 * 2 + 4, delta=TOL)
        self.assertAlmostEqual(result.total_energy, 7.0, delta=TOL)
        # Relative energies are against the spectrum total, so overlapping
        # bands legitimately sum to more than 1 -- never renormalised.
        self.assertAlmostEqual(p.relative_energy, 4.5 / 7.0, delta=TOL)
        self.assertAlmostEqual(q.relative_energy, 5.0 / 7.0, delta=TOL)
        self.assertGreater(p.relative_energy + q.relative_energy, 1.0)
        self.assertGreater(p.absolute_energy + q.absolute_energy,
                           result.total_energy)
        # Repeated coverage: bins 1 and 2 are doubly covered.
        self.assertAlmostEqual(p.repeated_coverage_energy, 2.0 + 0.5 * 4.0,
                               delta=TOL)
        self.assertAlmostEqual(q.repeated_coverage_energy, 0.5 * 2.0 + 4.0,
                               delta=TOL)
        self.assertAlmostEqual(result.shared_energy, 2.0 + 4.0, delta=TOL)


class VersionInterleavingTest(unittest.TestCase):
    """Frame corrections interleaved with band-set switches must not mix."""

    def test_correction_across_band_switch_no_mixing(self):
        bands_a = BandSet("bands-A", (
            Band("low", 0.0, 16.0),
            Band("high", 16.0, FULL_SPAN),
        ))
        bands_b = BandSet("bands-B", (
            Band("low", 0.0, 10.0),
            Band("mid", 10.0, 24.0),
            Band("high", 24.0, FULL_SPAN),
        ))
        engine = Engine(bands_a)
        frame_v1 = SpectrumFrame(0, 1, fixtures.SAMPLES_CALM, FS)
        r1, c1 = engine.ingest(frame_v1)
        self.assertIsNone(c1)
        self.assertEqual((r1.frame_version, r1.band_version), (1, "bands-A"))

        engine.switch_band_set(bands_b)
        frame_v2 = SpectrumFrame(0, 2, fixtures.SAMPLES_CALM_V2, FS)
        r2, correction = engine.ingest(frame_v2)

        # The correction is computed entirely under bands-B.
        self.assertIsNotNone(correction)
        self.assertEqual(correction.band_version, "bands-B")
        self.assertEqual(correction.old_frame_version, 1)
        self.assertEqual(correction.new_frame_version, 2)
        self.assertEqual(
            correction.provenance["band_version_used_for_both_sides"], "bands-B")
        self.assertTrue(correction.provenance["old_side_recomputed"])

        # Deltas match an independent recomputation under bands-B.
        old_under_b = integrate(power_spectrum(fixtures.SAMPLES_CALM),
                                sample_rate=FS, n_samples=N, band_set=bands_b)
        new_under_b = integrate(power_spectrum(fixtures.SAMPLES_CALM_V2),
                                sample_rate=FS, n_samples=N, band_set=bands_b)
        for name in ("low", "mid", "high"):
            expected = (new_under_b.report(name).absolute_energy
                        - old_under_b.report(name).absolute_energy)
            self.assertAlmostEqual(correction.deltas[name], expected, delta=TOL)
        # The 8 Hz component grew 1.0 -> 1.1 in amplitude: energy in the
        # low band grows by (1.1**2 - 1.0**2) / 2 = 0.105.
        self.assertAlmostEqual(correction.deltas["low"], 0.105, delta=TOL)

        # History keeps both versions, each tagged with its own band set:
        # no result ever mixes frame v2 with bands-A.
        self.assertEqual(
            [(r.frame_version, r.band_version) for r in engine.history],
            [(1, "bands-A"), (2, "bands-B")])
        self.assertEqual((r2.frame_version, r2.band_version), (2, "bands-B"))

    def test_version_regression_rejected(self):
        engine = Engine(BandSet("bands-A", (Band("all", 0.0, FULL_SPAN),)))
        engine.ingest(SpectrumFrame(0, 2, fixtures.SAMPLES_CALM, FS))
        with self.assertRaises(ValueError):
            engine.ingest(SpectrumFrame(0, 1, fixtures.SAMPLES_CALM_V2, FS))


class MissingFramesTest(unittest.TestCase):
    """Missing-frame interval statistics."""

    def test_missing_intervals_and_stats(self):
        engine = Engine(BandSet("bands-A", (Band("all", 0.0, FULL_SPAN),)))
        for seq in (0, 1, 4, 7):
            engine.ingest(SpectrumFrame(seq, 1, fixtures.SAMPLES_CALM, FS))
        self.assertEqual(engine.missing_intervals(), [(2, 3), (5, 6)])
        stats = engine.missing_stats()
        self.assertEqual(stats["missing_count"], 4)
        self.assertEqual(stats["longest_interval"], 2)
        self.assertEqual(stats["span"], 8)
        self.assertAlmostEqual(stats["frame_coverage"], 0.5, delta=TOL)

    def test_no_missing(self):
        engine = Engine(BandSet("bands-A", (Band("all", 0.0, FULL_SPAN),)))
        for seq in (0, 1, 2):
            engine.ingest(SpectrumFrame(seq, 1, fixtures.SAMPLES_CALM, FS))
        self.assertEqual(engine.missing_intervals(), [])
        self.assertEqual(engine.missing_stats()["missing_count"], 0)


class CheckpointTest(unittest.TestCase):
    """Checkpoint/restore must reproduce state and continue consistently."""

    def build_engine(self):
        bands_a = BandSet("bands-A", (Band("all", 0.0, FULL_SPAN),))
        bands_b = BandSet("bands-B", (
            Band("low", 0.0, 16.0),
            Band("high", 16.0, FULL_SPAN),
        ))
        engine = Engine(bands_a)
        engine.ingest(SpectrumFrame(0, 1, fixtures.SAMPLES_CALM, FS))
        engine.ingest(SpectrumFrame(3, 1, fixtures.SAMPLES_BRIGHT, FS))
        engine.switch_band_set(bands_b)
        engine.ingest(SpectrumFrame(0, 2, fixtures.SAMPLES_CALM_V2, FS))
        return engine

    def test_checkpoint_restore(self):
        engine = self.build_engine()
        payload = engine.checkpoint()
        restored = Engine.restore(payload)
        self.assertEqual(engine.snapshot(), restored.snapshot())
        # Gap statistics survive the round trip (seq 1 and 2 are missing).
        self.assertEqual(restored.missing_intervals(), [(1, 2)])
        self.assertEqual(len(restored.corrections), 1)
        self.assertEqual(restored.corrections[0].band_version, "bands-B")

        # Both engines continue identically after recovery.
        follow_up = SpectrumFrame(4, 1, fixtures.SAMPLES_BRIGHT, FS)
        engine.ingest(follow_up)
        restored.ingest(follow_up)
        self.assertEqual(engine.snapshot(), restored.snapshot())


if __name__ == "__main__":
    unittest.main()
