import random
import unittest

from oracle import (
    earliest_arrival_expanded,
    simultaneous_endpoint_times,
    simultaneous_paths_at_expanded,
)
from temporal_graph import (
    QueryEngine,
    RideStep,
    TemporalGraph,
    TemporalGraphError,
    WaitStep,
    validate_journey,
    validate_simultaneous_route,
)


class QuerySemanticsTest(unittest.TestCase):
    def build_main(self):
        graph = TemporalGraph()
        graph.add_edge("ab", "A", "B", 0, 5, 1)
        graph.add_edge("bc", "B", "C", 5, 10, 1)
        return graph, QueryEngine(graph)

    def test_same_time_vs_waiting_are_distinct_results(self):
        graph, engine = self.build_main()
        same = engine.simultaneous_paths("A", "C")
        self.assertEqual(same.kind, "simultaneous_paths")
        self.assertEqual(same.routes, ())
        self.assertEqual(engine.simultaneous_at("A", "C", 4).routes, ())

        journey = engine.earliest_journey("A", "C", 0)
        self.assertEqual(journey.kind, "earliest_waiting_journey")
        self.assertEqual(journey.arrival, 6)
        self.assertEqual(
            journey.events,
            (
                RideStep("ab", 0, "A", "B", 0, 1, 1, 0, 5),
                WaitStep("B", 1, 5),
                RideStep("bc", 0, "B", "C", 5, 6, 1, 5, 10),
            ),
        )
        validate_journey(journey, graph.snapshot())

    def test_correcting_middle_edge_revokes_negative_evidence_and_keeps_unrelated(self):
        graph, engine = self.build_main()
        graph.add_edge("xy", "X", "Y", 0, 10, 1)
        no_path = engine.simultaneous_paths("A", "C")
        unrelated = engine.simultaneous_paths("X", "Y")
        self.assertEqual(no_path.routes, ())
        self.assertEqual(len(unrelated.routes), 1)
        self.assertEqual(engine.cache_stats().simultaneous_entries, 2)

        graph.correct_edge("bc", start=0, end=5)
        self.assertEqual(engine.cache_stats().simultaneous_entries, 1)

        corrected = engine.simultaneous_paths("A", "C")
        self.assertEqual(len(corrected.routes), 1)
        self.assertEqual(corrected.routes[0].interval, (0, 5))
        cached_unrelated = engine.simultaneous_paths("X", "Y")
        self.assertEqual(cached_unrelated.routes[0].legs[0].edge_id, "xy")

        old = engine.simultaneous_paths("A", "C", snapshot_id=2)
        self.assertEqual(old.snapshot_id, 2)
        self.assertEqual(old.routes, ())

    def test_withdrawn_edge_and_old_snapshot_remain_readable(self):
        graph = TemporalGraph()
        graph.add_edge("e", "A", "B", 0, 3, 0)
        engine = QueryEngine(graph)
        before = engine.earliest_journey("A", "B", 0)
        self.assertEqual(before.arrival, 0)
        graph.withdraw_edge("e")
        after = engine.earliest_journey("A", "B", 0)
        self.assertIsNone(after.arrival)
        old = engine.earliest_journey("A", "B", 0, snapshot_id=1)
        self.assertEqual(old.snapshot_id, 1)
        self.assertEqual(old.arrival, 0)
        validate_journey(old, graph.snapshot(1))

    def test_journey_negative_evidence_is_invalidated_but_unrelated_evidence_remains(self):
        graph = TemporalGraph(vertices=("A", "B", "C", "X", "Y"))
        graph.add_edge("ab", "A", "B", 0, 6, 1)
        graph.add_edge("bc", "B", "C", 0, 5, 1)
        engine = QueryEngine(graph)
        graph.add_edge("xy", "X", "Y", 0, 4, 0)
        blocked = engine.earliest_journey("A", "C", 4)
        unrelated = engine.earliest_journey("X", "Y", 0)
        self.assertIsNone(blocked.arrival)
        self.assertEqual(unrelated.arrival, 0)
        self.assertEqual(engine.cache_stats().journey_entries, 2)

        graph.correct_edge("bc", start=0, end=6)
        self.assertEqual(engine.cache_stats().journey_entries, 1)
        reachable = engine.earliest_journey("A", "C", 4)
        self.assertEqual(reachable.arrival, 6)
        cached_unrelated = engine.earliest_journey("X", "Y", 0)
        self.assertEqual(cached_unrelated.arrival, 0)

    def test_multiple_edges_touching_boundaries_and_zero_duration_cycles(self):
        graph = TemporalGraph()
        graph.add_edge("fast", "A", "B", 5, 10, 0)
        graph.add_edge("slow", "A", "B", 5, 10, 2)
        graph.add_edge("loop", "B", "B", 0, 10, 0)
        graph.add_edge("bnext", "B", "C", 5, 10, 0)
        engine = QueryEngine(graph)

        same_at_4 = engine.simultaneous_at("A", "C", 4)
        self.assertEqual(same_at_4.routes, ())
        same_at_5 = engine.simultaneous_at("A", "C", 5)
        edge_sets = {tuple(step.edge_id for step in route.rides) for route in same_at_5.routes}
        self.assertEqual(edge_sets, {("fast", "bnext"), ("slow", "bnext")})

        zero = engine.earliest_journey("A", "C", 5)
        self.assertEqual(zero.arrival, 5)
        self.assertEqual([event.edge_id for event in zero.rides], ["fast", "bnext"])
        loop_journey = engine.earliest_journey("B", "B", 5)
        self.assertEqual(loop_journey.arrival, 5)
        self.assertEqual(loop_journey.events, ())

    def test_half_open_end_boundary_is_closed(self):
        graph = TemporalGraph()
        graph.add_edge("a", "A", "B", 0, 5, 0)
        graph.add_edge("b", "B", "C", 0, 5, 0)
        engine = QueryEngine(graph)
        self.assertEqual(engine.simultaneous_at("A", "C", 4).routes and 1, 1)
        self.assertEqual(engine.simultaneous_at("A", "C", 5).routes, ())
        journey = engine.earliest_journey("A", "C", 4)
        validate_journey(journey, graph.snapshot())
        self.assertEqual(journey.arrival, 4)

    def test_waiting_cannot_overlap_node_blocking(self):
        graph = TemporalGraph(blocked={"A": [(1, 5)]})
        graph.add_edge("ab_early", "A", "B", 0, 1, 0)
        graph.add_edge("ab", "A", "B", 5, 7, 0)
        graph.add_edge("bc", "B", "C", 5, 8, 1)
        engine = QueryEngine(graph)
        blocked_arrival = engine.earliest_journey("A", "C", 1)
        self.assertIsNone(blocked_arrival.arrival)
        legal = engine.earliest_journey("A", "C", 5)
        self.assertEqual(legal.arrival, 6)
        self.assertEqual([event.edge_id for event in legal.rides], ["ab", "bc"])
        validate_journey(legal, graph.snapshot())


class OraclePropertyTest(unittest.TestCase):
    def test_random_graphs_against_expanded_time_oracle(self):
        rng = random.Random(73219)
        for case in range(60):
            graph = TemporalGraph(
                vertices=(f"v{index}" for index in range(5)),
                blocked={
                    "v3": [(2, 4)] if case % 3 == 0 else [],
                    "v1": [(6, 8)] if case % 5 == 0 else [],
                }
            )
            for index in range(10):
                source = f"v{rng.randrange(5)}"
                target = f"v{rng.randrange(5)}"
                start = rng.randrange(0, 8)
                end = start + rng.randrange(1, 5)
                graph.add_edge(f"e{index}", source, target, start, end, rng.randrange(0, 4))
            engine = QueryEngine(graph)
            snapshot = graph.snapshot()
            for source_index in range(5):
                for target_index in range(5):
                    source = f"v{source_index}"
                    target = f"v{target_index}"
                    all_paths = engine.simultaneous_paths(source, target)
                    expected_times = simultaneous_endpoint_times(snapshot, 1)
                    for time in expected_times:
                        expected = set(simultaneous_paths_at_expanded(snapshot, source, target, time))
                        actual = {
                            tuple(edge.edge_id for edge in route.legs)
                            for route in all_paths.routes
                            if (route.interval[0] is None or route.interval[0] <= time)
                            and (route.interval[1] is None or time < route.interval[1])
                        }
                        self.assertEqual(expected, actual)
                    for time in range(10):
                        journey = engine.earliest_journey(source, target, time)
                        oracle = earliest_arrival_expanded(snapshot, source, target, time)
                        self.assertEqual(journey.arrival, oracle)
                        if journey.arrival is not None:
                            validate_journey(journey, snapshot)

    def test_old_snapshots_stay_valid_after_several_edits(self):
        graph = TemporalGraph()
        graph.add_edge("a", "A", "B", 0, 4, 1)
        graph.add_edge("b", "B", "C", 4, 8, 1)
        engine = QueryEngine(graph)
        first = engine.earliest_journey("A", "C", 0)
        self.assertEqual(first.arrival, 5)
        snapshot_id = graph.current_snapshot_id
        graph.correct_edge("a", start=0, end=2)
        graph.add_edge("direct", "A", "C", 0, 9, 0)
        graph.withdraw_edge("b")
        old = engine.earliest_journey("A", "C", 0, snapshot_id=snapshot_id)
        self.assertEqual(old.snapshot_id, snapshot_id)
        self.assertEqual(old.arrival, 5)
        validate_journey(old, graph.snapshot(snapshot_id))
        for step in old.rides:
            record = graph.snapshot(snapshot_id).edge(step.edge_id)
            self.assertEqual(step.revision, record.revision)


if __name__ == "__main__":
    unittest.main()


class MutationCachePropertyTest(unittest.TestCase):
    def test_edits_invalidate_only_dependent_evidence_and_old_snapshots_remain_exact(self):
        rng = random.Random(9917)
        graph = TemporalGraph(vertices=("A", "B", "C", "D"))
        for edge_id, source, target, start, end, duration in [
            ("ab", "A", "B", 0, 6, 1),
            ("bc", "B", "C", 3, 10, 1),
            ("cd", "C", "D", 7, 14, 1),
            ("alt", "A", "D", 8, 12, 0),
        ]:
            graph.add_edge(edge_id, source, target, start, end, duration)
        engine = QueryEngine(graph)
        before = engine.simultaneous_paths("A", "D")
        before_journey = engine.earliest_journey("A", "D", 0)
        before_snapshot = graph.current_snapshot_id
        self.assertEqual(len(before.routes), 1)
        self.assertEqual(before_journey.arrival, 8)
        stats = engine.cache_stats()
        self.assertEqual((stats.simultaneous_entries, stats.journey_entries), (1, 1))

        graph.correct_edge("bc", start=0, end=3)
        self.assertEqual(engine.cache_stats().simultaneous_entries, 0)
        self.assertEqual(engine.cache_stats().journey_entries, 0)
        after = engine.simultaneous_paths("A", "D")
        after_journey = engine.earliest_journey("A", "D", 0)
        self.assertEqual([edge.edge_id for edge in after.routes[0].legs], ["alt"])
        self.assertEqual(after_journey.arrival, 8)

        old_paths = engine.simultaneous_paths("A", "D", snapshot_id=before_snapshot)
        old_journey = engine.earliest_journey("A", "D", 0, snapshot_id=before_snapshot)
        self.assertEqual(old_paths.snapshot_id, before_snapshot)
        self.assertEqual(old_journey.snapshot_id, before_snapshot)
        self.assertEqual(len(old_paths.routes), 1)
        self.assertEqual(old_journey.arrival, 8)

        graph.withdraw_edge("alt")
        withdrawn = engine.simultaneous_paths("A", "D")
        self.assertEqual(withdrawn.routes, ())
        graph.add_edge("loop", "B", "B", 0, 20, rng.choice((0, 1)))
        for snapshot_id in range(before_snapshot, graph.current_snapshot_id + 1):
            snapshot = graph.snapshot(snapshot_id)
            for time in simultaneous_endpoint_times(snapshot):
                result = engine.simultaneous_at("A", "D", time, snapshot_id)
                expected = set(simultaneous_paths_at_expanded(snapshot, "A", "D", time))
                actual = {tuple(ride.edge_id for ride in route.rides) for route in result.routes}
                self.assertEqual(actual, expected)
                for route in result.routes:
                    validate_simultaneous_route(route, "A", "D", time, snapshot)
