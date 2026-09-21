import unittest

from ci_state.version import Version, VersionError


class VersionParsingTest(unittest.TestCase):
    def test_parses_core_and_prerelease(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertFalse(v.is_prerelease)

        pre = Version.parse("1.2.3-rc.1")
        self.assertTrue(pre.is_prerelease)
        self.assertEqual(pre.prerelease, ["rc", 1])

    def test_build_metadata_ignored_for_precedence(self):
        self.assertEqual(Version.parse("1.0.0+abc"), Version.parse("1.0.0+def"))

    def test_rejects_bad_versions(self):
        for bad in ["1.2", "01.2.3", "1.2.3-", "v1.2.3", "1.2.x", "1.2.3.4", ""]:
            with self.subTest(bad=bad):
                with self.assertRaises(VersionError):
                    Version.parse(bad)

    def test_release_outranks_prerelease(self):
        self.assertGreater(Version.parse("1.0.0"), Version.parse("1.0.0-rc.1"))

    def test_prerelease_semver_ordering(self):
        ordered = [
            "1.0.0-alpha",
            "1.0.0-alpha.1",
            "1.0.0-alpha.beta",
            "1.0.0-beta",
            "1.0.0-beta.2",
            "1.0.0-beta.11",
            "1.0.0-rc.1",
            "1.0.0",
        ]
        parsed = [Version.parse(x) for x in ordered]
        self.assertEqual(parsed, sorted(parsed))

    def test_core_version_ordering(self):
        self.assertLess(Version.parse("1.9.9"), Version.parse("1.10.0"))
        self.assertLess(Version.parse("2.0.0-alpha"), Version.parse("2.0.0"))

    def test_round_trip_str(self):
        for text in ["1.2.3", "2.0.0-rc.1+build.42"]:
            self.assertEqual(str(Version.parse(text)), text)


if __name__ == "__main__":
    unittest.main()
