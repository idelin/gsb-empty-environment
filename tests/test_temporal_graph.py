import random
import unittest

from temporal_graph import (
    EvidenceCache,
    EvidenceError,
    HalfOpenInterval,
    TemporalGraph,
    earliest_journey,
    earliest_journey_expanded,
    simultaneous_paths,
    verify_concurrent_path,
    verify_journey,
)


def interval(start, end):
    return HalfOpenInterval(start, end)


class SemanticTests(unittest.TestCase):
    def test_adjoining_intervals_distinguish_semantics(self):
        graph = TemporalGraph()
        graph.add_edge("ab", "A", "B", interval(0, 5), 1)
        graph.add_edge("bc", "B", "C", interval(5, 10), 1)
        snapshot = graph.snapshot()

        self.assertEqual(simultaneous_paths(snapshot, "A", "C"), ())

        journey = earliest_journey(snapshot, "A", "C", 0, 12)
        self.assertIsNotNone(journey)
        self.assertEqual(journey.arrival, 6)
        self.assertEqual([step.edge.edge_id for step in journey.steps], ["ab", "bc"])
        self.assertEqual([step.wait_before for step in journey.steps], [0, 4])
        verify_journey(snapshot, journey, "A", "C", 0, 12)

    def test_node_forbidden_interval_can_invalidate_all_waiting(self):
        graph = TemporalGraph()
        graph.declare_vertex("B", blocked=[interval(2, 6)])
        graph.add_edge("ab", "A", "B", interval(0, 5), 1)
        graph.add_edge("bc", "B", "C", interval(5, 10), 1)
        snapshot = graph.snapshot()

        self.assertIsNone(earliest_journey(snapshot, "A", "C", 0, 12))

    def test_later_arrival_can_be_useful_after_forbidden_interval(self):
        graph = TemporalGraph()
        graph.declare_vertex("B", blocked=[interval(1, 6)])
        graph.add_edge("ab-fast", "A", "B", interval(0, 5), 1)
        graph.add_edge("ab-slow", "A", "B", interval(0, 5), 6)
        graph.add_edge("bc", "B", "C", interval(5, 10), 1)
        snapshot = graph.snapshot()

        journey = earliest_journey(snapshot, "A", "C", 0, 12)
        self.assertIsNotNone(journey)
        self.assertEqual(journey.steps[0].edge.edge_id, "ab-slow")
        self.assertEqual(journey.steps[0].arrival, 6)
        self.assertEqual(journey.steps[1].departure, 6)
        verify_journey(snapshot, journey, "A", "C", 0, 12)

    def test_multiple_edges_are_separate_simultaneous_paths(self):
        graph = TemporalGraph()
        graph.add_edge("a1", "A", "B", interval(0, 8), 1)
        graph.add_edge("a2", "A", "B", interval(3, 10), 1)
        snapshot = graph.snapshot()

        paths = simultaneous_paths(snapshot, "A", "B")
        self.assertEqual([path.steps[0].edge_id for path in paths], ["a1", "a2"])
        self.assertEqual([path.common_window for path in paths], [interval(0, 8), interval(3, 10)])
        for path in paths:
            verify_concurrent_path(snapshot, path, "A", "B")

    def test_zero_duration_cycle_does_not_duplicate_or_deadlock(self):
        graph = TemporalGraph()
        graph.add_edge("ab", "A", "B", interval(0, 10), 0)
        graph.add_edge("bb", "B", "B", interval(0, 10), 0)
        graph.add_edge("bc", "B", "C", interval(2, 10), 0)
        snapshot = graph.snapshot()

        journey = earliest_journey(snapshot, "A", "C", 0, 10)
        self.assertIsNotNone(journey)
        self.assertEqual(journey.arrival, 2)
        verify_journey(snapshot, journey, "A", "C", 0, 10)

    def test_half_open_boundary_endpoint_is_excluded(self):
        graph = TemporalGraph()
        graph.add_edge("ab", "A", "B", interval(4, 5), 1)
        graph.add_edge("bc1", "B", "C", interval(0, 5), 0)
        graph.add_edge("bc2", "B", "C", interval(7, 10), 1)
        snapshot = graph.snapshot()

        simultaneous = simultaneous_paths(snapshot, "A", "C")
        self.assertEqual(len(simultaneous), 1)
        self.assertEqual(simultaneous[0].steps[1].edge_id, "bc1")
        verify_concurrent_path(snapshot, simultaneous[0], "A", "C")

        waiting = earliest_journey(snapshot, "A", "C", 0, 10)
        self.assertIsNotNone(waiting)
        self.assertEqual(waiting.arrival, 8)
        self.assertEqual(waiting.steps[1].edge.edge_id, "bc2")


class SnapshotCacheTests(unittest.TestCase):
    def test_middle_edge_correction_revokes_only_that_route(self):
        graph = TemporalGraph()
        graph.add_edge("ab", "A", "B", interval(0, 10), 1)
        graph.add_edge("bc", "B", "C", interval(0, 10), 1)
        graph.add_edge("cd", "C", "D", interval(0, 10), 1)
        graph.add_edge("pq", "P", "Q", interval(0, 10), 1)
        cache = EvidenceCache(graph)
        snapshot = graph.snapshot()

        old_route = simultaneous_paths(snapshot, "A", "D", cache)[0]
        unrelated = simultaneous_paths(snapshot, "P", "Q", cache)[0]
        graph.correct_edge("bc", window=interval(10, 20))
        changed = graph.snapshot()

        self.assertEqual(simultaneous_paths(changed, "A", "D", cache), ())
        unrelated_after = simultaneous_paths(changed, "P", "Q", cache)[0]
        self.assertEqual(unrelated_after, unrelated)
        self.assertEqual(cache.stats.revocations, 1)
        self.assertEqual(simultaneous_paths(snapshot, "A", "D", cache)[0], old_route)

    def test_correction_revokes_dependent_evidence_and_retains_unrelated(self):
        graph = TemporalGraph()
        graph.add_edge("ab1", "A", "B", interval(0, 5), 1)
        graph.add_edge("ab2", "A", "B", interval(5, 10), 1)
        graph.add_edge("bd", "B", "D", interval(0, 10), 1)
        cache = EvidenceCache(graph)
        old_snapshot = graph.snapshot()

        changed = simultaneous_paths(old_snapshot, "A", "B", cache)
        unrelated = simultaneous_paths(old_snapshot, "B", "D", cache)
        self.assertEqual(len(changed), 2)
        self.assertEqual(len(unrelated), 1)

        graph.correct_edge("ab1", window=interval(3, 8))
        new_snapshot = graph.snapshot()

        changed_new = simultaneous_paths(new_snapshot, "A", "B", cache)
        unrelated_new = simultaneous_paths(new_snapshot, "B", "D", cache)
        self.assertEqual([path.common_window for path in changed_new], [interval(3, 8), interval(5, 10)])
        self.assertEqual(unrelated_new[0].steps[0].version, 1)
        self.assertEqual(cache.stats.revocations, 1)
        self.assertEqual(cache.stats.hits, 2)

        old_again = simultaneous_paths(old_snapshot, "A", "B", cache)
        self.assertEqual([path.common_window for path in old_again], [interval(0, 5), interval(5, 10)])

        graph.correct_edge("bd", window=interval(1, 9))
        old_journey = earliest_journey(old_snapshot, "A", "B", 0, 12, cache)
        self.assertIsNotNone(old_journey)
        self.assertEqual(old_journey.arrival, 1)

    def test_withdrawn_edge_is_absent_new_but_readable_in_old_snapshot(self):
        graph = TemporalGraph()
        graph.add_edge("ab", "A", "B", interval(0, 5), 1)
        cache = EvidenceCache(graph)
        old_snapshot = graph.snapshot()
        old_path = simultaneous_paths(old_snapshot, "A", "B", cache)[0]

        graph.withdraw_edge("ab")
        new_snapshot = graph.snapshot()
        self.assertEqual(simultaneous_paths(new_snapshot, "A", "B"), ())
        with self.assertRaises(KeyError):
            new_snapshot.edge("ab")

        old_after = simultaneous_paths(old_snapshot, "A", "B", cache)
        self.assertEqual(old_after[0].steps[0].version, old_path.steps[0].version)
        self.assertEqual(old_snapshot.edge("ab").active, True)
        self.assertEqual(new_snapshot.edge_at_version("ab", 2).active, False)

    def test_tampered_evidence_fails_verification(self):
        graph = TemporalGraph()
        graph.add_edge("ab", "A", "B", interval(0, 5), 1)
        old_snapshot = graph.snapshot()
        graph.correct_edge("ab", window=interval(6, 9))
        new_snapshot = graph.snapshot()

        stale_path = simultaneous_paths(old_snapshot, "A", "B")[0]
        with self.assertRaises(EvidenceError):
            verify_concurrent_path(new_snapshot, stale_path, "A", "B")

    def test_journey_cache_depends_on_all_current_edges(self):
        graph = TemporalGraph()
        graph.add_edge("ab", "A", "B", interval(0, 5), 1)
        graph.add_edge("bc", "B", "C", interval(5, 10), 1)
        cache = EvidenceCache(graph)
        snapshot = graph.snapshot()
        first = earliest_journey(snapshot, "A", "C", 0, 12, cache)
        self.assertIsNotNone(first)
        self.assertEqual(cache.stats.hits, 0)

        earliest_journey(snapshot, "A", "C", 0, 12, cache)
        self.assertEqual(cache.stats.hits, 1)

        graph.add_edge("xc", "X", "C", interval(0, 10), 1)
        changed_snapshot = graph.snapshot()
        earliest_journey(changed_snapshot, "A", "C", 0, 12, cache)
        self.assertEqual(cache.stats.hits, 1)

        graph.correct_edge("xc", window=interval(2, 11))
        unrelated_correction_snapshot = graph.snapshot()
        earliest_journey(unrelated_correction_snapshot, "A", "C", 0, 12, cache)
        self.assertEqual(cache.stats.hits, 2)

    def test_added_edge_preserves_old_route_and_adds_new_route(self):
        graph = TemporalGraph()
        graph.add_edge("ab1", "A", "B", interval(0, 5), 1)
        cache = EvidenceCache(graph)
        old_snapshot = graph.snapshot()
        old_route = simultaneous_paths(old_snapshot, "A", "B", cache)[0]

        graph.add_edge("ab2", "A", "B", interval(3, 8), 1)
        new_snapshot = graph.snapshot()
        routes = simultaneous_paths(new_snapshot, "A", "B", cache)

        self.assertEqual([path.steps[0].edge_id for path in routes], ["ab1", "ab2"])
        self.assertIn(old_route, routes)
        self.assertEqual(cache.stats.revocations, 0)
        self.assertEqual(simultaneous_paths(old_snapshot, "A", "B", cache)[0], old_route)


class OracleComparisonTests(unittest.TestCase):
    def test_random_small_time_graphs_match_expanded_oracle(self):
        rng = random.Random(20260921)
        for case in range(120):
            graph = TemporalGraph()
            vertices = [chr(ord("A") + index) for index in range(4)]
            for vertex in vertices:
                if rng.random() < 0.25:
                    start = rng.randrange(0, 8)
                    graph.declare_vertex(vertex, blocked=[interval(start, start + rng.randrange(1, 4))])
                else:
                    graph.declare_vertex(vertex)

            for edge_index in range(rng.randrange(3, 9)):
                source = rng.choice(vertices)
                target = rng.choice(vertices)
                start = rng.randrange(0, 8)
                graph.add_edge(
                    f"e{edge_index}",
                    source,
                    target,
                    interval(start, start + rng.randrange(0, 6)),
                    rng.randrange(0, 4),
                )
            snapshot = graph.snapshot()

            for source in vertices:
                for target in vertices:
                    start_time = rng.randrange(0, 4)
                    actual = earliest_journey(snapshot, source, target, start_time, 12)
                    oracle = earliest_journey_expanded(snapshot, source, target, start_time, 12)
                    if actual is None:
                        self.assertIsNone(oracle, case)
                    else:
                        self.assertIsNotNone(oracle, case)
                        self.assertEqual(actual.arrival, oracle.arrival, case)
                        verify_journey(snapshot, actual, source, target, start_time, 12)


if __name__ == "__main__":
    unittest.main()
