from __future__ import annotations

import tempfile
import unittest
from pathlib import Path

from spectrum_integrator import SpectrumIntegrator

from tests.fixtures import (
    FRAME_LENGTH,
    HOP_SAMPLES,
    SAMPLE_RATE,
    frame_eight_tone,
    frame_four_corrected,
    frame_four_silence,
    frame_zero,
    frame_zero_corrected,
    narrow_rules,
    widened_rules,
)


def make_integrator() -> SpectrumIntegrator:
    return SpectrumIntegrator(
        sample_rate=SAMPLE_RATE,
        frame_length=FRAME_LENGTH,
        initial_bands=narrow_rules(),
        hop_samples=HOP_SAMPLES,
    )


class StreamingVersionTest(unittest.TestCase):
    def test_frame_correction_and_band_switch_interleave_without_mixing(self) -> None:
        processor = make_integrator()

        first = processor.add_frame(frame_zero())
        self.assertEqual(first.bands_version, "bands-v1-narrow")

        band_corrections = processor.set_band_rules(widened_rules(), effective_from_sample=4)
        self.assertEqual(band_corrections, [])

        silent = processor.add_frame(frame_four_silence())
        self.assertEqual(silent.bands_version, "bands-v2-widened")

        corrected_late = processor.correct_frame(frame_four_corrected())
        self.assertEqual(corrected_late.bands_version, "bands-v2-widened")
        self.assertEqual(corrected_late.frame_version, "spectrum-v2")
        self.assertEqual(corrected_late.event_type, "correction")
        self.assertAlmostEqual(corrected_late.total_energy_delta, 3.0)
        self.assertAlmostEqual(
            corrected_late.absolute_energy_delta["low"], 2.5
        )
        self.assertAlmostEqual(
            corrected_late.absolute_energy_delta["high"], 0.5
        )

        corrected_old = processor.correct_frame(frame_zero_corrected())
        self.assertEqual(corrected_old.bands_version, "bands-v1-narrow")
        self.assertEqual(corrected_old.frame_version, "spectrum-v2")
        self.assertAlmostEqual(corrected_old.total_energy, 13.0)
        self.assertAlmostEqual(corrected_old.bands[0].absolute_energy, 12.25)
        self.assertAlmostEqual(corrected_old.bands[1].absolute_energy, 0.5)
        self.assertAlmostEqual(corrected_old.total_energy_delta, 3.0)
        self.assertAlmostEqual(
            corrected_old.absolute_energy_delta["low"], 3.25
        )
        self.assertAlmostEqual(
            corrected_old.absolute_energy_delta["high"], 0.0
        )

        events = processor.events
        self.assertEqual(
            [event.frame_id for event in events],
            ["frame-000", "frame-004", "frame-004", "frame-000"],
        )
        self.assertEqual(
            [event.bands_version for event in events],
            [
                "bands-v1-narrow",
                "bands-v2-widened",
                "bands-v2-widened",
                "bands-v1-narrow",
            ],
        )
        for event in events:
            self.assertEqual(
                event.source_evidence.bands_version, event.bands_version
            )
            self.assertEqual(event.source_evidence.frame_id, event.frame_id)
            self.assertEqual(event.source_evidence.frame_version, event.frame_version)
            self.assertEqual(len(event.source_evidence.frame_checksum_sha256), 64)
            self.assertEqual(len(event.source_evidence.band_checksum_sha256), 64)

    def test_band_version_update_emits_incremental_correction(self) -> None:
        processor = make_integrator()
        original = processor.add_frame(frame_zero())

        corrections = processor.set_band_rules(
            widened_rules(), effective_from_sample=0
        )
        self.assertEqual(len(corrections), 1)
        correction = corrections[0]

        self.assertEqual(correction.event_type, "correction")
        self.assertEqual(correction.frame_id, original.frame_id)
        self.assertEqual(correction.frame_version, original.frame_version)
        self.assertEqual(correction.bands_version, "bands-v2-widened")
        self.assertEqual(
            correction.correction_reasons, ("band-rules-version-changed",)
        )
        self.assertAlmostEqual(correction.absolute_energy_delta["low"], 0.5)
        self.assertAlmostEqual(correction.absolute_energy_delta["high"], 0.0)
        self.assertAlmostEqual(correction.total_energy_delta, 0.0)
        self.assertAlmostEqual(correction.duplicate_coverage_energy_delta, 0.0)
        self.assertEqual(
            correction.source_evidence.frame_checksum_sha256,
            original.source_evidence.frame_checksum_sha256,
        )
        self.assertNotEqual(
            correction.source_evidence.band_checksum_sha256,
            original.source_evidence.band_checksum_sha256,
        )
        self.assertEqual(
            processor.latest_results[original.frame_id].bands_version,
            "bands-v2-widened",
        )


class MissingAndCheckpointTest(unittest.TestCase):
    def test_missing_interval_statistics(self) -> None:
        processor = make_integrator()
        processor.add_frame(frame_zero())
        processor.add_frame(frame_eight_tone())

        missing = processor.missing_intervals()
        self.assertEqual(len(missing), 1)
        self.assertEqual(missing[0].start_sample, 4)
        self.assertEqual(missing[0].end_sample, 8)
        self.assertEqual(missing[0].length_samples, 4)
        self.assertEqual(missing[0].missing_frame_starts, (4,))

        horizon_missing = processor.missing_intervals(expected_through_sample=12)
        self.assertEqual(len(horizon_missing), 1)
        self.assertEqual(horizon_missing[0].missing_frame_starts, (4,))

        processor.add_frame(frame_four_silence())
        self.assertEqual(processor.missing_intervals(), ())

    def test_checkpoint_restores_events_versions_and_missing_state(self) -> None:
        processor = make_integrator()
        processor.add_frame(frame_zero())
        processor.add_frame(frame_eight_tone())
        corrections = processor.set_band_rules(
            widened_rules(), effective_from_sample=4
        )
        self.assertEqual(len(corrections), 1)
        self.assertEqual(corrections[0].frame_id, "frame-008")

        with tempfile.TemporaryDirectory() as directory:
            checkpoint = Path(directory) / "integrator-checkpoint.json"
            processor.save_checkpoint(str(checkpoint))
            restored = SpectrumIntegrator.load_checkpoint(str(checkpoint))

        self.assertEqual(len(restored.events), 3)
        self.assertEqual(restored.latest_results["frame-000"].bands_version, "bands-v1-narrow")
        self.assertEqual(restored.latest_results["frame-008"].bands_version, "bands-v2-widened")
        self.assertEqual(restored.missing_intervals()[0].missing_frame_starts, (4,))
        self.assertEqual(
            restored.latest_results["frame-008"].source_evidence.frame_checksum_sha256,
            processor.latest_results["frame-008"].source_evidence.frame_checksum_sha256,
        )

        late = restored.add_frame(frame_four_silence())
        self.assertEqual(late.event_id, "event-000004")
        self.assertEqual(late.bands_version, "bands-v2-widened")
        self.assertEqual(restored.missing_intervals(), ())

        old_correction = restored.correct_frame(frame_zero_corrected())
        self.assertEqual(old_correction.bands_version, "bands-v1-narrow")
        self.assertAlmostEqual(old_correction.bands[0].absolute_energy, 12.25)
        self.assertAlmostEqual(old_correction.bands[1].absolute_energy, 0.5)


if __name__ == "__main__":
    unittest.main()
