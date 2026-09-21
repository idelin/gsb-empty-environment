import unittest

from buildsys.version import Constraint, Version


class TestVersionParse(unittest.TestCase):
    def test_basic(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertEqual(v.prerelease, ())
        self.assertEqual(str(v), "1.2.3")

    def test_prerelease(self):
        v = Version.parse("1.2.3-rc.1")
        self.assertEqual(v.prerelease, ("rc", "1"))
        self.assertEqual(str(v), "1.2.3-rc.1")

    def test_invalid(self):
        for bad in ("1.2", "1.2.3.4", "01.2.3", "1.2.x", "v1.2.3", ""):
            with self.assertRaises(ValueError, msg=bad):
                Version.parse(bad)


class TestVersionOrdering(unittest.TestCase):
    def test_core_ordering(self):
        self.assertLess(Version.parse("1.2.3"), Version.parse("1.10.0"))
        self.assertLess(Version.parse("0.9.9"), Version.parse("1.0.0"))

    def test_release_beats_prerelease(self):
        self.assertLess(Version.parse("1.0.0-rc.1"), Version.parse("1.0.0"))

    def test_semver_org_chain(self):
        chain = [
            "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
            "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
        ]
        versions = [Version.parse(c) for c in chain]
        for earlier, later in zip(versions, versions[1:]):
            self.assertLess(earlier, later)
        self.assertEqual(versions, sorted(reversed(versions)))

    def test_numeric_identifiers_below_alpha(self):
        self.assertLess(Version.parse("1.0.0-1"), Version.parse("1.0.0-a"))


class TestConstraint(unittest.TestCase):
    def test_exact_bare_and_op(self):
        self.assertTrue(Constraint.parse("1.2.3").matches(Version.parse("1.2.3")))
        self.assertTrue(Constraint.parse("==1.2.3").matches(Version.parse("1.2.3")))
        self.assertFalse(Constraint.parse("==1.2.3").matches(Version.parse("1.2.4")))

    def test_range(self):
        c = Constraint.parse(">=1.0.0 <2.0.0")
        self.assertTrue(c.matches(Version.parse("1.4.2")))
        self.assertFalse(c.matches(Version.parse("2.0.0")))
        self.assertFalse(c.matches(Version.parse("0.9.9")))

    def test_any(self):
        self.assertTrue(Constraint.parse("*").matches(Version.parse("3.1.4")))
        self.assertTrue(Constraint.parse("").matches(Version.parse("0.0.1")))

    def test_prerelease_bounds(self):
        c = Constraint.parse(">=2.0.0-rc.1 <2.0.0")
        self.assertTrue(c.matches(Version.parse("2.0.0-rc.2")))
        self.assertFalse(c.matches(Version.parse("2.0.0")))
        self.assertFalse(c.matches(Version.parse("2.0.0-alpha")))


if __name__ == "__main__":
    unittest.main()
