import unittest

from lockmerge.version import Constraint, Version


class VersionParseTests(unittest.TestCase):
    def test_parse_release(self):
        v = Version.parse("1.2.3")
        self.assertEqual((v.major, v.minor, v.patch), (1, 2, 3))
        self.assertEqual(v.prerelease, ())
        self.assertEqual(str(v), "1.2.3")

    def test_parse_prerelease(self):
        v = Version.parse("2.0.0-rc.1")
        self.assertEqual(v.prerelease, ("rc", "1"))
        self.assertEqual(str(v), "2.0.0-rc.1")

    def test_rejects_garbage(self):
        for text in ("1.2", "1.2.3.4", "v1.2.3", "1.02.3", "", "1.2.x"):
            with self.assertRaises(ValueError, msg=text):
                Version.parse(text)


class VersionOrderTests(unittest.TestCase):
    def test_core_ordering(self):
        self.assertLess(Version.parse("1.2.3"), Version.parse("1.10.0"))
        self.assertLess(Version.parse("0.9.9"), Version.parse("1.0.0"))

    def test_prerelease_before_release(self):
        self.assertLess(Version.parse("1.0.0-alpha"), Version.parse("1.0.0"))

    def test_prerelease_chain(self):
        ordered = [
            "1.0.0-alpha", "1.0.0-alpha.1", "1.0.0-alpha.beta", "1.0.0-beta",
            "1.0.0-beta.2", "1.0.0-beta.11", "1.0.0-rc.1", "1.0.0",
        ]
        versions = [Version.parse(v) for v in ordered]
        self.assertEqual(versions, sorted(versions))


class ConstraintTests(unittest.TestCase):
    def test_wildcard(self):
        self.assertTrue(Constraint("*").allows(Version.parse("9.9.9")))
        self.assertTrue(Constraint("").allows(Version.parse("0.0.1")))

    def test_ranges(self):
        c = Constraint(">=1.2.0,<2.0.0")
        self.assertTrue(c.allows(Version.parse("1.2.0")))
        self.assertTrue(c.allows(Version.parse("1.9.9")))
        self.assertFalse(c.allows(Version.parse("2.0.0")))
        self.assertFalse(c.allows(Version.parse("1.1.9")))

    def test_exact_and_not_equal(self):
        self.assertTrue(Constraint("==1.0.0").allows(Version.parse("1.0.0")))
        self.assertFalse(Constraint("!=1.0.0").allows(Version.parse("1.0.0")))

    def test_prerelease_in_ranges(self):
        c = Constraint(">=1.0.0-alpha")
        self.assertTrue(c.allows(Version.parse("1.0.0")))
        self.assertFalse(c.allows(Version.parse("0.9.9")))


if __name__ == "__main__":
    unittest.main()
