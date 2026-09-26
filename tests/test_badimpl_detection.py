import unittest

from filterverify import BandSpec, VerifierConfig, verify
from filterverify.counterexample import CounterExample
from filterverify.impls import (
    AttenuatingFilter,
    BlockParityFilter,
    BlockResetFIR,
    FIRFilter,
    UndeclaredDelayFIR,
    design_lowpass,
)

FS = 8000.0
SPEC = BandSpec(FS, (100.0, 1000.0), (1800.0, 3900.0))
TAPS = design_lowpass(53, 1400.0, FS)
CFG = VerifierConfig(block_trials=8)


def measurement(report, name):
    return next(m for m in report.measurements if m.name == name)


class BadImplementationDetectionTest(unittest.TestCase):
    def test_block_reset_detected_with_shrunk_replayable_counterexample(self):
        report = verify(lambda: BlockResetFIR(TAPS), SPEC, CFG, name="BlockResetFIR")
        self.assertFalse(report.passed)
        ce = next(c for c in report.counterexamples if c.kind == "block_inconsistency")
        # shrunk: far fewer blocks than a random partition, still failing
        self.assertLessEqual(len(ce.partition), 4)
        still_failing, err = ce.replay(lambda: BlockResetFIR(TAPS))
        self.assertTrue(still_failing)
        self.assertGreater(err, ce.tolerance)
        # evidence keeps the replayable input spec and error excerpt
        self.assertIn("around_error", ce.evidence)
        self.assertGreater(ce.signal_length, 0)
        # a correct implementation must NOT fail on the same counterexample
        ok_fails, _ = ce.replay(lambda: FIRFilter(TAPS))
        self.assertFalse(ok_fails)

    def test_counterexample_json_roundtrip(self):
        report = verify(lambda: BlockResetFIR(TAPS), SPEC, CFG)
        ce = report.counterexamples[0]
        clone = CounterExample.from_json(ce.to_json())
        self.assertEqual(clone.partition, ce.partition)
        still_failing, _ = clone.replay(lambda: BlockResetFIR(TAPS))
        self.assertTrue(still_failing)

    def test_undeclared_delay_detected_as_time_offset(self):
        report = verify(lambda: UndeclaredDelayFIR(TAPS), SPEC, CFG,
                        name="UndeclaredDelayFIR")
        self.assertFalse(report.passed)
        rec = measurement(report, "time_offset")
        self.assertFalse(rec.passed)
        self.assertEqual(rec.value, 3.0)  # measured - declared
        phase = measurement(report, "passband_phase_residual")
        self.assertFalse(phase.passed)

    def test_attenuation_detected_as_passband_deviation(self):
        report = verify(AttenuatingFilter, SPEC, CFG, name="AttenuatingFilter")
        self.assertFalse(report.passed)
        rec = measurement(report, "passband_deviation")
        self.assertFalse(rec.passed)
        self.assertAlmostEqual(rec.value, 0.5, places=6)

    def test_block_size_dependent_gain_detected(self):
        report = verify(BlockParityFilter, SPEC, CFG, name="BlockParityFilter")
        self.assertFalse(report.passed)
        kinds = {c.kind for c in report.counterexamples}
        self.assertIn("block_inconsistency", kinds)


if __name__ == "__main__":
    unittest.main()
