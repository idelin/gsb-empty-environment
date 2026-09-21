from temporal_graph import QueryEngine, TemporalGraph, validate_journey, validate_simultaneous_route


def main() -> None:
    graph = TemporalGraph(vertices=("A", "B", "C", "X", "Y"))
    graph.add_edge("ab", "A", "B", 0, 5, 1)
    graph.add_edge("bc", "B", "C", 5, 10, 1)
    graph.add_edge("xy", "X", "Y", 0, 20, 1)
    engine = QueryEngine(graph)

    print("== Same instant, all route intervals ==")
    simultaneous = engine.simultaneous_paths("A", "C")
    print(simultaneous)
    at_four = engine.simultaneous_at("A", "C", 4)
    print("at t=4:", [tuple(ride.edge_id for ride in route.rides) for route in at_four.routes])

    print("== Waiting allowed, earliest journey ==")
    journey = engine.earliest_journey("A", "C", 0)
    print("arrival:", journey.arrival)
    for event in journey.events:
        print(event)
    validate_journey(journey, graph.snapshot())

    old_snapshot = graph.current_snapshot_id
    graph.correct_edge("bc", start=0, end=5)
    print("== After correction ==")
    new_routes = engine.simultaneous_paths("A", "C")
    print([(tuple(edge.edge_id for edge in route.legs), route.interval) for route in new_routes.routes])
    timed = engine.simultaneous_at("A", "C", 4)
    for route in timed.routes:
        validate_simultaneous_route(route, "A", "C", 4, graph.snapshot())
        print("timed rides:", [(ride.edge_id, ride.revision, ride.depart, ride.arrive) for ride in route.rides])

    old_routes = engine.simultaneous_paths("A", "C", snapshot_id=old_snapshot)
    print("old snapshot", old_routes.snapshot_id, "still reports", len(old_routes.routes), "routes")


if __name__ == "__main__":
    main()
