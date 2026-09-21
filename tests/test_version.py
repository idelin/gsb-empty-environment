import unittest

from abicompat.version import Version, bump_kind


class VersionTest(unittest.TestCase):
    def test_parse_core_and_prerelease(self):
        v = Version.parse("1.2.3-rc.1+build.7")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertEqual(v.prerelease, ("rc", 1))
        self.assertTrue(v.is_prerelease)
        self.assertEqual(v.build, "build.7")
        self.assertEqual(str(v), "1.2.3-rc.1+build.7")

    def test_ordering_semver_examples(self):
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
        versions = [Version.parse(text) for text in ordered]
        self.assertEqual(versions, sorted(versions))
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0"))

    def test_build_metadata_ignored_for_precedence(self):
        self.assertEqual(Version.parse("1.0.0+x"), Version.parse("1.0.0+y"))

    def test_invalid_versions(self):
        for bad in ("1", "1.2", "01.2.3", "1.2.3-01", "v1.2.3", "1.2.3-"):
            with self.subTest(bad=bad):
                with self.assertRaises(ValueError):
                    Version.parse(bad)

    def test_bump_kind(self):
        self.assertEqual(
            bump_kind(Version.parse("1.2.3"), Version.parse("2.0.0")), "major"
        )
        self.assertEqual(
            bump_kind(Version.parse("1.2.3"), Version.parse("1.3.0")), "minor"
        )
        self.assertEqual(
            bump_kind(Version.parse("1.2.3"), Version.parse("1.2.4")), "patch"
        )
        self.assertEqual(
            bump_kind(Version.parse("1.2.3-rc.1"), Version.parse("1.2.3-rc.2")),
            "prerelease",
        )
        self.assertEqual(
            bump_kind(Version.parse("1.2.3-rc.1"), Version.parse("1.2.3")), "patch"
        )
        self.assertEqual(
            bump_kind(Version.parse("1.2.3"), Version.parse("1.2.3")), "none"
        )


if __name__ == "__main__":
    unittest.main()
