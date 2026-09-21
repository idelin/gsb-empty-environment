import unittest

from buildcache.version import InvalidVersion, Version


class VersionTests(unittest.TestCase):
    def test_parse_shapes(self):
        self.assertEqual(str(Version.parse(1)), "1.0.0")
        self.assertEqual(str(Version.parse("1.2")), "1.2.0")
        self.assertEqual(str(Version.parse("1.2.3")), "1.2.3")
        self.assertEqual(str(Version.parse("0.0.0")), "0.0.0")

    def test_prerelease(self):
        v = Version.parse("2.0.0-beta.1")
        self.assertTrue(v.is_prerelease)
        self.assertEqual(v.prerelease, "beta.1")
        self.assertEqual(Version(2, 0, 0, "rc.2"), Version.parse("2.0.0-rc.2"))

    def test_prerelease_orders_below_release(self):
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0"))
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0-alpha.1"))
        self.assertLess(Version.parse("1.0.0-alpha.1"), Version.parse("1.0.0-alpha.beta"))
        self.assertLess(Version.parse("1.0.0-alpha.beta"), Version.parse("1.0.0-beta"))
        self.assertLess(Version.parse("1.0.0-beta"), Version.parse("1.0.0-beta.2"))
        self.assertLess(Version.parse("1.0.0-beta.2"), Version.parse("1.0.0-beta.11"))
        self.assertLess(Version.parse("1.0.0-beta.11"), Version.parse("1.0.0-rc.1"))
        self.assertLess(Version.parse("1.0.0-rc.1"), Version.parse("1.0.0"))

    def test_core_ordering(self):
        self.assertLess(Version.parse("1.9.9"), Version.parse("2.0.0"))
        self.assertEqual(Version.parse("1.2.3"), Version(1, 2, 3))
        self.assertEqual(hash(Version.parse("1.2.3")), hash(Version(1, 2, 3)))

    def test_invalid(self):
        for bad in ("01.2.3", "1.02.3", "1.2.x", "", "v1.2.3", "1.2.3-", "1.2.3-rc..1", True):
            with self.subTest(bad=bad):
                with self.assertRaises(InvalidVersion):
                    Version.parse(bad)


if __name__ == "__main__":
    unittest.main()
