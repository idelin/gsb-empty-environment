import unittest

from buildcache.version import Version, VersionError


class VersionTests(unittest.TestCase):
    def test_parse_release_and_prerelease(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertFalse(v.is_prerelease)
        self.assertEqual(str(v), "1.2.3")

        pre = Version.parse("2.0.0-rc.1")
        self.assertEqual(pre.prerelease, ("rc", "1"))
        self.assertTrue(pre.is_prerelease)
        self.assertEqual(str(pre), "2.0.0-rc.1")

    def test_invalid_versions(self):
        for bad in ("1.2", "1.2.3.4", "01.2.3", "1.2.x",
                    "1.2.3-", "1.2.3-rc!", "", "v1.2.3"):
            with self.subTest(bad=bad):
                with self.assertRaises(VersionError):
                    Version.parse(bad)

    def test_release_beats_prerelease(self):
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0"))
        self.assertGreater(Version.parse("1.0.0"), Version.parse("1.0.0-beta.1"))

    def test_prerelease_ordering(self):
        ordered = [
            "1.0.0-alpha",
            "1.0.0-alpha.1",
            "1.0.0-alpha.2",
            "1.0.0-alpha.10",
            "1.0.0-beta",
            "1.0.0-rc.1",
            "1.0.0",
        ]
        parsed = [Version.parse(v) for v in ordered]
        self.assertEqual(parsed, sorted(parsed))

    def test_release_ordering(self):
        self.assertLess(Version.parse("1.2.9"), Version.parse("1.2.10"))
        self.assertLess(Version.parse("1.9.9"), Version.parse("2.0.0"))
        self.assertEqual(Version.parse("3.4.5"), Version.parse("3.4.5"))


if __name__ == "__main__":
    unittest.main()
