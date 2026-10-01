import os
import unittest

from invcheck import (InvariantChecker, cross_check, load_model,
                      reference_check)

EXAMPLES = os.path.join(os.path.dirname(os.path.dirname(
    os.path.abspath(__file__))), "examples")
ALL_EXAMPLES = sorted(f for f in os.listdir(EXAMPLES) if f.endswith(".json"))


class TestReference(unittest.TestCase):
    def test_reference_verdicts(self):
        expected = {
            "mod3_counter.json": "PASS",
            "mod5_unreachable_step.json": "FAIL_STEP",
            "mod3_bad_invariant.json": "FAIL_STEP",
            "base_fail.json": "FAIL_BASE",
            "empty_init.json": "PASS",
            "next_functions.json": "PASS",
        }
        for name, verdict in expected.items():
            ref = reference_check(load_model(os.path.join(EXAMPLES, name)))
            self.assertEqual(ref["verdict"], verdict, name)

    def test_reference_mod5_unreachable(self):
        ref = reference_check(load_model(os.path.join(
            EXAMPLES, "mod5_unreachable_step.json")))
        self.assertFalse(ref["step_cex_reachable"])
        self.assertTrue(ref["holds_on_reachable"])
        self.assertEqual(ref["reachable_states"], 5)

    def test_cross_check_all_examples(self):
        for name in ALL_EXAMPLES:
            model = load_model(os.path.join(EXAMPLES, name))
            result = InvariantChecker(model).check()
            mismatches = cross_check(model, result)
            self.assertEqual(mismatches, [], f"{name}: {mismatches}")

    def test_large_domain_skipped(self):
        model = {
            "name": "big",
            "variables": ["v%d" % i for i in range(20)],
            "init": "1",
            "invariant": "1",
            "transition": {"relation": "1"},
        }
        self.assertIsNone(reference_check(model, max_states=4096))


if __name__ == "__main__":
    unittest.main()
