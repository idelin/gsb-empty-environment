import unittest

from buildstate.version import Version, VersionError


class VersionTests(unittest.TestCase):
    def test_parse_core(self):
        v = Version.parse("10.2.7")
        self.assertEqual((v.major, v.minor, v.patch), (10, 2, 7))
        self.assertFalse(v.is_prerelease)
        self.assertEqual(str(v), "10.2.7")

    def test_parse_prerelease(self):
        v = Version.parse("0.9.0-rc.2")
        self.assertTrue(v.is_prerelease)
        self.assertEqual(v.prerelease, ("rc", 2))
        self.assertEqual(str(v), "0.9.0-rc.2")

    def test_prerelease_lower_than_release(self):
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0"))
        self.assertLess(Version.parse("1.0.0-alpha.1"), Version.parse("1.0.0-alpha.beta"))
        self.assertLess(Version.parse("1.0.0-alpha.beta"), Version.parse("1.0.0-beta"))
        self.assertLess(Version.parse("1.0.0-beta"), Version.parse("1.0.0-beta.2"))
        self.assertLess(Version.parse("1.0.0-beta.2"), Version.parse("1.0.0-beta.11"))
        self.assertLess(Version.parse("1.0.0-beta.11"), Version.parse("1.0.0-rc.1"))
        self.assertLess(Version.parse("1.0.0-rc.1"), Version.parse("1.0.0"))

    def test_numeric_identifier_compares_numerically(self):
        self.assertLess(Version.parse("1.0.0-1"), Version.parse("1.0.0-2"))
        self.assertLess(Version.parse("1.0.0-1.9"), Version.parse("1.0.0-1.10"))

    def test_invalid_versions(self):
        for bad in ("1.2", "1.2.3.4", "v1.2.3", "1.x.3", "01.2.3",
                    "1.02.3", "1.2.03", "1.2.3-", "1.2.3-a..b",
                    "1.2.3-a_01", "1.2.3-01", "", "1.2.3+build"):
            with self.subTest(bad=bad):
                with self.assertRaises(VersionError):
                    Version.parse(bad)

    def test_equality_and_order(self):
        self.assertEqual(Version.parse("2.4.1"), Version.parse("2.4.1"))
        self.assertNotEqual(Version.parse("2.4.1"), Version.parse("2.4.2"))
        self.assertLess(Version.parse("2.4.1"), Version.parse("2.5.0"))


if __name__ == "__main__":
    unittest.main()
