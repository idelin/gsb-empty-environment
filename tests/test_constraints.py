import unittest

from lockmerge.constraints import Constraint
from lockmerge.version import Version


def v(text):
    return Version.parse(text)


class TestConstraintParse(unittest.TestCase):
    def test_wildcard(self):
        for raw in ("*", "", "any"):
            self.assertTrue(Constraint.parse(raw).satisfies(v("9.9.9")))

    def test_exact(self):
        c = Constraint.parse("1.2.3")
        self.assertTrue(c.satisfies(v("1.2.3")))
        self.assertFalse(c.satisfies(v("1.2.4")))
        self.assertFalse(c.satisfies(v("1.2.3-rc1")))

    def test_operators(self):
        self.assertTrue(Constraint.parse(">=1.2.0").satisfies(v("1.2.0")))
        self.assertTrue(Constraint.parse(">=1.2.0").satisfies(v("2.0.0")))
        self.assertFalse(Constraint.parse(">=1.2.0").satisfies(v("1.1.9")))
        self.assertTrue(Constraint.parse("<2.0.0").satisfies(v("1.9.9")))
        self.assertFalse(Constraint.parse("<2.0.0").satisfies(v("2.0.0")))
        self.assertTrue(Constraint.parse("==1.0.0").satisfies(v("1.0.0")))
        self.assertTrue(Constraint.parse("=1.0.0").satisfies(v("1.0.0")))

    def test_combined(self):
        c = Constraint.parse(">=1.2.0,<2.0.0")
        self.assertTrue(c.satisfies(v("1.5.0")))
        self.assertFalse(c.satisfies(v("2.0.0")))
        self.assertFalse(c.satisfies(v("1.1.0")))

    def test_caret(self):
        c = Constraint.parse("^1.2.3")
        self.assertTrue(c.satisfies(v("1.2.3")))
        self.assertTrue(c.satisfies(v("1.9.0")))
        self.assertFalse(c.satisfies(v("2.0.0")))
        self.assertFalse(c.satisfies(v("1.2.2")))

    def test_caret_zero_major(self):
        self.assertTrue(Constraint.parse("^0.2.3").satisfies(v("0.2.9")))
        self.assertFalse(Constraint.parse("^0.2.3").satisfies(v("0.3.0")))
        self.assertFalse(Constraint.parse("^0.0.3").satisfies(v("0.0.4")))

    def test_tilde(self):
        c = Constraint.parse("~1.2.3")
        self.assertTrue(c.satisfies(v("1.2.9")))
        self.assertFalse(c.satisfies(v("1.3.0")))

    def test_prerelease_bounds(self):
        c = Constraint.parse(">=1.5.0-rc1,<1.6.0")
        self.assertTrue(c.satisfies(v("1.5.0-rc1")))
        self.assertTrue(c.satisfies(v("1.5.0")))
        self.assertFalse(c.satisfies(v("1.5.0-b2")))

    def test_invalid(self):
        for bad in ("??1.0", ">=x.y.z", "=>1.0.0"):
            with self.assertRaises(ValueError, msg=bad):
                Constraint.parse(bad)


if __name__ == "__main__":
    unittest.main()
