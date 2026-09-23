import unittest

from lockmerge.version import Version


class TestVersionParse(unittest.TestCase):
    def test_plain_version(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertIsNone(v.pre)
        self.assertFalse(v.is_prerelease)
        self.assertEqual(str(v), "1.2.3")

    def test_prerelease(self):
        v = Version.parse("1.5.0-rc1")
        self.assertEqual(v.pre, ("rc", 1))
        self.assertTrue(v.is_prerelease)
        self.assertEqual(str(v), "1.5.0-rc1")

    def test_prerelease_aliases(self):
        for tag in ("a1", "alpha2", "b3", "beta4", "rc5"):
            self.assertTrue(Version.parse(f"2.0.0-{tag}").is_prerelease)

    def test_invalid_versions(self):
        for bad in ("1.2", "1.2.3.4", "a.b.c", "1.2.-3", "1.2.3-", "1.2.3-x1", 12):
            with self.assertRaises(ValueError, msg=bad):
                Version.parse(bad)


class TestVersionOrdering(unittest.TestCase):
    def test_numeric_ordering(self):
        self.assertLess(Version.parse("1.9.9"), Version.parse("1.10.0"))
        self.assertLess(Version.parse("0.9.9"), Version.parse("1.0.0"))
        self.assertGreater(Version.parse("2.0.1"), Version.parse("2.0.0"))

    def test_prerelease_before_final(self):
        self.assertLess(Version.parse("1.5.0-rc1"), Version.parse("1.5.0"))
        self.assertLess(Version.parse("1.5.0-a1"), Version.parse("1.5.0-b1"))
        self.assertLess(Version.parse("1.5.0-b1"), Version.parse("1.5.0-rc1"))
        self.assertLess(Version.parse("1.5.0-rc1"), Version.parse("1.5.0-rc2"))

    def test_equality_and_hash(self):
        self.assertEqual(Version.parse("1.2.3"), Version.parse("1.2.3"))
        self.assertEqual(hash(Version.parse("1.2.3")), hash(Version.parse("1.2.3")))
        self.assertNotEqual(Version.parse("1.2.3"), Version.parse("1.2.3-rc1"))

    def test_sorted(self):
        versions = [Version.parse(s) for s in
                    ("2.0.0", "1.5.0", "1.5.0-rc1", "1.4.0", "1.5.0-b1")]
        self.assertEqual([str(v) for v in sorted(versions)],
                         ["1.4.0", "1.5.0-b1", "1.5.0-rc1", "1.5.0", "2.0.0"])


if __name__ == "__main__":
    unittest.main()
