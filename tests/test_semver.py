import unittest

from scheduler.semver import Version


class TestParse(unittest.TestCase):
    def test_plain_version(self):
        self.assertEqual(Version.parse("1.2.3"), Version(1, 2, 3, ()))

    def test_prerelease(self):
        version = Version.parse("1.4.0-rc.1")
        self.assertEqual(version.prerelease, ("rc", "1"))

    def test_prerelease_with_hyphen_and_digits(self):
        version = Version.parse("0.0.1-x-y-z.--.1a")
        self.assertEqual(version.prerelease, ("x-y-z", "--", "1a"))

    def test_str_roundtrip(self):
        for text in ("1.2.3", "0.0.0", "10.20.30-alpha.1", "1.0.0-1a"):
            self.assertEqual(str(Version.parse(text)), text)

    def test_rejects_invalid(self):
        for bad in (
            "", "1.2", "1.2.3.4", "v1.2.3", "1.02.3", "1.2.3-",
            "1.2.3-alpha..1", "1.2.3-01", "1.2.3-alpha_1", "a.b.c",
        ):
            with self.subTest(bad=bad):
                with self.assertRaises(ValueError):
                    Version.parse(bad)

    def test_rejects_non_string(self):
        with self.assertRaises(ValueError):
            Version.parse(None)
        with self.assertRaises(ValueError):
            Version.parse(123)


class TestOrdering(unittest.TestCase):
    def test_semver_org_chain(self):
        chain = [
            "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta",
            "1.0.0-beta", "1.0.0-beta.2", "1.0.0-beta.11",
            "1.0.0-rc.1", "1.0.0",
        ]
        versions = [Version.parse(text) for text in chain]
        self.assertEqual(versions, sorted(reversed(versions)))
        for earlier, later in zip(versions, versions[1:]):
            self.assertLess(earlier, later)

    def test_numeric_fields(self):
        self.assertLess(Version.parse("1.9.9"), Version.parse("1.10.0"))
        self.assertLess(Version.parse("2.0.0"), Version.parse("10.0.0"))

    def test_release_after_prerelease(self):
        self.assertLess(Version.parse("1.0.0-rc.9"), Version.parse("1.0.0"))


if __name__ == "__main__":
    unittest.main()
