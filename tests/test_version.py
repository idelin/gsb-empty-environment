import unittest

from licsel.version import Version, VersionConstraint


class TestVersionParse(unittest.TestCase):
    def test_plain(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch, v.prerelease), (1, 2, 3, ()))
        self.assertEqual(str(v), "1.2.3")

    def test_prerelease(self):
        v = Version.parse("2.0.0-rc.1")
        self.assertEqual(v.prerelease, ("rc", "1"))
        self.assertEqual(str(v), "2.0.0-rc.1")

    def test_invalid(self):
        for bad in ["1.2", "1.2.3.4", "v1.2.3", "1.02.3", "1.2.3-", "1.2.3-a..b", ""]:
            with self.assertRaises(ValueError, msg=bad):
                Version.parse(bad)


class TestVersionOrdering(unittest.TestCase):
    def test_core_ordering(self):
        self.assertLess(Version.parse("1.2.3"), Version.parse("1.10.0"))
        self.assertLess(Version.parse("0.9.9"), Version.parse("1.0.0"))

    def test_prerelease_before_release(self):
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0"))

    def test_prerelease_numeric_vs_alpha(self):
        self.assertLess(Version.parse("1.0.0-1"), Version.parse("1.0.0-alpha"))
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0-alpha.1"))
        self.assertLess(Version.parse("1.0.0-alpha.1"), Version.parse("1.0.0-alpha.2"))
        self.assertLess(Version.parse("1.0.0-alpha.2"), Version.parse("1.0.0-beta"))
        self.assertLess(Version.parse("1.0.0-2"), Version.parse("1.0.0-10"))

    def test_equality_hash(self):
        self.assertEqual(Version.parse("1.0.0-rc.1"), Version.parse("1.0.0-rc.1"))
        self.assertEqual(hash(Version.parse("1.0.0")), hash(Version.parse("1.0.0")))
        self.assertNotEqual(Version.parse("1.0.0"), Version.parse("1.0.0-rc.1"))


class TestConstraint(unittest.TestCase):
    def test_clauses(self):
        c = VersionConstraint.parse(">=1.0.0 <2.0.0")
        self.assertTrue(c.allows(Version.parse("1.5.0")))
        self.assertFalse(c.allows(Version.parse("2.0.0")))
        self.assertFalse(c.allows(Version.parse("0.9.9")))

    def test_bare_means_exact(self):
        c = VersionConstraint.parse("1.2.3")
        self.assertTrue(c.allows(Version.parse("1.2.3")))
        self.assertFalse(c.allows(Version.parse("1.2.4")))

    def test_wildcard_and_prerelease(self):
        self.assertTrue(VersionConstraint.parse("*").allows(Version.parse("9.9.9-rc.1")))
        c = VersionConstraint.parse(">=1.0.0-alpha")
        self.assertTrue(c.allows(Version.parse("1.0.0")))
        self.assertTrue(c.allows(Version.parse("1.0.0-alpha")))  # equal bound allowed
        self.assertFalse(c.allows(Version.parse("0.9.0")))


if __name__ == "__main__":
    unittest.main()
