from temporal_graph import (
    EvidenceCache,
    HalfOpenInterval,
    TemporalGraph,
    earliest_journey,
    earliest_journey_expanded,
    simultaneous_paths,
    verify_concurrent_path,
    verify_journey,
)


def describe_journey(journey):
    if journey is None:
        return "no journey"
    if not journey.steps:
        return f"already at target at {journey.arrival}"
    text = []
    for step in journey.steps:
        text.append(
            f"{step.edge.edge_id} t={step.departure}->{step.arrival}"
            f" (wait {step.wait_before})"
        )
    return f"arrive {journey.arrival}: " + ", ".join(text)


def main():
    graph = TemporalGraph()
    graph.add_edge("ab", "A", "B", HalfOpenInterval(0, 5), 1)
    graph.add_edge("bc", "B", "C", HalfOpenInterval(5, 10), 1)
    cache = EvidenceCache(graph)
    old_snapshot = graph.snapshot()

    old_concurrent = simultaneous_paths(old_snapshot, "A", "C", cache)
    old_journey = earliest_journey(old_snapshot, "A", "C", 0, 12, cache)
    oracle_journey = earliest_journey_expanded(old_snapshot, "A", "C", 0, 12)
    print("old simultaneous paths:", len(old_concurrent))
    print("old earliest journey:", describe_journey(old_journey))
    print("oracle earliest:", describe_journey(oracle_journey))
    if old_journey is not None:
        for step in old_journey.steps:
            verify_journey(old_snapshot, old_journey, "A", "C", 0, 12)

    graph.correct_edge("ab", window=HalfOpenInterval(5, 10))
    new_snapshot = graph.snapshot()

    new_concurrent = simultaneous_paths(new_snapshot, "A", "C", cache)
    new_journey = earliest_journey(new_snapshot, "A", "C", 0, 12, cache)
    old_concurrent_after = simultaneous_paths(old_snapshot, "A", "C", cache)
    old_journey_after = earliest_journey(old_snapshot, "A", "C", 0, 12, cache)

    for path in new_concurrent:
        verify_concurrent_path(new_snapshot, path, "A", "C")
    if new_journey is not None:
        verify_journey(new_snapshot, new_journey, "A", "C", 0, 12)

    print("new simultaneous paths:", [(path.common_window.start, path.common_window.end) for path in new_concurrent])
    print("new earliest journey:", describe_journey(new_journey))
    print("old snapshot simultaneous after correction:", len(old_concurrent_after))
    print("old snapshot journey after correction:", describe_journey(old_journey_after))
    print("cache entries:", len(cache.entries()), "revoked:", cache.stats.revocations)


if __name__ == "__main__":
    main()
