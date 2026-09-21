import unittest

from depres import Constraint, Version


def v(text):
    return Version.parse(text)


class TestConstraintParse(unittest.TestCase):
    def test_any(self):
        for text in ("", "*", None):
            self.assertEqual(Constraint.parse(text).comparators, ())

    def test_bare_version_means_exact(self):
        c = Constraint.parse("1.2.3")
        self.assertTrue(c.satisfied_by(v("1.2.3")))
        self.assertFalse(c.satisfied_by(v("1.2.4")))

    def test_range_with_exclusion(self):
        c = Constraint.parse(">=1.0.0, <2.0.0, !=1.5.0")
        self.assertTrue(c.satisfied_by(v("1.4.9")))
        self.assertFalse(c.satisfied_by(v("1.5.0")))
        self.assertFalse(c.satisfied_by(v("2.0.0")))
        self.assertFalse(c.satisfied_by(v("0.9.9")))

    def test_space_separated(self):
        c = Constraint.parse(">=1.0.0 <2.0.0")
        self.assertTrue(c.satisfied_by(v("1.9.9")))
        self.assertFalse(c.satisfied_by(v("2.0.0")))

    def test_str_round_trip(self):
        self.assertEqual(str(Constraint.parse(">=1.0.0, !=1.5.0")), ">=1.0.0, !=1.5.0")
        self.assertEqual(str(Constraint.parse("*")), "any")


class TestPrereleaseGate(unittest.TestCase):
    def test_prerelease_not_allowed_by_plain_range(self):
        self.assertFalse(Constraint.parse(">=1.0.0").allows_prerelease())

    def test_prerelease_allowed_when_named(self):
        self.assertTrue(Constraint.parse(">=2.0.0-beta.1").allows_prerelease())


if __name__ == "__main__":
    unittest.main()
