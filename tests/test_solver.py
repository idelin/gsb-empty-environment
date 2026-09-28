"""Tests for the exact solver: optimality is cross-checked against
independent exhaustive enumeration on many small synthetic instances,
including the acceptance-boundary scenarios and tie-breaking."""

import random
import unittest

from push_suppress.checkers import is_feasible
from push_suppress.enumerate import best_by_enumeration
from push_suppress.model import Instance, Notification
from push_suppress.solver import schedule_key, solve


def note(nid, user="u1", channel="email", profit=1.0, start=0, end=5,
         cost=0.0, mutex_group=None):
    return Notification(
        id=nid, user=user, channel=channel, profit=profit,
        start=start, end=end, cost=cost, mutex_group=mutex_group,
    )


class SolverCorrectnessTest(unittest.TestCase):
    def test_rolling_window_boundary_instance(self):
        # u1 can get at most one send in any [t, t+3): the solver must
        # pick t=0 and t=3 (not t=0 and t=2) for the two best offers.
        inst = Instance(
            notifications=[
                note("a", profit=5, start=0, end=0),
                note("b", profit=4, start=2, end=2),
                note("c", profit=4, start=3, end=3),
            ],
            window_length=3, window_max_sends=1,
        )
        result = solve(inst)
        self.assertEqual(result.profit, 9.0)
        self.assertEqual(
            [(s.notification_id, s.time) for s in result.sends],
            [("a", 0), ("c", 3)],
        )
        self.assertTrue(is_feasible(inst, result.sends))

    def test_channel_capacity_forces_spread(self):
        inst = Instance(
            notifications=[
                note("a", user="u1", profit=3, start=0, end=1),
                note("b", user="u2", profit=3, start=0, end=1),
            ],
            window_length=3, window_max_sends=1,
            channel_capacity={"email": 1},
        )
        result = solve(inst)
        self.assertEqual(result.profit, 6.0)
        times = sorted(s.time for s in result.sends)
        self.assertEqual(times, [0, 1], "capacity 1 forces different slots")

    def test_mutex_group_allows_only_one(self):
        inst = Instance(
            notifications=[
                note("a", profit=5, start=0, end=2, mutex_group="g"),
                note("b", profit=4, start=0, end=2, mutex_group="g"),
            ],
            window_length=3, window_max_sends=1,
        )
        result = solve(inst)
        self.assertEqual(result.profit, 5.0)
        self.assertEqual(len(result.sends), 1)
        self.assertEqual(result.sends[0].notification_id, "a")

    def test_budget_excludes_expensive(self):
        inst = Instance(
            notifications=[
                note("a", user="u1", profit=10, cost=5, start=0, end=0),
                note("b", user="u2", profit=6, cost=2, start=0, end=0),
                note("c", user="u3", profit=6, cost=2, start=0, end=0),
            ],
            window_length=3, window_max_sends=1, budget=4,
        )
        result = solve(inst)
        self.assertEqual(result.profit, 12.0)
        self.assertEqual(
            sorted(s.notification_id for s in result.sends), ["b", "c"])

    def test_empty_instance(self):
        result = solve(Instance(notifications=[]))
        self.assertEqual(result.profit, 0.0)
        self.assertEqual(result.sends, [])

    def test_negative_profits_are_skipped(self):
        inst = Instance(
            notifications=[note("a", profit=-5, start=0, end=2)],
            window_length=3, window_max_sends=1,
        )
        result = solve(inst)
        self.assertEqual(result.profit, 0.0)
        self.assertEqual(result.sends, [])

    def test_tie_break_by_id_then_time(self):
        # Two schedules with profit 5: {"a"@1} and {"b"@0}.
        # Key (("a",1),) < (("b",0),) so "a"@1 wins.
        inst = Instance(
            notifications=[
                note("b", user="u2", profit=5, start=0, end=0, mutex_group="g"),
                note("a", user="u1", profit=5, start=1, end=1, mutex_group="g"),
            ],
            window_length=3, window_max_sends=1,
        )
        result = solve(inst)
        self.assertEqual(result.profit, 5.0)
        self.assertEqual(
            [(s.notification_id, s.time) for s in result.sends], [("a", 1)])

    def test_tie_break_by_time_for_same_id(self):
        # Same notification, same profit at t=1 and t=2: earlier time wins.
        inst = Instance(
            notifications=[note("a", profit=5, start=1, end=2)],
            window_length=3, window_max_sends=1,
        )
        result = solve(inst)
        self.assertEqual(
            [(s.notification_id, s.time) for s in result.sends], [("a", 1)])

    def test_tie_break_prefers_fewer_sends(self):
        # Zero-profit "b" must not be added to the optimal schedule.
        inst = Instance(
            notifications=[
                note("a", user="u1", profit=5, start=0, end=0),
                note("b", user="u2", profit=0, start=0, end=0),
            ],
            window_length=3, window_max_sends=1,
        )
        result = solve(inst)
        self.assertEqual(
            [(s.notification_id, s.time) for s in result.sends], [("a", 0)])


class SolverVsEnumerationTest(unittest.TestCase):
    """Cross-verify solver optimality against brute-force enumeration
    on deterministic pseudo-random small synthetic instances."""

    def random_instance(self, rng: random.Random, n_notes: int) -> Instance:
        users = ["u1", "u2"]
        channels = ["email", "sms"]
        notes = []
        for i in range(n_notes):
            start = rng.randint(0, 3)
            notes.append(Notification(
                id=f"n{i}",
                user=rng.choice(users),
                channel=rng.choice(channels),
                profit=float(rng.randint(-2, 9)),
                start=start,
                end=start + rng.randint(0, 3),
                cost=float(rng.randint(0, 3)),
                mutex_group=rng.choice([None, None, "g1", "g2"]),
            ))
        return Instance(
            notifications=notes,
            window_length=3,
            window_max_sends=rng.choice([1, 1, 2]),
            channel_capacity={c: rng.randint(1, 2) for c in channels},
            budget=rng.choice([None, 4.0, 6.0]),
        )

    def test_solver_matches_enumeration(self):
        rng = random.Random(20260929)
        cases = 0
        for trial in range(60):
            inst = self.random_instance(rng, n_notes=rng.randint(1, 6))
            result = solve(inst)
            enum_profit, enum_key, _ = best_by_enumeration(inst)
            self.assertAlmostEqual(
                result.profit, enum_profit, places=9,
                msg=f"profit mismatch on trial {trial}",
            )
            self.assertEqual(
                schedule_key(result.sends), enum_key,
                msg=f"tie-break key mismatch on trial {trial}",
            )
            self.assertTrue(
                is_feasible(inst, result.sends),
                msg=f"solver returned infeasible schedule on trial {trial}",
            )
            cases += 1
        self.assertEqual(cases, 60)


if __name__ == "__main__":
    unittest.main()
