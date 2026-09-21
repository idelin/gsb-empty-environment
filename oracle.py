from __future__ import annotations

from typing import Dict, Iterable, Optional, Set, Tuple

from temporal_graph import EdgeVersion, Snapshot, interval_contains, normalize_intervals


def bounded_horizon(snapshot: Snapshot, extra: int = 2) -> int:
    values = [0, extra]
    for edge in snapshot.edges.values():
        if edge.active:
            values.append(edge.end + edge.duration + extra)
    for intervals in snapshot.blocked.values():
        values.extend(right + extra for left, right in intervals)
    return max(values)


def earliest_arrival_expanded(
    snapshot: Snapshot,
    source: str,
    target: str,
    at_time: int,
    horizon: Optional[int] = None,
) -> Optional[int]:
    if horizon is None:
        horizon = bounded_horizon(snapshot)
    if at_time < 0 or at_time > horizon:
        raise ValueError("oracle supports non-negative times up to its horizon")
    if interval_contains(snapshot.blocked.get(source, ()), at_time):
        return None
    states: Set[Tuple[str, int]] = {(source, at_time)}
    frontier = {(source, at_time)}
    while frontier:
        next_frontier: Set[Tuple[str, int]] = set()
        for vertex, time in frontier:
            if vertex == target:
                continue
            blocked = snapshot.blocked.get(vertex, ())
            if time + 1 <= horizon and not interval_contains(blocked, time) and not interval_contains(blocked, time + 1):
                state = (vertex, time + 1)
                if state not in states:
                    states.add(state)
                    next_frontier.add(state)
            for edge in snapshot.adjacency.get(vertex, ()):
                arrive = time + edge.duration
                if arrive > horizon or not edge.is_open_at(time):
                    continue
                if interval_contains(snapshot.blocked.get(edge.target, ()), arrive):
                    continue
                state = (edge.target, arrive)
                if state not in states:
                    states.add(state)
                    next_frontier.add(state)
        frontier = next_frontier
    arrivals = [time for vertex, time in states if vertex == target]
    return min(arrivals) if arrivals else None


def simultaneous_endpoint_times(snapshot: Snapshot, extra: int = 2) -> Tuple[int, ...]:
    points: Set[int] = set()
    boundaries: Set[int] = {0, extra}
    active = [edge for edge in snapshot.edges.values() if edge.active]
    for edge in active:
        boundaries.update({edge.start, edge.end})
    for time in boundaries:
        points.update({max(0, time - 1), time, time + 1})
    ordered = sorted(time for time in points if time >= 0)
    if not ordered:
        return tuple(range(0, max(1, bounded_horizon(snapshot, extra))))
    return tuple(ordered)


def simultaneous_paths_at_expanded(snapshot: Snapshot, source: str, target: str, at_time: int) -> Tuple[Tuple[str, ...], ...]:
    if source == target:
        return ((),)
    answer: Set[Tuple[str, ...]] = set()

    def visit(vertex: str, path: Tuple[str, ...], edges: Tuple[str, ...], seen: Set[str]) -> None:
        if vertex == target:
            answer.add(edges)
            return
        if vertex in seen:
            return
        next_seen = set(seen)
        next_seen.add(vertex)
        for edge in snapshot.adjacency.get(vertex, ()):
            if edge.is_open_at(at_time):
                visit(edge.target, path + (edge.target,), edges + (edge.edge_id,), next_seen)

    visit(source, (source,), (), set())
    return tuple(sorted(answer))


def assert_simultaneous_matches(engine_result, snapshot: Snapshot, source: str, target: str) -> None:
    for time in simultaneous_endpoint_times(snapshot):
        expected = set(simultaneous_paths_at_expanded(snapshot, source, target, time))
        actual = {
            tuple(edge.edge_id for edge in route.legs)
            for route in engine_result.routes
            if route.interval[0] is None or route.interval[0] <= time
            if route.interval[1] is None or time < route.interval[1]
        }
        if source != target and () in expected:
            expected.discard(())
        if actual != expected:
            raise AssertionError(f"simultaneous mismatch at {time}: expected {expected}, got {actual}")
