import unittest

from stageplan.version import Constraint, Version, VersionError


class VersionTests(unittest.TestCase):
    def test_parse_parts_and_string(self):
        version = Version.parse("1.2.3")
        self.assertEqual((version.major, version.minor, version.patch), (1, 2, 3))
        self.assertEqual(str(version), "1.2.3")
        self.assertFalse(version.is_prerelease)

    def test_parse_prerelease(self):
        version = Version.parse("2.0.0-rc.1")
        self.assertEqual(version.prerelease, ("rc", "1"))
        self.assertTrue(version.is_prerelease)
        self.assertEqual(str(version), "2.0.0-rc.1")

    def test_prerelease_orders_below_release(self):
        self.assertLess(Version.parse("1.0.0-rc.1"), Version.parse("1.0.0"))
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0-rc.1"))
        self.assertLess(Version.parse("1.0.0-rc.1"), Version.parse("1.0.0-rc.2"))
        self.assertEqual(Version.parse("1.0.0+x"), Version.parse("1.0.0+y"))

    def test_invalid_versions(self):
        for text in ("1.2", "v1.2.3", "1.2.3.4", "01.2.3", "1.2.3-rc..1", ""):
            with self.subTest(text=text):
                with self.assertRaises(VersionError):
                    Version.parse(text)

    def test_constraints_exact_and_comparison(self):
        self.assertTrue(Constraint.parse("1.2.3").satisfies(Version.parse("1.2.3")))
        self.assertFalse(Constraint.parse("1.2.3").satisfies(Version.parse("1.2.4")))
        self.assertTrue(Constraint.parse(">=1.0.0 <2.0.0").satisfies(Version.parse("1.5.0")))
        self.assertFalse(Constraint.parse(">=1.0.0 <2.0.0").satisfies(Version.parse("2.0.0")))

    def test_caret_and_tilde_ranges(self):
        self.assertTrue(Constraint.parse("^1.2.0").satisfies(Version.parse("1.9.9")))
        self.assertFalse(Constraint.parse("^1.2.0").satisfies(Version.parse("2.0.0")))
        self.assertTrue(Constraint.parse("^0.3.1").satisfies(Version.parse("0.3.9")))
        self.assertFalse(Constraint.parse("^0.3.1").satisfies(Version.parse("0.4.0")))
        self.assertTrue(Constraint.parse("~1.2.0").satisfies(Version.parse("1.2.9")))
        self.assertFalse(Constraint.parse("~1.2.0").satisfies(Version.parse("1.3.0")))

    def test_pre_release_does_not_satisfy_stable_range_silently(self):
        # A stable range must not silently accept an earlier pre-release;
        # accepting a pre-release requires naming it explicitly.
        self.assertFalse(Constraint.parse(">=1.0.0").satisfies(Version.parse("1.0.0-rc.1")))
        self.assertTrue(
            Constraint.parse(">=1.0.0-rc.1 <1.0.0").satisfies(Version.parse("1.0.0-rc.2"))
        )
        self.assertFalse(Constraint.parse(">=1.0.0").satisfies(Version.parse("0.9.9")))


if __name__ == "__main__":
    unittest.main()
