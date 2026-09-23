import unittest
from fractions import Fraction as F

from interval_arith import AffineForm, Certificate, Interval, NoiseRegistry


def make_x(lo, hi):
    reg = NoiseRegistry()
    return AffineForm.from_interval(Interval(lo, hi), reg), reg


class TestAffine(unittest.TestCase):
    def test_from_interval_roundtrip(self):
        x, _ = make_x(-1, 1)
        self.assertEqual(x.to_interval(), Interval(-1, 1))

    def test_linear_ops_exact(self):
        x, _ = make_x(-1, 1)
        y = (x + x).scale(F(1, 2)) - 1
        self.assertEqual(y.to_interval(), Interval(-2, 0))
        # x - x cancels exactly thanks to the shared noise symbol
        z = x - x
        self.assertEqual(z.to_interval(), Interval(0, 0))

    def test_square_exploits_correlation(self):
        x, reg = make_x(-1, 1)
        self.assertEqual(x.square(reg).to_interval(), Interval(0, 1))

    def test_square_conservative_elsewhere(self):
        x, reg = make_x(1, 2)
        sq = x.square(reg).to_interval()
        self.assertTrue(Interval(1, 4).subset_of(sq))  # contains true range
        self.assertTrue(sq.subset_of(Interval(F(3, 4), 4)))

    def test_mul_independent(self):
        reg = NoiseRegistry()
        x = AffineForm.from_interval(Interval(-1, 1), reg)
        y = AffineForm.from_interval(Interval(-1, 1), reg)
        self.assertEqual(x.mul(y, reg).to_interval(), Interval(-1, 1))

    def test_mul_same_form_conservative_but_sound(self):
        x, reg = make_x(-1, 1)
        wide = x.mul(x, reg).to_interval()
        tight = x.square(reg).to_interval()
        self.assertEqual(wide, Interval(-1, 1))
        self.assertTrue(tight.subset_of(wide))  # square never wider than mul

    def test_certificate_records_steps_and_domains(self):
        cert = Certificate("x*x range on [-1,1]")
        reg = NoiseRegistry()
        x = AffineForm.from_interval(Interval(-1, 1), reg, cert=cert)
        x.square(reg, cert=cert)
        self.assertGreaterEqual(len(cert.steps), 3)
        for step in cert.steps:
            self.assertTrue(step.domain)
        self.assertIn("eps_0", cert.steps[0].rule)


if __name__ == "__main__":
    unittest.main()
