import json
import random
import unittest

from transit import (
    CapacityError,
    CONFIRMED,
    REPLAN,
    Journey,
    Network,
    Planner,
    ReservationSystem,
    RideLeg,
    Stop,
    Timetable,
    Trip,
    brute_force_pareto_pairs,
    pareto_query,
)


def flat(station, t):
    return Stop(station, t, t)


class TransferTimeTest(unittest.TestCase):
    """Arrive 5, transfer 2: departure 6 cannot be caught, 7 can."""

    def setUp(self):
        net = Network()
        net.add_vertex("A")
        net.add_vertex("B", transfer_time=2)
        net.add_vertex("C")
        self.tt = Timetable(net)
        self.tt.add_trip(Trip("T1", (flat("A", 0), flat("B", 5)), 2))
        self.tt.add_trip(Trip("T2", (flat("B", 6), flat("C", 9)), 2))
        self.tt.add_trip(Trip("T3", (flat("B", 7), flat("C", 10)), 2))

    def test_miss_6_catch_7(self):
        journeys = pareto_query(self.tt, "A", "C", 0)
        self.assertEqual(
            [(j.arrival_time, j.num_transfers) for j in journeys], [(10, 1)]
        )
        self.assertEqual(journeys[0].trip_ids(), ["T1", "T3"])

    def test_interval_is_left_closed(self):
        # Boarding at exactly t0 / exactly arrival+transfer is allowed.
        self.assertEqual(pareto_query(self.tt, "B", "C", 7)[0].arrival_time, 10)
        # One minute later the 7 o'clock departure is gone and nothing remains.
        self.assertEqual(pareto_query(self.tt, "B", "C", 8), [])


class ParetoTest(unittest.TestCase):
    def test_two_non_dominated_routes_same_departure(self):
        net = Network()
        net.add_vertex("A")
        net.add_vertex("H", transfer_time=1)
        net.add_vertex("D")
        tt = Timetable(net)
        tt.add_trip(Trip("direct", (flat("A", 60), flat("D", 120)), 5))
        tt.add_trip(Trip("fast1", (flat("A", 60), flat("H", 90)), 5))
        tt.add_trip(Trip("fast2", (flat("H", 95), flat("D", 110)), 5))
        pairs = {(j.arrival_time, j.num_transfers) for j in pareto_query(tt, "A", "D", 60)}
        # Earlier arrival with a transfer vs. later arrival direct: both kept.
        self.assertEqual(pairs, {(110, 1), (120, 0)})


class CrossDayTest(unittest.TestCase):
    def test_overnight_trips(self):
        net = Network()
        for s in "ABC":
            net.add_vertex(s)
        tt = Timetable(net)
        tt.add_trip(Trip("night1", (flat("A", 1430), flat("B", 1510)), 3))
        tt.add_trip(Trip("night2", (flat("B", 1520), flat("C", 1560)), 3))
        journeys = pareto_query(tt, "A", "C", 1400)
        self.assertEqual([(j.arrival_time, j.num_transfers) for j in journeys], [(1560, 1)])
        self.assertEqual(pareto_query(tt, "A", "B", 1400)[0].arrival_time, 1510)


class NetworkStructureTest(unittest.TestCase):
    def test_isolated_vertices_and_multiedges(self):
        net = Network()
        net.add_vertex("X")
        net.add_vertex("Y")
        net.add_vertex("ISO")
        net.add_edge("e1", "X", "Y", 9)
        net.add_edge("e2", "X", "Y", 3)  # parallel edge, distinct ID
        self.assertIn("ISO", net.vertices)
        self.assertEqual(set(net.edges), {"e1", "e2"})
        self.assertEqual(net.shortest_walks()["X"]["Y"], 3)

    def test_walking_transfer_uses_shortest_edge(self):
        net = Network()
        for s in ("A", "X", "Y", "B"):
            net.add_vertex(s)
        net.add_edge("slow", "X", "Y", 9)
        net.add_edge("fast", "X", "Y", 3)
        tt = Timetable(net)
        tt.add_trip(Trip("T1", (flat("A", 0), flat("X", 10)), 2))
        tt.add_trip(Trip("T2", (flat("Y", 20), flat("B", 30)), 2))
        journeys = pareto_query(tt, "A", "B", 0)
        self.assertEqual([(j.arrival_time, j.num_transfers) for j in journeys], [(30, 1)])


class ReservationTest(unittest.TestCase):
    def setUp(self):
        net = Network()
        for s in "ABC":
            net.add_vertex(s)
        self.tt = Timetable(net)
        self.tt.add_trip(Trip("T1", (flat("A", 0), flat("B", 10)), 1))
        self.tt.add_trip(Trip("T2", (flat("B", 10), flat("C", 20)), 1))
        self.rs = ReservationSystem(self.tt, ttl=30)

    def test_competing_requests_only_one_wins_last_seat(self):
        journey = Journey((RideLeg("T1", "A", 0, "B", 10),))
        self.rs.reserve(journey, now=0)
        with self.assertRaises(CapacityError):
            self.rs.reserve(journey, now=0)
        self.assertEqual(self.rs.remaining["T1"], [0])

    def test_partial_reservation_rolls_back_without_leak(self):
        blocker = Journey((RideLeg("T2", "B", 10, "C", 20),))
        self.rs.reserve(blocker, now=0)  # fills T2's only seat
        two_leg = Journey(
            (RideLeg("T1", "A", 0, "B", 10), RideLeg("T2", "B", 10, "C", 20))
        )
        before = {k: list(v) for k, v in self.rs.remaining.items()}
        with self.assertRaises(CapacityError):
            self.rs.reserve(two_leg, now=0)  # takes T1, fails on T2, must undo T1
        self.assertEqual(self.rs.remaining, before)
        self.assertEqual(self.rs.remaining["T1"], [1])

    def test_valid_release_then_token_goes_stale(self):
        journey = Journey((RideLeg("T1", "A", 0, "B", 10),))
        res = self.rs.reserve(journey, now=0)
        self.assertTrue(self.rs.release(res.id, res.generation))
        self.assertEqual(self.rs.remaining["T1"], [1])
        self.assertFalse(self.rs.release(res.id, 0))  # generation bumped
        self.assertEqual(self.rs.remaining["T1"], [1])

    def test_stale_token_cannot_release_after_expiry(self):
        journey = Journey((RideLeg("T1", "A", 0, "B", 10),))
        res = self.rs.reserve(journey, now=0)
        self.assertEqual(self.rs.expire(now=30), [res.id])  # [0,30) window over
        self.assertEqual(self.rs.remaining["T1"], [1])
        self.assertFalse(self.rs.release(res.id, 0))  # old generation rejected
        self.assertEqual(self.rs.remaining["T1"], [1])  # no phantom capacity

    def test_expired_token_stays_invalid_after_restart(self):
        journey = Journey((RideLeg("T1", "A", 0, "B", 10),))
        res = self.rs.reserve(journey, now=0)
        self.assertEqual(self.rs.remaining["T1"], [0])
        # Simulate a restart: persist to JSON and rebuild from scratch.
        snap = json.loads(json.dumps(self.rs.snapshot()))
        restored = ReservationSystem.restore(snap, timetable=self.tt)
        self.assertEqual(restored.expire(now=30), [res.id])
        self.assertFalse(restored.release(res.id, 0))
        self.assertEqual(restored.remaining["T1"], [1])
        # A fresh reservation on the freed seat must not be affected by the
        # old token either.
        res2 = restored.reserve(journey, now=40)
        self.assertFalse(restored.release(res.id, 0))
        self.assertEqual(restored.remaining["T1"], [0])
        self.assertTrue(restored.release(res2.id, res2.generation))


class PlannerTest(unittest.TestCase):
    def setUp(self):
        net = Network()
        for s in "ABC":
            net.add_vertex(s)
        self.planner = Planner(net, ttl=30)
        self.planner.add_trip(Trip("T1", (flat("A", 0), flat("B", 10)), 1))
        self.planner.add_trip(Trip("T2", (flat("B", 10), flat("C", 20)), 1))

    def test_cancel_mid_journey_replans_and_invalidates_evidence(self):
        first = self.planner.query("A", "C", 0)
        self.assertEqual(len(first), 1)
        v0 = self.planner.evidence_version("A", "C", 0)
        cj = self.planner.confirm(first[0], now=0)
        self.assertEqual(cj.status, CONFIRMED)
        self.planner.cancel_trip("T2")  # cancel the second leg mid-journey
        self.assertEqual(cj.status, REPLAN)
        self.assertGreater(self.planner.timetable.version, v0)
        after = self.planner.query("A", "C", 0)
        self.assertEqual(after, [])
        self.assertEqual(
            self.planner.evidence_version("A", "C", 0), self.planner.timetable.version
        )

    def test_correction_replans_and_updates_results(self):
        journey = self.planner.query("A", "C", 0)[0]
        cj = self.planner.confirm(journey, now=0)
        self.planner.correct_trip("T2", stops=(flat("B", 12), flat("C", 22)))
        self.assertEqual(cj.status, REPLAN)
        arrivals = [j.arrival_time for j in self.planner.query("A", "C", 0)]
        self.assertEqual(arrivals, [22])

    def test_cancelled_trip_excluded_from_query(self):
        self.planner.cancel_trip("T1")
        self.assertEqual(self.planner.query("A", "C", 0), [])


class BruteForceConsistencyTest(unittest.TestCase):
    """pareto_query must agree with exhaustive time-expanded enumeration."""

    def test_random_small_instances(self):
        for seed in range(8):
            with self.subTest(seed=seed):
                rng = random.Random(seed)
                stations = [f"S{i}" for i in range(4)]
                net = Network()
                for s in stations:
                    net.add_vertex(s, transfer_time=rng.randint(0, 3))
                net.add_vertex("ISO")  # isolated vertex
                for k in range(3):
                    a, b = rng.sample(stations, 2)
                    net.add_edge(f"E{k}", a, b, rng.randint(1, 8))
                tt = Timetable(net)
                for t in range(6):
                    seq = [rng.choice(stations)]
                    for _ in range(rng.randint(1, 2)):
                        nxt = rng.choice(stations)
                        while nxt == seq[-1]:
                            nxt = rng.choice(stations)
                        seq.append(nxt)
                    time = rng.randint(0, 1500)
                    stops = []
                    for st in seq:
                        arr = time + rng.randint(0, 2)
                        dep = arr + rng.randint(0, 3)
                        stops.append(Stop(st, arr, dep))
                        time = dep + rng.randint(3, 15)
                    tt.add_trip(
                        Trip(
                            f"T{t}",
                            tuple(stops),
                            rng.randint(0, 3),
                            cancelled=rng.random() < 0.15,
                        )
                    )
                for case in range(6):
                    origin, dest = rng.sample(stations, 2)
                    t0 = rng.randint(0, 600)
                    with self.subTest(seed=seed, case=case, origin=origin, dest=dest, t0=t0):
                        got = {
                            (j.arrival_time, j.num_transfers)
                            for j in pareto_query(tt, origin, dest, t0)
                        }
                        want = brute_force_pareto_pairs(tt, origin, dest, t0)
                        self.assertEqual(got, want)


if __name__ == "__main__":
    unittest.main()
