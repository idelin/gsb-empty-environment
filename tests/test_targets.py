import unittest

from vsets import Version, parse_spec
from vsets.target import ConditionalVersionSet


class ConditionalSetTest(unittest.TestCase):
    def test_branch_dictionary_and_list_parse_identically(self):
        via_dict = ConditionalVersionSet.parse({
            "os=linux,libc=glibc": "[1.0.0,2.0.0)",
        })
        via_list = ConditionalVersionSet.parse({
            "branches": [
                {"when": "os=linux,libc=glibc", "set": "[1.0.0,2.0.0)"},
            ]
        })
        self.assertEqual(via_dict.serialize(), via_list.serialize())

    def test_first_matching_branch_wins_and_otherwise_empty(self):
        conditional = ConditionalVersionSet.parse({
            "branches": [
                {"when": "os=linux", "set": "[1.0.0,2.0.0)"},
                {"when": "os=mac", "set": "[2.0.0,3.0.0)"},
            ]
        })
        self.assertIn(Version.parse("1.5.0"), conditional.evaluate({"os": "linux"}))
        self.assertIn(Version.parse("2.5.0"), conditional.evaluate({"os": "mac"}))
        self.assertNotIn(Version.parse("1.0.0"), conditional.evaluate({"os": "win"}))

    def test_conjunction_of_conditionals_intersects_sets(self):
        left = ConditionalVersionSet.parse({"os=linux": "[1.0.0,3.0.0)"})
        right = ConditionalVersionSet.parse({"libc=glibc": "[2.0.0,4.0.0)"})
        combined = left & right
        active = combined.evaluate({"os": "linux", "libc": "glibc"})
        self.assertIn(Version.parse("2.5.0"), active)
        self.assertNotIn(Version.parse("1.5.0"), active)
        self.assertTrue(combined.evaluate({"os": "linux"}).is_empty())
        self.assertTrue(combined.evaluate({"libc": "glibc"}).is_empty())

    def test_canonical_form_does_not_depend_on_branch_order(self):
        a = ConditionalVersionSet.parse({
            "branches": [
                {"when": "os=linux", "set": "[1.0.0,2.0.0)"},
                {"when": "os=mac", "set": "[1.0.0,2.0.0)"},
            ]
        })
        b = ConditionalVersionSet.parse({
            "branches": [
                {"when": "os=mac", "set": "[1.0.0,2.0.0)"},
                {"when": "os=linux", "set": "[1.0.0,2.0.0)"},
            ]
        })
        self.assertEqual(a.serialize(), b.serialize())
        self.assertEqual(a.digest(), b.digest())
        self.assertIn("[1.0.0,2.0.0)", a.serialize())
        self.assertIn("os=linux", a.serialize())

    def test_unconditional_spec_stays_single_branch(self):
        conditional = ConditionalVersionSet.parse(">=1.0.0 & <2.0.0")
        self.assertEqual(len(conditional.branches), 1)
        self.assertEqual(conditional.evaluate({}), parse_spec("[1.0.0,2.0.0)"))


if __name__ == "__main__":
    unittest.main()
