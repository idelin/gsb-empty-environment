"""CLI smoke tests: solve / check / enumerate / demo on the bundled
synthetic example files."""

import io
import json
import os
import unittest
from contextlib import redirect_stdout

from push_suppress.cli import main

EXAMPLES = os.path.join(os.path.dirname(__file__), "..", "examples")
INSTANCE = os.path.join(EXAMPLES, "small_instance.json")
GOOD_SCHEDULE = os.path.join(EXAMPLES, "schedule_example.json")
BAD_SCHEDULE = os.path.join(EXAMPLES, "infeasible_schedule.json")


def run_cli(argv):
    buf = io.StringIO()
    with redirect_stdout(buf):
        code = main(argv)
    return code, buf.getvalue()


class CliTest(unittest.TestCase):
    def test_solve_example(self):
        code, out = run_cli(["solve", INSTANCE])
        self.assertEqual(code, 0)
        result = json.loads(out)
        self.assertTrue(result["optimal"])
        self.assertEqual(result["violations"], [])
        self.assertEqual(result["profit"], 13.0)
        self.assertEqual(
            result["sends"],
            [{"id": "n1", "time": 0}, {"id": "n3", "time": 3},
             {"id": "n4", "time": 1}],
        )

    def test_check_feasible_schedule(self):
        code, out = run_cli(["check", INSTANCE, GOOD_SCHEDULE])
        self.assertEqual(code, 0)
        result = json.loads(out)
        self.assertTrue(result["feasible"])
        self.assertEqual(result["profit"], 13.0)

    def test_check_infeasible_schedule(self):
        code, out = run_cli(["check", INSTANCE, BAD_SCHEDULE])
        self.assertEqual(code, 1)
        result = json.loads(out)
        self.assertFalse(result["feasible"])
        text = " ".join(result["violations"])
        self.assertIn("window", text)      # rolling window: n1@0 vs n2@2
        self.assertIn("mutex", text)       # n1 and n2 share campA
        self.assertIn("capacity", text)    # n1@0 and n4@0 both email

    def test_enumerate_example(self):
        code, out = run_cli(["enumerate", INSTANCE])
        self.assertEqual(code, 0)
        result = json.loads(out)
        self.assertFalse(result["truncated"])
        self.assertEqual(result["optimal_profit"], 13.0)
        self.assertGreater(result["feasible_schedule_count"], 100)
        optimals = [s for s in result["schedules"] if s["is_optimal"]]
        self.assertEqual(len(optimals), 1, "tie-break picks exactly one")

    def test_enumerate_limit(self):
        code, out = run_cli(["enumerate", INSTANCE, "--limit", "5"])
        self.assertEqual(code, 0)
        result = json.loads(out)
        self.assertTrue(result["truncated"])
        self.assertEqual(len(result["schedules"]), 5)

    def test_demo(self):
        code, out = run_cli(["demo"])
        self.assertEqual(code, 0)
        self.assertIn("optimal profit: 13.0", out)
        self.assertIn("<-- optimal", out)


if __name__ == "__main__":
    unittest.main()
