import unittest

from filterverify import BandSpec, VerifierConfig, verify
from filterverify.impls import FIRFilter, IdentityFilter, design_lowpass

FS = 8000.0
SPEC = BandSpec(FS, (100.0, 1000.0), (1800.0, 3900.0))
TAPS = design_lowpass(53, 1400.0, FS)


class GoodImplementationTest(unittest.TestCase):
    @classmethod
    def setUpClass(cls):
        cls.report = verify(lambda: FIRFilter(TAPS, name="GoodLowpass"), SPEC,
                            VerifierConfig(block_trials=8), name="GoodLowpass")

    def test_overall_pass(self):
        self.assertTrue(self.report.passed, msg="\n" + self.report.to_text())

    def test_no_counterexamples(self):
        self.assertEqual(self.report.counterexamples, [])

    def test_full_band_certified_from_coefficients(self):
        self.assertIn("certified from FIR coefficients", self.report.full_band_claim)

    def test_time_offset_zero(self):
        rec = next(m for m in self.report.measurements if m.name == "time_offset")
        self.assertEqual(rec.value, 0.0)

    def test_every_measurement_reports_samples_and_method(self):
        for m in self.report.measurements:
            self.assertGreater(m.num_samples, 0, m.name)
            self.assertTrue(m.method, m.name)

    def test_identity_passes_unity_gain(self):
        spec = BandSpec(FS, (100.0, 1000.0), (1800.0, 3900.0))
        report = verify(IdentityFilter, spec, VerifierConfig(block_trials=4),
                        name="Identity")
        rec = next(m for m in report.measurements if m.name == "passband_deviation")
        self.assertLess(rec.value, 1e-6)
        self.assertIn("not certified", report.full_band_claim)


if __name__ == "__main__":
    unittest.main()
