import unittest

from lockmerge.versions import Constraint, Version, VersionError


class VersionParseTest(unittest.TestCase):
    def test_parse_release(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertEqual(v.prerelease, ())
        self.assertEqual(str(v), "1.2.3")

    def test_parse_prerelease_roundtrip(self):
        v = Version.parse("2.0.0-rc.1")
        self.assertEqual(str(v), "2.0.0-rc.1")

    def test_reject_invalid_versions(self):
        for bad in ["1.2", "1.2.3.4", "v1.2.3", "1.02.3", "", "1.2.x", 3]:
            with self.assertRaises(VersionError, msg=repr(bad)):
                Version.parse(bad)


class VersionOrderTest(unittest.TestCase):
    def test_numeric_ordering(self):
        self.assertLess(Version.parse("1.2.3"), Version.parse("1.10.0"))
        self.assertLess(Version.parse("0.9.9"), Version.parse("1.0.0"))
        self.assertGreater(Version.parse("2.0.1"), Version.parse("2.0.0"))

    def test_prerelease_before_release(self):
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0"))
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0-alpha.1"))
        self.assertLess(Version.parse("1.0.0-alpha.1"), Version.parse("1.0.0-alpha.beta"))
        self.assertLess(Version.parse("1.0.0-rc.1"), Version.parse("1.0.0"))

    def test_equality(self):
        self.assertEqual(Version.parse("1.2.3"), Version.parse("1.2.3"))
        self.assertNotEqual(Version.parse("1.2.3"), Version.parse("1.2.3-rc.1"))


class ConstraintTest(unittest.TestCase):
    def test_exact_and_empty(self):
        self.assertTrue(Constraint("1.2.3").allows(Version.parse("1.2.3")))
        self.assertFalse(Constraint("1.2.3").allows(Version.parse("1.2.4")))
        self.assertTrue(Constraint("").allows(Version.parse("9.9.9")))
        self.assertTrue(Constraint("*").allows(Version.parse("9.9.9")))

    def test_comparators(self):
        c = Constraint(">=1.2.0 <2.0.0")
        self.assertTrue(c.allows(Version.parse("1.2.0")))
        self.assertTrue(c.allows(Version.parse("1.9.9")))
        self.assertFalse(c.allows(Version.parse("2.0.0")))
        self.assertFalse(c.allows(Version.parse("1.1.9")))
        self.assertTrue(Constraint("!=1.2.3").allows(Version.parse("1.2.4")))
        self.assertFalse(Constraint("!=1.2.3").allows(Version.parse("1.2.3")))
        self.assertTrue(Constraint("=1.2.3").allows(Version.parse("1.2.3")))

    def test_caret(self):
        self.assertTrue(Constraint("^1.2.3").allows(Version.parse("1.9.0")))
        self.assertFalse(Constraint("^1.2.3").allows(Version.parse("2.0.0")))
        self.assertTrue(Constraint("^0.2.3").allows(Version.parse("0.2.9")))
        self.assertFalse(Constraint("^0.2.3").allows(Version.parse("0.3.0")))
        self.assertFalse(Constraint("^0.0.3").allows(Version.parse("0.0.4")))

    def test_tilde(self):
        self.assertTrue(Constraint("~1.2.3").allows(Version.parse("1.2.9")))
        self.assertFalse(Constraint("~1.2.3").allows(Version.parse("1.3.0")))

    def test_prerelease_in_constraint(self):
        self.assertTrue(Constraint(">=2.0.0-rc.1").allows(Version.parse("2.0.0")))
        self.assertTrue(Constraint("<2.0.0").allows(Version.parse("2.0.0-rc.1")))

    def test_reject_invalid_constraint(self):
        with self.assertRaises(VersionError):
            Constraint(">=1.2")
        with self.assertRaises(VersionError):
            Constraint(5)


if __name__ == "__main__":
    unittest.main()
