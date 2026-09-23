import unittest

from product_paths import ForbiddenRule, RuleSet


class RuleSetTest(unittest.TestCase):
    def test_version_bumps_and_snapshots(self):
        rules = RuleSet()
        self.assertEqual(rules.version, 0)
        v1 = rules.add_rule(ForbiddenRule("r1", "C", "clear"))
        self.assertEqual(v1, 1)
        v2 = rules.add_rule(ForbiddenRule("r2", "D", "loaded"))
        self.assertEqual(v2, 2)
        v3 = rules.remove_rule("r1")
        self.assertEqual(v3, 3)
        self.assertEqual([r.rule_id for r in rules.rules(version=0)], [])
        self.assertEqual([r.rule_id for r in rules.rules(version=1)], ["r1"])
        self.assertEqual(
            sorted(r.rule_id for r in rules.rules(version=2)), ["r1", "r2"]
        )
        self.assertEqual([r.rule_id for r in rules.rules(version=3)], ["r2"])
        self.assertEqual([r.rule_id for r in rules.rules()], ["r2"])

    def test_interval_is_left_closed_right_open(self):
        rules = RuleSet()
        rules.add_rule(ForbiddenRule("r1", "C", "clear", valid_from=2.0, valid_to=5.0))
        self.assertFalse(rules.is_forbidden("C", "clear", at=1.999))
        self.assertTrue(rules.is_forbidden("C", "clear", at=2.0))
        self.assertTrue(rules.is_forbidden("C", "clear", at=4.999))
        self.assertFalse(rules.is_forbidden("C", "clear", at=5.0))

    def test_open_ended_interval(self):
        rules = RuleSet()
        rules.add_rule(ForbiddenRule("r1", "C", "clear", valid_from=2.0))
        self.assertFalse(rules.is_forbidden("C", "clear", at=1.0))
        self.assertTrue(rules.is_forbidden("C", "clear", at=10**9))

    def test_forbidden_matches_vertex_and_state(self):
        rules = RuleSet()
        rules.add_rule(ForbiddenRule("r1", "C", "clear"))
        self.assertTrue(rules.is_forbidden("C", "clear"))
        self.assertFalse(rules.is_forbidden("C", "loaded"))
        self.assertFalse(rules.is_forbidden("D", "clear"))

    def test_versioned_query_uses_snapshot(self):
        rules = RuleSet()
        rules.add_rule(ForbiddenRule("r1", "C", "clear"))
        rules.remove_rule("r1")
        self.assertTrue(rules.is_forbidden("C", "clear", version=1))
        self.assertFalse(rules.is_forbidden("C", "clear"))

    def test_duplicate_rule_rejected(self):
        rules = RuleSet()
        rules.add_rule(ForbiddenRule("r1", "C", "clear"))
        with self.assertRaises(ValueError):
            rules.add_rule(ForbiddenRule("r1", "D", "loaded"))

    def test_remove_unknown_rule_rejected(self):
        rules = RuleSet()
        with self.assertRaises(KeyError):
            rules.remove_rule("nope")

    def test_unknown_version_rejected(self):
        rules = RuleSet()
        with self.assertRaises(ValueError):
            rules.rules(version=7)


if __name__ == "__main__":
    unittest.main()
