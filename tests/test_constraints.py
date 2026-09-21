import unittest

from depsolver.constraints import Constraint, ConstraintError
from depsolver.version import Version


def v(text):
    return Version.parse(text)


class TestConstraintParse(unittest.TestCase):
    def test_empty_and_star_allow_anything(self):
        for text in ("", "*", None):
            constraint = Constraint.parse(text)
            self.assertTrue(constraint.allows(v("0.0.1")))
            self.assertTrue(constraint.allows(v("9.9.9-rc.1")))
            self.assertEqual(str(constraint), "*")

    def test_single_comparator_roundtrip(self):
        constraint = Constraint.parse(">=1.2.0")
        self.assertEqual(str(constraint), ">=1.2.0")

    def test_range(self):
        constraint = Constraint.parse(">=1.0.0,<2.0.0")
        self.assertTrue(constraint.allows(v("1.0.0")))
        self.assertTrue(constraint.allows(v("1.9.9")))
        self.assertFalse(constraint.allows(v("2.0.0")))
        self.assertFalse(constraint.allows(v("0.9.9")))

    def test_exact_and_exclusion(self):
        self.assertTrue(Constraint.parse("==1.2.3").allows(v("1.2.3")))
        self.assertFalse(Constraint.parse("==1.2.3").allows(v("1.2.4")))
        self.assertFalse(Constraint.parse("!=1.2.3").allows(v("1.2.3")))
        self.assertTrue(Constraint.parse("!=1.2.3").allows(v("1.2.4")))

    def test_prerelease_comparators(self):
        constraint = Constraint.parse(">=1.0.0-alpha")
        self.assertTrue(constraint.allows(v("1.0.0-beta")))
        self.assertTrue(constraint.allows(v("1.0.0")))
        self.assertFalse(constraint.allows(v("0.9.9")))

    def test_invalid_comparator(self):
        with self.assertRaises(ConstraintError):
            Constraint.parse("~1.2.3")
        with self.assertRaises(ConstraintError):
            Constraint.parse("1.2.3")


if __name__ == "__main__":
    unittest.main()
