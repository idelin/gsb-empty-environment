import unittest

from buildplanner.semver import Version, VersionError


class TestParse(unittest.TestCase):
    def test_plain_release(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertEqual(v.prerelease, ())
        self.assertEqual(str(v), "1.2.3")

    def test_prerelease(self):
        v = Version.parse("2.0.0-rc.1")
        self.assertEqual(v.prerelease, ("rc", "1"))
        self.assertEqual(str(v), "2.0.0-rc.1")

    def test_rejects_incomplete_and_malformed(self):
        for bad in ["1.2", "1.2.3.4", "v1.2.3", "1.02.3", "1.2.x", "", "1.2.3-"]:
            with self.assertRaises(VersionError, msg=bad):
                Version.parse(bad)

    def test_rejects_numeric_prerelease_leading_zero(self):
        with self.assertRaises(VersionError):
            Version.parse("1.0.0-01")

    def test_rejects_non_string(self):
        with self.assertRaises(VersionError):
            Version.parse(123)


class TestOrdering(unittest.TestCase):
    def test_release_outranks_prerelease(self):
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0"))

    def test_semver_org_ordering_chain(self):
        chain = [
            "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta",
            "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11",
            "1.0.0-rc.1", "1.0.0",
        ]
        versions = [Version.parse(c) for c in chain]
        for earlier, later in zip(versions, versions[1:]):
            self.assertLess(earlier, later)

    def test_core_precedence(self):
        self.assertLess(Version.parse("1.9.9"), Version.parse("1.10.0"))
        self.assertLess(Version.parse("2.0.0"), Version.parse("10.0.0"))


if __name__ == "__main__":
    unittest.main()
