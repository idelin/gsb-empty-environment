import unittest

from buildcache.version import Version


class ParseTests(unittest.TestCase):
    def test_plain_version(self):
        version = Version.parse("1.2.3")
        self.assertEqual((version.major, version.minor, version.patch), (1, 2, 3))
        self.assertIsNone(version.prerelease)
        self.assertEqual(str(version), "1.2.3")

    def test_prerelease_version(self):
        version = Version.parse("0.9.0-rc.1")
        self.assertEqual(version.prerelease, "rc.1")
        self.assertEqual(str(version), "0.9.0-rc.1")

    def test_invalid_versions_rejected(self):
        bad = ["", "1.2", "1.2.3.4", "01.2.3", "1.2.x", "1.2.3-", "1.2.3-a..b",
               "v1.2.3", "1.2.3-alpha_1", " 1.2.3"]
        for text in bad:
            with self.subTest(text=text):
                with self.assertRaises(ValueError):
                    Version.parse(text)

    def test_non_string_rejected(self):
        with self.assertRaises(ValueError):
            Version.parse(123)


class OrderingTests(unittest.TestCase):
    def test_semver_org_chain(self):
        chain = ["1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
                 "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0"]
        versions = [Version.parse(text) for text in chain]
        for earlier, later in zip(versions, versions[1:]):
            self.assertLess(earlier, later)
            self.assertGreater(later, earlier)
        self.assertEqual(sorted(reversed(versions)), versions)

    def test_release_beats_prerelease(self):
        self.assertLess(Version.parse("2.0.0-rc.9"), Version.parse("2.0.0"))

    def test_numeric_beats_alpha_identifiers(self):
        self.assertLess(Version.parse("1.0.0-1"), Version.parse("1.0.0-alpha"))

    def test_equality_and_hash(self):
        self.assertEqual(Version.parse("1.2.3"), Version(1, 2, 3))
        self.assertEqual(hash(Version.parse("1.2.3-rc.1")), hash(Version(1, 2, 3, "rc.1")))
        self.assertNotEqual(Version.parse("1.2.3"), Version.parse("1.2.3-rc.1"))


if __name__ == "__main__":
    unittest.main()
