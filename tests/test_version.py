import unittest

from feature_resolver.errors import VersionError
from feature_resolver.version import Requirement, Version


class VersionParseTest(unittest.TestCase):
    def test_plain_version(self):
        version = Version.parse("1.2.3")
        self.assertEqual((version.major, version.minor, version.patch), (1, 2, 3))
        self.assertEqual(version.prerelease, ())
        self.assertEqual(str(version), "1.2.3")

    def test_prerelease_roundtrip(self):
        version = Version.parse("0.0.7-alpha.1")
        self.assertEqual(version.prerelease, ("alpha", "1"))
        self.assertEqual(str(version), "0.0.7-alpha.1")

    def test_rejects_invalid_versions(self):
        for text in ["", "1.2", "1.2.3.4", "01.2.3", "1.2.3-", "1.2.3-a..b", "v1.2.3"]:
            with self.subTest(text=text):
                with self.assertRaises(VersionError):
                    Version.parse(text)

    def test_rejects_leading_zero_prerelease_number(self):
        with self.assertRaises(VersionError):
            Version.parse("1.0.0-01")


class VersionOrderingTest(unittest.TestCase):
    def test_semver_org_ordering_chain(self):
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
        for lower, higher in zip(versions, versions[1:]):
            with self.subTest(lower=lower, higher=higher):
                self.assertLess(lower, higher)
                self.assertGreater(higher, lower)
                self.assertNotEqual(lower, higher)

    def test_core_version_ordering(self):
        self.assertLess(Version.parse("1.9.9"), Version.parse("1.10.0"))
        self.assertLess(Version.parse("1.0.0"), Version.parse("2.0.0-alpha"))


class RequirementTest(unittest.TestCase):
    def test_wildcard_matches_anything(self):
        req = Requirement.parse("*")
        self.assertTrue(req.matches(Version.parse("0.0.1")))
        self.assertTrue(req.matches(Version.parse("9.9.9-rc.1")))

    def test_bare_version_means_exact(self):
        req = Requirement.parse("1.2.3")
        self.assertTrue(req.matches(Version.parse("1.2.3")))
        self.assertFalse(req.matches(Version.parse("1.2.4")))

    def test_conjunction(self):
        req = Requirement.parse(">=1.0.0, <2.0.0")
        self.assertTrue(req.matches(Version.parse("1.5.0")))
        self.assertFalse(req.matches(Version.parse("2.0.0")))
        self.assertFalse(req.matches(Version.parse("0.9.9")))

    def test_all_operators(self):
        cases = [
            ("==1.2.3", "1.2.3", True),
            ("!=1.2.3", "1.2.4", True),
            ("<1.2.3", "1.2.2", True),
            ("<=1.2.3", "1.2.3", True),
            (">1.2.3", "1.2.4", True),
            (">=1.2.3", "1.2.3", True),
            ("=1.2.3", "1.2.3", True),
            (">=2.0.0-alpha", "2.0.0", True),
            (">=2.0.0", "2.0.0-alpha", False),
        ]
        for req_text, version_text, expected in cases:
            with self.subTest(req=req_text, version=version_text):
                self.assertIs(
                    Requirement.parse(req_text).matches(Version.parse(version_text)),
                    expected,
                )

    def test_invalid_requirement(self):
        with self.assertRaises(VersionError):
            Requirement.parse(">=1.2")


if __name__ == "__main__":
    unittest.main()
