import unittest

from buildset.conditions import Condition, evaluate

CTX = {"os": "darwin", "arch": "arm64", "stage": "build"}


class ConditionTests(unittest.TestCase):
    def test_empty_condition_always_taken(self):
        result = evaluate(Condition.from_json(None), CTX)
        self.assertTrue(result.taken)
        self.assertEqual(result.checks, ())

    def test_matching_condition(self):
        cond = Condition.from_json({"os": ["linux", "darwin"], "stage": "build"})
        result = evaluate(cond, CTX)
        self.assertTrue(result.taken)
        self.assertEqual([c.field for c in result.checks], ["os", "stage"])
        self.assertTrue(all(c.passed for c in result.checks))

    def test_failing_condition_reports_evidence(self):
        cond = Condition.from_json({"os": ["windows"], "arch": ["arm64"]})
        result = evaluate(cond, CTX)
        self.assertFalse(result.taken)
        by_field = {c.field: c for c in result.checks}
        self.assertFalse(by_field["os"].passed)
        self.assertEqual(by_field["os"].actual, "darwin")
        self.assertEqual(by_field["os"].required, ("windows",))
        self.assertTrue(by_field["arch"].passed)

    def test_single_string_normalized_to_list(self):
        cond = Condition.from_json({"stage": "test"})
        self.assertEqual(cond.stage, ("test",))

    def test_unknown_field_rejected(self):
        with self.assertRaises(ValueError):
            Condition.from_json({"kernel": ["linux"]})


if __name__ == "__main__":
    unittest.main()
