import sys
import unittest
from pathlib import Path

sys.path.insert(0, str(Path(__file__).resolve().parent.parent))

from license_solver import Version, VersionRange


class VersionTest(unittest.TestCase):
    def test_parse_and_str_roundtrip(self):
        v = Version.parse("1.2.3-rc.1")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertEqual(v.prerelease, ("rc", "1"))
        self.assertEqual(str(v), "1.2.3-rc.1")
        self.assertEqual(str(Version.parse("0.0.1")), "0.0.1")

    def test_invalid_versions_rejected(self):
        for bad in ["1.2", "1.2.3.4", "v1.2.3", "1.02.3", "", "1.2.x"]:
            with self.assertRaises(ValueError, msg=bad):
                Version.parse(bad)

    def test_semver_prerelease_ordering(self):
        ordered = [
            "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
            "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
        ]
        versions = [Version.parse(s) for s in ordered]
        for earlier, later in zip(versions, versions[1:]):
            self.assertLess(earlier, later)
            self.assertGreater(later, earlier)

    def test_core_ordering(self):
        self.assertLess(Version.parse("1.2.3"), Version.parse("1.10.0"))
        self.assertLess(Version.parse("0.9.9"), Version.parse("1.0.0-alpha"))

    def test_ranges(self):
        r = VersionRange.parse(">=1.0.0 <2.0.0")
        self.assertTrue(r.contains(Version.parse("1.0.0")))
        self.assertTrue(r.contains(Version.parse("1.9.9")))
        self.assertFalse(r.contains(Version.parse("2.0.0")))
        self.assertFalse(r.contains(Version.parse("0.9.0")))

    def test_caret_exact_any(self):
        self.assertTrue(VersionRange.parse("^1.2.0").contains(Version.parse("1.9.0")))
        self.assertFalse(VersionRange.parse("^1.2.0").contains(Version.parse("2.0.0")))
        self.assertFalse(VersionRange.parse("^1.2.0").contains(Version.parse("1.1.9")))
        self.assertTrue(VersionRange.parse("1.2.3").contains(Version.parse("1.2.3")))
        self.assertFalse(VersionRange.parse("1.2.3").contains(Version.parse("1.2.4")))
        self.assertTrue(VersionRange.parse("*").contains(Version.parse("9.9.9-rc.1")))

    def test_invalid_range_rejected(self):
        with self.assertRaises(ValueError):
            VersionRange.parse("=>1.0.0")


if __name__ == "__main__":
    unittest.main()
