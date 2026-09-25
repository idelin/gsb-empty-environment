from __future__ import annotations

import unittest

from spectrum_integrator import (
    coverage_weights,
    estimate_spectrum,
    integrate_bands,
)

from tests.fixtures import (
    SAMPLE_RATE,
    frame_zero,
    full_cover_rules,
    nested_overlap_rules,
    narrow_rules,
    widened_rules,
)


class SpectrumIntegrationTest(unittest.TestCase):
    def test_non_overlapping_full_coverage_conserves_energy(self) -> None:
        frame = frame_zero()
        spectrum, bands, gross, union, duplicate = integrate_bands(
            frame.samples, frame.sample_rate, full_cover_rules()
        )

        self.assertEqual(spectrum.resolution_hz, 1.0)
        self.assertEqual(spectrum.frequencies, (0.0, 1.0, 2.0))
        self.assertAlmostEqual(spectrum.total_energy, 10.0)
        self.assertAlmostEqual(bands[0].absolute_energy, 9.5)
        self.assertAlmostEqual(bands[1].absolute_energy, 0.5)
        self.assertAlmostEqual(gross, 10.0)
        self.assertAlmostEqual(union, 10.0)
        self.assertAlmostEqual(duplicate, 0.0)
        self.assertAlmostEqual(
            sum(band.relative_energy for band in bands), 1.0
        )

    def test_cross_boundary_bin_uses_partial_weight(self) -> None:
        weights = coverage_weights((0.0, 1.0, 2.0), 1.0, 0.0, 1.0)
        self.assertEqual(weights, (1.0, 0.5, 0.0))

        spectrum = estimate_spectrum(frame_zero().samples, SAMPLE_RATE)
        density = spectrum.energy_density
        expected = density[0] + density[1] * 0.5
        self.assertAlmostEqual(expected, 9.5)

    def test_widening_one_band_changes_only_that_band(self) -> None:
        frame = frame_zero()
        _, wide, _, _, _ = integrate_bands(
            frame.samples, frame.sample_rate, widened_rules()
        )
        _, narrow, _, _, _ = integrate_bands(
            frame.samples, frame.sample_rate, narrow_rules()
        )

        self.assertAlmostEqual(wide[0].absolute_energy, 9.5)
        self.assertAlmostEqual(wide[1].absolute_energy, 0.5)
        self.assertAlmostEqual(narrow[0].absolute_energy, 9.0)
        self.assertAlmostEqual(narrow[1].absolute_energy, 0.5)
        self.assertAlmostEqual(
            wide[0].absolute_energy - narrow[0].absolute_energy, 0.5
        )
        self.assertAlmostEqual(
            narrow[1].absolute_energy - wide[1].absolute_energy, 0.0
        )
        self.assertEqual(narrow[1].coverage_weights, wide[1].coverage_weights)

    def test_small_grid_matches_hand_integration(self) -> None:
        samples = (1.0, 2.0, 2.0, 1.0)
        spectrum = estimate_spectrum(samples, SAMPLE_RATE)
        self.assertEqual(spectrum.energy_density[0], 9.0)
        self.assertAlmostEqual(spectrum.energy_density[1], 1.0)
        self.assertAlmostEqual(spectrum.energy_density[2], 0.0)
        self.assertEqual(spectrum.total_energy, sum(x * x for x in samples))

        _, bands, _, _, _ = integrate_bands(
            samples, SAMPLE_RATE, widened_rules()
        )
        self.assertAlmostEqual(bands[0].absolute_energy, 9.5)
        self.assertAlmostEqual(bands[1].absolute_energy, 0.5)
        self.assertEqual(bands[0].coverage_weights, (1.0, 0.5, 0.0))
        self.assertEqual(bands[1].coverage_weights, (0.0, 0.5, 1.0))

    def test_zero_total_energy_has_unknown_relative_energy(self) -> None:
        samples = (0.0, 0.0, 0.0, 0.0)
        spectrum, bands, _, _, _ = integrate_bands(
            samples, SAMPLE_RATE, full_cover_rules()
        )
        self.assertEqual(spectrum.total_energy, 0.0)
        for band in bands:
            self.assertEqual(band.absolute_energy, 0.0)
            self.assertIsNone(band.relative_energy)

    def test_nested_overlap_is_not_normalized_to_one(self) -> None:
        frame = frame_zero()
        _, bands, gross, union, duplicate = integrate_bands(
            frame.samples, frame.sample_rate, nested_overlap_rules()
        )

        self.assertAlmostEqual(bands[0].absolute_energy, 10.0)
        self.assertAlmostEqual(bands[1].absolute_energy, 9.5)
        self.assertAlmostEqual(gross, 19.5)
        self.assertAlmostEqual(union, 10.0)
        self.assertAlmostEqual(duplicate, 9.5)
        self.assertAlmostEqual(sum(b.relative_energy for b in bands), 1.95)


if __name__ == "__main__":
    unittest.main()
