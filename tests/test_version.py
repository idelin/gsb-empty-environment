import unittest

from depsolver.version import Version, VersionError


class TestVersionParse(unittest.TestCase):
    def test_parse_simple(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertEqual(v.prerelease, ())
        self.assertEqual(str(v), "1.2.3")

    def test_parse_prerelease(self):
        v = Version.parse("1.2.3-alpha.1")
        self.assertEqual(v.prerelease, ("alpha", 1))
        self.assertEqual(str(v), "1.2.3-alpha.1")

    def test_parse_rejects_garbage(self):
        for bad in ["1.2", "1.2.3.4", "v1.2.3", "1.02.3", "1.2.3-", ""]:
            with self.assertRaises(VersionError, msg=bad):
                Version.parse(bad)


class TestVersionOrdering(unittest.TestCase):
    def test_base_ordering(self):
        self.assertLess(Version.parse("1.2.3"), Version.parse("1.2.10"))
        self.assertLess(Version.parse("1.2.3"), Version.parse("1.3.0"))
        self.assertLess(Version.parse("1.9.9"), Version.parse("2.0.0"))

    def test_prerelease_before_release(self):
        self.assertLess(Version.parse("1.2.3-alpha"), Version.parse("1.2.3"))

    def test_prerelease_semver_order(self):
        order = [
            "1.0.0-alpha",
            "1.0.0-alpha.1",
            "1.0.0-alpha.2",
            "1.0.0-alpha.beta",
            "1.0.0-beta",
            "1.0.0-beta.2",
            "1.0.0-beta.11",
            "1.0.0-rc.1",
            "1.0.0",
        ]
        versions = [Version.parse(v) for v in order]
        for earlier, later in zip(versions, versions[1:]):
            self.assertLess(earlier, later)

    def test_equality_and_hash(self):
        self.assertEqual(Version.parse("1.2.3"), Version.parse("1.2.3"))
        self.assertEqual(hash(Version.parse("1.2.3")), hash(Version.parse("1.2.3")))
        self.assertNotEqual(
            Version.parse("1.2.3-alpha"), Version.parse("1.2.3-beta")
        )


if __name__ == "__main__":
    unittest.main()
