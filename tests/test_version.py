import unittest

from depres import Version


class TestVersionParse(unittest.TestCase):
    def test_parse_release(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertEqual(v.prerelease, ())
        self.assertFalse(v.is_prerelease)
        self.assertEqual(str(v), "1.2.3")

    def test_parse_prerelease(self):
        v = Version.parse("2.0.0-beta.1")
        self.assertTrue(v.is_prerelease)
        self.assertEqual(v.prerelease, ("beta", "1"))
        self.assertEqual(str(v), "2.0.0-beta.1")

    def test_parse_rejects_garbage(self):
        for bad in ["1.2", "1.2.3.4", "v1.2.3", "1.02.3", ""]:
            with self.assertRaises(ValueError, msg=bad):
                Version.parse(bad)


class TestVersionOrdering(unittest.TestCase):
    def test_core_ordering(self):
        self.assertLess(Version.parse("1.2.3"), Version.parse("1.10.0"))
        self.assertLess(Version.parse("0.9.9"), Version.parse("1.0.0"))

    def test_release_beats_prerelease(self):
        self.assertLess(Version.parse("2.0.0-rc.1"), Version.parse("2.0.0"))

    def test_prerelease_identifier_rules(self):
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
        versions = [Version.parse(v) for v in ordered]
        self.assertEqual(sorted(versions), versions)


if __name__ == "__main__":
    unittest.main()
