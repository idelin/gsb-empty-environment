import unittest

from rebuildplanner.version import (
    Version,
    VersionError,
    parse_constraints,
    satisfies_all,
)


class VersionTest(unittest.TestCase):
    def test_parse_and_str(self):
        self.assertEqual(str(Version.parse("1.2.3")), "1.2.3")
        self.assertEqual(str(Version.parse("1.0.0-rc.1+build.7")), "1.0.0-rc.1")

    def test_invalid_versions(self):
        for bad in ["1.2", "1.2.x", "01.0.0", "1.2.3-", "v1.2.3", ""]:
            with self.subTest(bad=bad):
                with self.assertRaises(VersionError):
                    Version.parse(bad)

    def test_prerelease_ordering(self):
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
        parsed = [Version.parse(v) for v in ordered]
        self.assertEqual(parsed, sorted(parsed))

    def test_core_ordering(self):
        self.assertLess(Version.parse("1.9.9"), Version.parse("1.10.0"))
        self.assertLess(Version.parse("0.9.9"), Version.parse("1.0.0"))

    def test_caret_constraint(self):
        cs = parse_constraints("^1.2.3")
        self.assertTrue(satisfies_all(Version.parse("1.9.0"), cs))
        self.assertFalse(satisfies_all(Version.parse("2.0.0"), cs))
        self.assertFalse(satisfies_all(Version.parse("1.2.2"), cs))
        zero = parse_constraints("^0.2.3")
        self.assertTrue(satisfies_all(Version.parse("0.2.9"), zero))
        self.assertFalse(satisfies_all(Version.parse("0.3.0"), zero))
        strict_zero = parse_constraints("^0.0.3")
        self.assertFalse(satisfies_all(Version.parse("0.0.4"), strict_zero))

    def test_tilde_constraint(self):
        cs = parse_constraints("~1.2.3")
        self.assertTrue(satisfies_all(Version.parse("1.2.9"), cs))
        self.assertFalse(satisfies_all(Version.parse("1.3.0"), cs))

    def test_operators_and_comma(self):
        cs = parse_constraints(">=1.0.0, <2.0.0, !=1.5.0")
        self.assertTrue(satisfies_all(Version.parse("1.4.0"), cs))
        self.assertFalse(satisfies_all(Version.parse("1.5.0"), cs))
        self.assertFalse(satisfies_all(Version.parse("2.0.0"), cs))

    def test_prerelease_gate(self):
        cs = parse_constraints(">=1.0.0")
        self.assertFalse(satisfies_all(Version.parse("1.1.0-rc.1"), cs))
        cs2 = parse_constraints(">=1.0.0-beta.1")
        self.assertTrue(satisfies_all(Version.parse("1.1.0-rc.1"), cs2))


if __name__ == "__main__":
    unittest.main()
