import unittest
from fractions import Fraction

from scheduler import (
    BUDGET_UNDECIDED,
    FEASIBLE,
    INFEASIBLE,
    OPTIMAL,
    ForbiddenInterval,
    Interval,
    KIND_MAINTENANCE,
    KIND_OCCUPANCY,
    Operation,
    Track,
    schedule,
    to_fraction,
)

F = Fraction


def maint(start, end, label="m"):
    return ForbiddenInterval(Interval(F(start), F(end)), KIND_MAINTENANCE, label)


def occ(start, end, label="o"):
    return ForbiddenInterval(Interval(F(start), F(end)), KIND_OCCUPANCY, label)


class ParsingTest(unittest.TestCase):
    def test_int_and_rational_strings(self):
        self.assertEqual(to_fraction(3), F(3))
        self.assertEqual(to_fraction("3/2"), F(3, 2))
        self.assertEqual(to_fraction(" 7 "), F(7))
        self.assertEqual(to_fraction(F(5, 6)), F(5, 6))

    def test_rejects_bool_and_float(self):
        with self.assertRaises(TypeError):
            to_fraction(True)
        with self.assertRaises(TypeError):
            to_fraction(1.5)


class IntervalTest(unittest.TestCase):
    def test_half_open_touching_is_not_overlap(self):
        a = Interval(F(0), F(3))
        b = Interval(F(3), F(5))
        self.assertFalse(a.overlaps(b))
        self.assertFalse(b.overlaps(a))

    def test_real_overlap(self):
        a = Interval(F(0), F(3))
        self.assertTrue(a.overlaps(Interval(F(2), F(9))))
        self.assertTrue(a.overlaps(Interval(F(0), F(1))))

    def test_rejects_empty_interval(self):
        with self.assertRaises(ValueError):
            Interval(F(2), F(2))


class ScheduleTest(unittest.TestCase):
    def test_unbounded_empty_track_is_optimal_at_horizon_start(self):
        op = Operation("op", F(4), F(1), F(1))
        res = schedule(op, [Track("T1")])
        self.assertEqual(res.status, OPTIMAL)
        self.assertEqual(res.track_id, "T1")
        self.assertEqual(res.start, F(1))  # occupied starts at 0, plus safety_before
        self.assertEqual((res.occupied_start, res.occupied_end), (F(0), F(6)))

    def test_maintenance_pushes_start_and_evidence_recorded(self):
        op = Operation("op", F(4), F(1), F(1))
        track = Track("T1", maintenance=(maint(0, 5, "maint-A"),))
        res = schedule(op, [track])
        self.assertEqual(res.status, OPTIMAL)
        self.assertEqual(res.occupied_start, F(5))
        self.assertEqual(res.start, F(6))
        self.assertEqual(len(res.evidence), 1)
        ev = res.evidence[0]
        self.assertEqual(ev["kind"], KIND_MAINTENANCE)
        self.assertEqual(ev["label"], "maint-A")
        self.assertEqual(ev["forbidden_interval"], ["0", "5"])
        self.assertEqual(ev["attempted_occupied_interval"], ["0", "6"])

    def test_half_open_adjacency_to_maintenance_is_allowed(self):
        # Occupied [5, 11) against maintenance [0, 5) and [11, 13): both touch.
        op = Operation("op", F(4), F(1), F(1))
        track = Track("T1", maintenance=(maint(0, 5), maint(11, 13)))
        res = schedule(op, [track])
        self.assertEqual(res.status, OPTIMAL)
        self.assertEqual((res.occupied_start, res.occupied_end), (F(5), F(11)))

    def test_safety_margins_occupy_capacity(self):
        # Existing occupancy [6, 8): without safety the op [1,5) would fit at
        # occupied start 1, but safety_after pushes the footprint to [1, 7)
        # which collides, so the occupied start must move to 8.
        op = Operation("op", F(4), F(1), F(2))
        track = Track("T1", occupied=(occ(6, 8, "other-op"),))
        res = schedule(op, [track])
        self.assertEqual(res.occupied_start, F(8))
        self.assertEqual(res.evidence[0]["kind"], KIND_OCCUPANCY)
        self.assertEqual(res.evidence[0]["label"], "other-op")

    def test_empty_compatible_tracks_is_immediately_infeasible(self):
        op = Operation("op", F(4))
        res = schedule(op, [Track("T1")], compatible_track_ids=[])
        self.assertEqual(res.status, INFEASIBLE)
        self.assertEqual(res.reason, "no compatible tracks")
        self.assertIsNone(res.start)

    def test_unknown_compatible_ids_are_ignored(self):
        op = Operation("op", F(2))
        tracks = [Track("T1"), Track("T2", maintenance=(maint(0, 10),))]
        res = schedule(op, tracks, compatible_track_ids=["T2", "ghost"])
        self.assertEqual(res.track_id, "T2")
        self.assertEqual(res.occupied_start, F(10))

    def test_earliest_track_wins_and_tie_breaks_by_id(self):
        op = Operation("op", F(3))
        tracks = [
            Track("TB", maintenance=(maint(0, 4),)),
            Track("TA", maintenance=(maint(0, 4),)),
            Track("TC", maintenance=(maint(0, 9),)),
        ]
        res = schedule(op, tracks)
        self.assertEqual(res.status, OPTIMAL)
        self.assertEqual(res.track_id, "TA")  # deterministic tie-break
        self.assertEqual(res.occupied_start, F(4))
        later = [e for e in res.evidence if e["kind"] == "later_track"]
        self.assertEqual({e["track_id"] for e in later}, {"TC"})

    def test_rational_exactness(self):
        op = Operation("op", F(3, 2), F(1, 4), F(1, 4))  # footprint 2
        track = Track("T1", maintenance=(maint(0, F(5, 2)),))
        res = schedule(op, [track])
        self.assertEqual(res.occupied_start, F(5, 2))
        self.assertEqual(res.start, F(11, 4))
        self.assertEqual(res.occupied_end, F(9, 2))

    def test_horizon_infeasible_with_blocker_evidence(self):
        op = Operation("op", F(4), F(1), F(1))  # footprint 6
        track = Track("T1", maintenance=(maint(0, 5),))
        res = schedule(op, [track], horizon_end=F(10))
        self.assertEqual(res.status, INFEASIBLE)
        kinds = {e["kind"] for e in res.evidence}
        self.assertIn(KIND_MAINTENANCE, kinds)
        self.assertIn("horizon", kinds)
        horizon_ev = next(e for e in res.evidence if e["kind"] == "horizon")
        self.assertEqual(horizon_ev["earliest_occupied_end"], "11")
        self.assertEqual(horizon_ev["horizon_end"], "10")

    def test_within_budget_is_optimal(self):
        op = Operation("op", F(2))
        res = schedule(op, [Track("T1")], horizon_end=F(20), budget=F(5))
        self.assertEqual(res.status, OPTIMAL)
        self.assertTrue(res.budget_met)

    def test_over_budget_is_feasible_not_optimal(self):
        op = Operation("op", F(2))
        track = Track("T1", maintenance=(maint(0, 7),))
        res = schedule(op, [track], horizon_end=F(20), budget=F(5))
        self.assertEqual(res.status, FEASIBLE)
        self.assertEqual(res.start, F(7))
        self.assertFalse(res.budget_met)

    def test_budget_bounded_miss_is_undecided(self):
        # No explicit horizon: the budget bounds the search, so a miss is
        # undecided rather than infeasible.
        op = Operation("op", F(4))
        track = Track("T1", maintenance=(maint(0, 100),))
        res = schedule(op, [track], budget=F(10))
        self.assertEqual(res.status, BUDGET_UNDECIDED)
        self.assertFalse(res.budget_met)
        self.assertIsNone(res.start)
        self.assertTrue(res.evidence)

    def test_budget_bounded_hit_is_optimal(self):
        op = Operation("op", F(4))
        track = Track("T1", maintenance=(maint(0, 3),))
        res = schedule(op, [track], budget=F(10))
        self.assertEqual(res.status, OPTIMAL)
        self.assertEqual(res.start, F(3))

    def test_occupied_interval_respects_horizon_start(self):
        op = Operation("op", F(2), F(1), F(0))
        res = schedule(op, [Track("T1")], horizon_start=F(4))
        self.assertEqual(res.occupied_start, F(4))
        self.assertEqual(res.start, F(5))


if __name__ == "__main__":
    unittest.main()
