"""Unit tests for the multi-channel push suppression decision component.

All data below is synthetic and constructed by hand.
"""

from __future__ import annotations

import itertools
import json
import subprocess
import sys
import unittest
from pathlib import Path

from pushsuppress.checkers import (
    check_channel_capacity,
    check_mutex,
    check_time_window,
    check_user_rolling_limit,
    verify_schedule,
)
from pushsuppress.model import parse_instance
from pushsuppress.solver import enumerate_schedules, solve, tie_key

REPO_ROOT = Path(__file__).resolve().parents[1]


def tiny_instance(window_length=3, limit=1, horizon=4):
    data = {
        "horizon": horizon,
        "window_length": window_length,
        "max_pushes_per_window": limit,
        "channel_capacity": {"sms": 10},
        "notifications": [
            {"id": "a", "user": "u1", "channel": "sms", "revenue": 10,
             "release": 0, "deadline": horizon - 1, "cost": 1},
            {"id": "b", "user": "u1", "channel": "sms", "revenue": 8,
             "release": 0, "deadline": horizon - 1, "cost": 1},
        ],
    }
    return parse_instance(data)


class AcceptanceBoundaryTests(unittest.TestCase):
    """Acceptance rule: length-3 half-open window, slots 0 and 2 clash, 0 and 3 OK."""

    def setUp(self):
        self.instance = tiny_instance()

    def test_zero_and_two_conflict(self):
        violations = check_user_rolling_limit(self.instance, {"a": 0, "b": 2})
        self.assertEqual(len(violations), 1)
        self.assertEqual(violations[0].constraint, "user_rolling_limit")
        self.assertNotEqual(verify_schedule(self.instance, {"a": 0, "b": 2}), [])

    def test_zero_and_three_allowed(self):
        self.assertEqual(check_user_rolling_limit(self.instance, {"a": 0, "b": 3}), [])
        self.assertEqual(verify_schedule(self.instance, {"a": 0, "b": 3}), [])

    def test_two_and_five_allowed_but_two_and_four_conflict(self):
        instance = tiny_instance(horizon=6)
        self.assertEqual(check_user_rolling_limit(instance, {"a": 2, "b": 5}), [])
        self.assertNotEqual(check_user_rolling_limit(instance, {"a": 2, "b": 4}), [])

    def test_rolling_not_fixed_segments(self):
        """Two pushes distance 2 apart clash in ANY position, including ones
        that fixed bucket counting ([0,3),[3,6)) would miss."""
        instance = tiny_instance(horizon=6)
        # Fixed segments would place 1 and 3 in different buckets and accept
        # them; the rolling window [1,4) contains both and must reject them.
        self.assertNotEqual(check_user_rolling_limit(instance, {"a": 1, "b": 3}), [])
        self.assertNotEqual(check_user_rolling_limit(instance, {"a": 2, "b": 4}), [])

    def test_all_length3_windows_examined(self):
        instance = tiny_instance(horizon=6)
        for x, y in itertools.combinations(range(6), 2):
            violations = check_user_rolling_limit(instance, {"a": x, "b": y})
            clash = y - x < 3
            self.assertEqual(bool(violations), clash, f"slots {x},{y} unexpected")


class CheckerTests(unittest.TestCase):
    def test_time_window(self):
        instance = tiny_instance(horizon=4)
        self.assertEqual(check_time_window(instance, {"a": 0}), [])
        violations = check_time_window(instance, {"a": 4})
        self.assertTrue(any(v.constraint == "time_window" for v in violations))

    def test_channel_capacity_per_slot(self):
        data = {
            "horizon": 3,
            "window_length": 9,
            "max_pushes_per_window": 9,
            "channel_capacity": {"sms": 5},
            "notifications": [
                {"id": "a", "user": "u1", "channel": "sms", "revenue": 1,
                 "release": 0, "deadline": 2, "cost": 3},
                {"id": "b", "user": "u2", "channel": "sms", "revenue": 1,
                 "release": 0, "deadline": 2, "cost": 3},
            ],
        }
        instance = parse_instance(data)
        # Same slot: 6 > 5 capacity -> violation; different slots -> fine.
        self.assertNotEqual(check_channel_capacity(instance, {"a": 1, "b": 1}), [])
        self.assertEqual(check_channel_capacity(instance, {"a": 0, "b": 1}), [])

    def test_mutex(self):
        data = {
            "horizon": 2,
            "channel_capacity": {"sms": 99},
            "notifications": [
                {"id": "a", "user": "u1", "channel": "sms", "revenue": 1,
                 "release": 0, "deadline": 1, "cost": 1, "activity_id": "X"},
                {"id": "b", "user": "u2", "channel": "sms", "revenue": 1,
                 "release": 0, "deadline": 1, "cost": 1, "activity_id": "X"},
                {"id": "c", "user": "u3", "channel": "sms", "revenue": 1,
                 "release": 0, "deadline": 1, "cost": 1, "activity_id": "Y"},
            ],
        }
        instance = parse_instance(data)
        self.assertNotEqual(check_mutex(instance, {"a": 0, "b": 1}), [])
        self.assertEqual(check_mutex(instance, {"a": 0, "c": 1}), [])

    def test_unknown_id_and_slot_types(self):
        instance = tiny_instance()
        violations = verify_schedule(instance, {"ghost": 1})
        self.assertTrue(any(v.constraint == "identity" for v in violations))


class SolverTests(unittest.TestCase):
    def test_tiny_optimum_and_tie_break(self):
        instance = tiny_instance()
        result = solve(instance)
        self.assertTrue(result.proven_optimal)
        self.assertEqual(result.revenue, 18.0)
        self.assertEqual(result.schedule, {"a": 0, "b": 3})
        self.assertEqual(tie_key(instance, {"a": 0, "b": 3}), (0, 3))
        self.assertEqual(tie_key(instance, {"a": 3, "b": 0}), (3, 0))
        self.assertLess(tie_key(instance, {"a": 0, "b": 3}),
                        tie_key(instance, {"a": 3, "b": 0}))
        self.assertEqual(verify_schedule(instance, result.schedule), [])

    def test_small_example_optimum(self):
        data = json.loads((REPO_ROOT / "examples" / "small.json").read_text())
        instance = parse_instance(data)
        result = solve(instance)
        self.assertTrue(result.proven_optimal)
        self.assertEqual(result.revenue, 46.0)
        self.assertEqual(verify_schedule(instance, result.schedule), [])
        # Mutex: n5 (revenue 11) chosen, n6 (revenue 3) suppressed.
        self.assertIn("n5", result.schedule)
        self.assertNotIn("n6", result.schedule)

    def test_enumeration_matches_bruteforce(self):
        instance = tiny_instance()
        schedules = enumerate_schedules(instance)
        # Empty, a@0..3, b@0..3, plus feasible pairs (0,3) and (3,0).
        self.assertEqual(len(schedules), 11)
        for schedule in schedules:
            self.assertEqual(verify_schedule(instance, schedule), [])

    def test_solver_matches_bruteforce_on_random_instances(self):
        rng_seed = 12345
        channels = ["sms", "push"]

        def seeded_random(seed):
            state = seed
            while True:
                state = (state * 1103515245 + 12345) & 0x7FFFFFFF
                yield state / 0x7FFFFFFF

        rnd = seeded_random(rng_seed)

        for trial in range(40):
            horizon = 2 + int(next(rnd) * 3)          # 2..4
            n = 3 + int(next(rnd) * 3)                # 3..5 notifications
            window = 2 + int(next(rnd) * 3)           # 2..4
            limit = 1 + int(next(rnd) * 2)            # 1..2
            notifications = []
            for idx in range(n):
                release = int(next(rnd) * horizon)
                deadline = release + int(next(rnd) * (horizon - release))
                notifications.append({
                    "id": f"t{idx}",
                    "user": f"u{int(next(rnd) * 3)}",
                    "channel": channels[int(next(rnd) * len(channels))],
                    "revenue": int(next(rnd) * 12),
                    "release": release,
                    "deadline": deadline,
                    "cost": 1 + int(next(rnd) * 5),
                    "activity_id": (
                        f"A{int(next(rnd) * 2)}" if next(rnd) < 0.3 else None
                    ),
                })
            data = {
                "horizon": horizon,
                "window_length": window,
                "max_pushes_per_window": limit,
                "channel_capacity": {"sms": 6, "push": 6},
                "notifications": notifications,
            }
            instance = parse_instance(data)

            # Independent brute force over every (UNSENT or slot) assignment.
            domains = []
            for note in instance.notifications:
                domains.append([-1] + note.options(horizon))
            brute_best = -1.0
            brute_keys = []
            for combo in itertools.product(*domains):
                schedule = {
                    note.nid: slot
                    for note, slot in zip(instance.notifications, combo)
                    if slot >= 0
                }
                if verify_schedule(instance, schedule) == []:
                    revenue = sum(instance.by_id[i].revenue for i in schedule)
                    key = tuple(combo)
                    if revenue > brute_best:
                        brute_best = revenue
                        brute_keys = [key]
                    elif revenue == brute_best:
                        brute_keys.append(key)

            result = solve(instance, node_limit=10_000_000)
            self.assertTrue(result.proven_optimal, f"trial {trial}: search incomplete")
            self.assertEqual(result.revenue, brute_best, f"trial {trial}")
            self.assertIn(tie_key(instance, result.schedule), brute_keys)

    def test_node_limit_reports_unproven(self):
        data = json.loads((REPO_ROOT / "examples" / "small.json").read_text())
        instance = parse_instance(data)
        result = solve(instance, node_limit=1)
        self.assertFalse(result.proven_optimal)
        self.assertIn("node limit", result.reason)


class CliTests(unittest.TestCase):
    def run_cli(self, *args):
        return subprocess.run(
            [sys.executable, "-m", "pushsuppress", *args],
            cwd=REPO_ROOT,
            capture_output=True,
            text=True,
        )

    def test_solve_cli(self):
        proc = self.run_cli("solve", "-i", "examples/tiny.json")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        payload = json.loads(proc.stdout)
        self.assertTrue(payload["proven_optimal"])
        self.assertEqual(payload["revenue"], 18.0)

    def test_check_cli_exit_codes(self):
        bad = self.run_cli("check", "-i", "examples/check_invalid.json")
        good = self.run_cli("check", "-i", "examples/check_valid.json")
        self.assertEqual(bad.returncode, 1)
        self.assertFalse(json.loads(bad.stdout)["feasible"])
        self.assertEqual(good.returncode, 0)
        self.assertTrue(json.loads(good.stdout)["feasible"])

    def test_enumerate_cli(self):
        proc = self.run_cli("enumerate", "-i", "examples/tiny.json")
        self.assertEqual(proc.returncode, 0, proc.stderr)
        payload = json.loads(proc.stdout)
        self.assertEqual(payload["count"], 11)
        self.assertEqual(payload["optimal_revenue"], 18.0)


if __name__ == "__main__":
    unittest.main(verbosity=2)
