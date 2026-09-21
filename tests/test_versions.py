import unittest

from build_state.versions import Version, VersionError, compare_prerelease, parse_version


class VersionParsingTests(unittest.TestCase):
    def test_parse_core(self):
        v = parse_version("10.2.3")
        self.assertEqual(v, Version(10, 2, 3))
        self.assertFalse(v.is_prerelease)
        self.assertEqual(str(v), "10.2.3")

    def test_parse_prerelease(self):
        v = parse_version("2.0.0-rc.1")
        self.assertEqual(v.prerelease, ("rc", "1"))
        self.assertTrue(v.is_prerelease)
        self.assertEqual(str(v), "2.0.0-rc.1")

    def test_rejects_bad_shapes(self):
        for bad in ["1.2", "1", "1.2.3.4", "v1.2.3", "01.2.3", "1.2.x",
                    "1.2.3-", "1.2.3-alpha..1", "", "  ", "1.2.3-beta_1"]:
            with self.subTest(bad=bad):
                with self.assertRaises(VersionError):
                    parse_version(bad)

    def test_rejects_leading_zero_prerelease_numeric(self):
        with self.assertRaises(VersionError):
            parse_version("1.0.0-01")

    def test_non_string_rejected(self):
        with self.assertRaises(VersionError):
            parse_version(1.2)  # type: ignore[arg-type]


class VersionOrderingTests(unittest.TestCase):
    def test_core_order(self):
        self.assertLess(parse_version("1.0.0"), parse_version("1.0.1"))
        self.assertLess(parse_version("1.0.9"), parse_version("1.1.0"))
        self.assertLess(parse_version("1.99.99"), parse_version("2.0.0"))

    def test_release_beats_prerelease_on_same_core(self):
        self.assertLess(parse_version("1.0.0-alpha"), parse_version("1.0.0"))
        self.assertGreater(parse_version("2.0.0"), parse_version("2.0.0-rc.2"))

    def test_semver_prerelease_examples(self):
        sequence = [
            "1.0.0-alpha",
            "1.0.0-alpha.1",
            "1.0.0-alpha.beta",
            "1.0.0-beta",
            "1.0.0-beta.2",
            "1.0.0-beta.11",
            "1.0.0-rc.1",
            "1.0.0",
        ]
        parsed = [parse_version(v) for v in sequence]
        self.assertEqual(parsed, sorted(parsed))

    def test_compare_prerelease_helper(self):
        self.assertEqual(compare_prerelease(("a",), ("a",)), 0)
        self.assertLess(compare_prerelease(("a",), ("a", "1")), 0)
        # Numeric identifiers rank below alphanumeric ones.
        self.assertLess(compare_prerelease(("1",), ("alpha")), 0)

    def test_equality_and_booleans(self):
        self.assertEqual(parse_version("1.2.3-rc.1"), parse_version("1.2.3-rc.1"))
        self.assertTrue(parse_version("1.2.3") >= parse_version("1.2.3"))
        self.assertTrue(parse_version("1.2.3") <= parse_version("1.2.3"))


if __name__ == "__main__":
    unittest.main()
