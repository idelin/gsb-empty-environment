"""Tests for the constraint re-checkers, especially the rolling-window
acceptance boundary: with L=3, K=1, sends at t=0 and t=2 conflict,
sends at t=0 and t=3 are allowed, and the rule is a true sliding
window (not fixed buckets)."""

import unittest

from push_suppress.checkers import (
    check_budget,
    check_channel_capacity,
    check_mutex,
    check_send_once_and_window,
    check_user_rolling_window,
    validate_schedule,
)
from push_suppress.model import Instance, Notification, Send


def make_instance(notifications, **kwargs):
    return Instance(notifications=notifications, **kwargs)


def note(nid, user="u1", channel="email", profit=1.0, start=0, end=10,
         cost=0.0, mutex_group=None):
    return Notification(
        id=nid, user=user, channel=channel, profit=profit,
        start=start, end=end, cost=cost, mutex_group=mutex_group,
    )


class RollingWindowBoundaryTest(unittest.TestCase):
    """Acceptance boundary: half-open length-3 window, at most 1 send."""

    def setUp(self):
        self.inst = make_instance(
            [note("a"), note("b"), note("c")],
            window_length=3,
            window_max_sends=1,
        )

    def test_times_0_and_2_conflict(self):
        sends = [Send("a", 0), Send("b", 2)]
        violations = check_user_rolling_window(self.inst, sends)
        self.assertTrue(violations, "t=0 and t=2 must conflict (window [0,3))")

    def test_times_0_and_3_allowed(self):
        sends = [Send("a", 0), Send("b", 3)]
        violations = check_user_rolling_window(self.inst, sends)
        self.assertEqual(violations, [], "t=0 and t=3 must be allowed")

    def test_sliding_not_fixed_buckets(self):
        # t=2 and t=4 fall into different fixed buckets [0,3) and [3,6),
        # but the half-open window [2,5) contains both -> must conflict.
        sends = [Send("a", 2), Send("b", 4)]
        violations = check_user_rolling_window(self.inst, sends)
        self.assertTrue(
            violations,
            "t=2 and t=4 conflict under a sliding window even though fixed "
            "buckets [0,3) and [3,6) would count them separately",
        )

    def test_boundary_chain_allowed(self):
        sends = [Send("a", 0), Send("b", 3), Send("c", 6)]
        self.assertEqual(check_user_rolling_window(self.inst, sends), [])

    def test_three_sends_one_window(self):
        sends = [Send("a", 1), Send("b", 2), Send("c", 3)]
        violations = check_user_rolling_window(self.inst, sends)
        self.assertTrue(violations)

    def test_different_users_independent(self):
        inst = make_instance(
            [note("a", user="u1"), note("b", user="u2")],
            window_length=3, window_max_sends=1,
        )
        sends = [Send("a", 0), Send("b", 0)]
        self.assertEqual(check_user_rolling_window(inst, sends), [])

    def test_max_sends_greater_than_one(self):
        inst = make_instance(
            [note("a"), note("b"), note("c")],
            window_length=3, window_max_sends=2,
        )
        ok = [Send("a", 0), Send("b", 1)]
        bad = [Send("a", 0), Send("b", 1), Send("c", 2)]
        self.assertEqual(check_user_rolling_window(inst, ok), [])
        self.assertTrue(check_user_rolling_window(inst, bad))


class SendOnceAndWindowTest(unittest.TestCase):
    def setUp(self):
        self.inst = make_instance(
            [note("a", start=2, end=4)], time_horizon=8,
            window_length=3, window_max_sends=1,
        )

    def test_send_inside_window_ok(self):
        self.assertEqual(
            check_send_once_and_window(self.inst, [Send("a", 3)]), [])

    def test_send_outside_window(self):
        self.assertTrue(
            check_send_once_and_window(self.inst, [Send("a", 5)]))

    def test_send_twice(self):
        violations = check_send_once_and_window(
            self.inst, [Send("a", 2), Send("a", 3)])
        self.assertTrue(any("more than once" in v for v in violations))

    def test_unknown_notification(self):
        violations = check_send_once_and_window(self.inst, [Send("zzz", 2)])
        self.assertTrue(any("unknown" in v for v in violations))

    def test_horizon_enforced(self):
        inst = make_instance(
            [note("a", start=0, end=99)], time_horizon=5,
            window_length=3, window_max_sends=1,
        )
        self.assertTrue(check_send_once_and_window(inst, [Send("a", 7)]))


class ChannelCapacityTest(unittest.TestCase):
    def test_capacity_exceeded_globally_across_users(self):
        inst = make_instance(
            [note("a", user="u1"), note("b", user="u2")],
            window_length=3, window_max_sends=1,
            channel_capacity={"email": 1},
        )
        sends = [Send("a", 0), Send("b", 0)]
        self.assertTrue(check_channel_capacity(inst, sends))

    def test_capacity_ok_at_distinct_times(self):
        inst = make_instance(
            [note("a", user="u1"), note("b", user="u2")],
            window_length=3, window_max_sends=1,
            channel_capacity={"email": 1},
        )
        sends = [Send("a", 0), Send("b", 1)]
        self.assertEqual(check_channel_capacity(inst, sends), [])

    def test_unlisted_channel_unlimited(self):
        inst = make_instance(
            [note("a", user="u1", channel="push"),
             note("b", user="u2", channel="push")],
            window_length=3, window_max_sends=1,
            channel_capacity={"email": 1},
        )
        sends = [Send("a", 0), Send("b", 0)]
        self.assertEqual(check_channel_capacity(inst, sends), [])


class MutexTest(unittest.TestCase):
    def test_same_group_conflicts(self):
        inst = make_instance(
            [note("a", user="u1", mutex_group="camp"),
             note("b", user="u2", mutex_group="camp")],
            window_length=3, window_max_sends=1,
        )
        sends = [Send("a", 0), Send("b", 5)]
        self.assertTrue(check_mutex(inst, sends))

    def test_different_groups_ok(self):
        inst = make_instance(
            [note("a", user="u1", mutex_group="c1"),
             note("b", user="u2", mutex_group="c2")],
            window_length=3, window_max_sends=1,
        )
        sends = [Send("a", 0), Send("b", 5)]
        self.assertEqual(check_mutex(inst, sends), [])


class BudgetTest(unittest.TestCase):
    def test_budget_exceeded(self):
        inst = make_instance(
            [note("a", user="u1", cost=3), note("b", user="u2", cost=3)],
            window_length=3, window_max_sends=1, budget=5,
        )
        sends = [Send("a", 0), Send("b", 0)]
        self.assertTrue(check_budget(inst, sends))

    def test_budget_ok_and_unlimited(self):
        inst = make_instance(
            [note("a", user="u1", cost=3)],
            window_length=3, window_max_sends=1, budget=3,
        )
        self.assertEqual(check_budget(inst, [Send("a", 0)]), [])
        inst_no_budget = make_instance(
            [note("a", user="u1", cost=100)],
            window_length=3, window_max_sends=1,
        )
        self.assertEqual(check_budget(inst_no_budget, [Send("a", 0)]), [])


class ValidateScheduleTest(unittest.TestCase):
    def test_aggregates_all_violations(self):
        inst = make_instance(
            [note("a", user="u1", mutex_group="g"),
             note("b", user="u1", mutex_group="g")],
            window_length=3, window_max_sends=1,
            channel_capacity={"email": 1},
        )
        # Same user, same slot, same group, same channel: 3 violation kinds.
        sends = [Send("a", 0), Send("b", 0)]
        violations = validate_schedule(inst, sends)
        self.assertGreaterEqual(len(violations), 3)

    def test_feasible_schedule_has_no_violations(self):
        inst = make_instance(
            [note("a", user="u1"), note("b", user="u2")],
            window_length=3, window_max_sends=1,
            channel_capacity={"email": 2},
        )
        self.assertEqual(validate_schedule(inst, [Send("a", 0), Send("b", 0)]), [])


if __name__ == "__main__":
    unittest.main()
