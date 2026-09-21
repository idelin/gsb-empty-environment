import unittest

from buildset.version import Constraint, Version


class VersionParseTests(unittest.TestCase):
    def test_plain_version(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertEqual(v.prerelease, ())
        self.assertEqual(str(v), "1.2.3")

    def test_prerelease_version(self):
        v = Version.parse("2.2.0-beta.1")
        self.assertEqual(v.prerelease, ("beta", 1))
        self.assertEqual(str(v), "2.2.0-beta.1")

    def test_rejects_missing_patch(self):
        with self.assertRaises(ValueError):
            Version.parse("1.2")

    def test_rejects_four_components(self):
        with self.assertRaises(ValueError):
            Version.parse("1.2.3.4")

    def test_rejects_build_metadata(self):
        with self.assertRaises(ValueError):
            Version.parse("1.2.3+build.5")


class VersionOrderTests(unittest.TestCase):
    def test_release_chain(self):
        versions = ["0.9.9", "1.0.0", "1.0.1", "1.2.0", "2.0.0"]
        parsed = [Version.parse(v) for v in versions]
        self.assertEqual(sorted(parsed, reverse=True), parsed[::-1])

    def test_prerelease_lower_than_release(self):
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0"))

    def test_prerelease_chain(self):
        chain = ["1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-beta", "1.0.0"]
        parsed = [Version.parse(v) for v in chain]
        self.assertEqual(sorted(parsed), parsed)

    def test_numeric_identifiers_below_alpha(self):
        self.assertLess(Version.parse("1.0.0-1"), Version.parse("1.0.0-alpha"))

    def test_numeric_identifiers_compare_numerically(self):
        self.assertLess(Version.parse("1.0.0-2"), Version.parse("1.0.0-11"))


class ConstraintTests(unittest.TestCase):
    def test_empty_matches_anything(self):
        c = Constraint.parse("")
        self.assertTrue(c.allows(Version.parse("0.0.1")))
        self.assertTrue(c.allows(Version.parse("9.9.9-rc.1")))

    def test_range(self):
        c = Constraint.parse(">=1.2.0,<2.0.0")
        self.assertTrue(c.allows(Version.parse("1.5.0")))
        self.assertFalse(c.allows(Version.parse("1.1.9")))
        self.assertFalse(c.allows(Version.parse("2.0.0")))

    def test_bare_version_means_exact(self):
        c = Constraint.parse("1.4.0")
        self.assertTrue(c.allows(Version.parse("1.4.0")))
        self.assertFalse(c.allows(Version.parse("1.4.1")))

    def test_prerelease_participates_by_precedence(self):
        c = Constraint.parse(">=2.1.0")
        self.assertTrue(c.allows(Version.parse("2.2.0-beta.1")))
        self.assertFalse(c.allows(Version.parse("2.1.0-alpha")))

    def test_not_equal(self):
        c = Constraint.parse("!=1.3.0")
        self.assertFalse(c.allows(Version.parse("1.3.0")))
        self.assertTrue(c.allows(Version.parse("1.3.1")))


if __name__ == "__main__":
    unittest.main()
