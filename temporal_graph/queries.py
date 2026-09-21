from __future__ import annotations

import heapq
from typing import Any, Optional

from .cache import EvidenceCache
from .graph import Snapshot
from .model import ConcurrentPath, EdgeRecord, HalfOpenInterval, Journey, JourneyStep


def simultaneous_paths(
    snapshot: Snapshot,
    source: str,
    target: str,
    cache: EvidenceCache | None = None,
) -> tuple[ConcurrentPath, ...]:
    snapshot.vertex(source)
    snapshot.vertex(target)

    def search(
        vertex: str,
        window: HalfOpenInterval,
        steps: tuple[EdgeRecord, ...],
        visited: frozenset[str],
    ) -> tuple[ConcurrentPath, ...]:
        if vertex == target and steps:
            path = ConcurrentPath(steps, window)
            key = (
                "simultaneous_path",
                source,
                target,
                tuple(step.edge_id for step in steps),
            )
            dependencies = {("edge", step.edge_id): step.version for step in steps}
            if cache is not None:
                hit = cache.get("simultaneous_path", key, snapshot.revision, dependencies)
                if hit is not None and isinstance(hit.value, ConcurrentPath):
                    return (hit.value,)
                cache.put("simultaneous_path", key, snapshot.revision, dependencies, path)
            return (path,)

        results: list[ConcurrentPath] = []
        for edge in snapshot.outgoing(vertex):
            next_window = window.intersect(edge.window)
            if next_window is None or edge.target in visited:
                continue

            candidate_steps = steps + (edge,)

            subtree = search(
                edge.target,
                next_window,
                candidate_steps,
                visited | {edge.target},
            )
            results.extend(subtree)
        return tuple(results)

    return search(source, HalfOpenInterval(-(10**18), 10**18), (), frozenset({source}))


def earliest_journey(
    snapshot: Snapshot,
    source: str,
    target: str,
    start_time: int,
    horizon: int,
    cache: EvidenceCache | None = None,
) -> Optional[Journey]:
    _require_time(start_time, "start_time")
    _require_time(horizon, "horizon")
    if start_time > horizon:
        raise ValueError("start_time must be <= horizon")

    source_record = snapshot.vertex(source)
    snapshot.vertex(target)
    if source_record.blocked_at(start_time):
        return None
    if source == target:
        return Journey(source, target, start_time, start_time, ())

    signature = tuple(sorted(snapshot.active_edge_ids()))
    key = ("earliest_journey", source, target, start_time, horizon, signature)
    dependencies: dict[tuple[str, str], int] = {
        ("vertex", source): source_record.version,
        ("vertex", target): snapshot.vertex(target).version,
    }
    hit = (
        cache.get("earliest_journey", key, snapshot.revision, dependencies)
        if cache is not None
        else None
    )
    if hit is not None:
        return hit.value if isinstance(hit.value, Journey) else None

    parents = {
        (source, start_time): ((None, None), None, start_time, start_time),
    }
    settled: set[tuple[str, int]] = set()
    queue: list[tuple[int, int, str, tuple[str, int]]] = [
        (start_time, 0, source, (source, start_time))
    ]
    sequence = 1

    while queue:
        arrival, _, vertex, label = heapq.heappop(queue)
        if label in settled:
            continue
        settled.add(label)
        if snapshot.vertex(vertex).blocked_at(arrival):
            continue
        for edge in snapshot.outgoing(vertex):
            dependencies[("edge", edge.edge_id)] = edge.version
            dependencies[("vertex", edge.target)] = snapshot.vertex(edge.target).version
        if vertex == target:
            journey = _build_journey(source, target, label, parents, snapshot)
            _store_journey(cache, key, snapshot, dependencies, journey)
            return journey

        for edge in snapshot.outgoing(vertex):
            segment = _first_departure_segment(
                snapshot, vertex, max(arrival, start_time), edge, horizon
            )
            while segment is not None:
                lower, upper = segment
                for departure in range(lower, upper):
                    pushed = _relax_edge(
                        snapshot,
                        label,
                        edge,
                        departure,
                        arrival,
                        horizon,
                        settled,
                        parents,
                        queue,
                        sequence,
                    )
                    if pushed:
                        sequence += 1
                segment = _first_departure_segment(
                    snapshot, vertex, upper, edge, horizon
                )

    _store_journey(cache, key, snapshot, dependencies, None)
    return None


def _first_departure_segment(
    snapshot: Snapshot,
    vertex: str,
    earliest: int,
    edge: EdgeRecord,
    horizon: int,
) -> int | None:
    if earliest > horizon or earliest >= edge.window.end or edge.window.start > horizon:
        return None
    vertex_record = snapshot.vertex(vertex)
    if vertex_record.blocked_at(earliest):
        return None
    next_block_start = min(
        (window.start for window in vertex_record.blocked if window.start > earliest),
        default=horizon + 1,
    )
    if edge.window.start >= next_block_start:
        return None
    lower = max(earliest, edge.window.start)
    upper = min(edge.window.end, next_block_start, horizon + 1)
    if lower >= upper:
        return None
    return lower, upper


def _relax_edge(
    snapshot: Snapshot,
    label: tuple[str, int],
    edge: EdgeRecord,
    departure: int,
    previous_arrival: int,
    horizon: int,
    settled: set[tuple[str, int]],
    parents: dict[tuple[str, int], tuple[Any, EdgeRecord | None, int, int]],
    queue: list[tuple[int, int, str, tuple[str, int]]],
    sequence: int,
) -> bool:
    next_arrival = departure + edge.duration
    next_label = (edge.target, next_arrival)
    target_blocked = snapshot.vertex(edge.target).blocked_at(next_arrival)
    if (
        next_arrival <= horizon
        and next_label not in settled
        and next_label not in parents
        and not target_blocked
    ):
        parents[next_label] = (label, edge, departure, previous_arrival)
        heapq.heappush(queue, (next_arrival, sequence, edge.target, next_label))
        return True
    return False


def _build_journey(
    source: str,
    target: str,
    final_label: tuple[str, int],
    parents: dict[tuple[str, int], tuple[Any, EdgeRecord | None, int, int]],
    snapshot: Snapshot,
) -> Journey:
    reversed_steps: list[JourneyStep] = []
    label = final_label
    while True:
        previous_label, edge, departure, previous_arrival = parents[label]
        if edge is None or previous_label[0] is None:
            break
        current_edge = snapshot.edge(edge.edge_id)
        if current_edge.version != edge.version:
            raise RuntimeError("journey references stale edge version")
        reversed_steps.append(
            JourneyStep(
                edge=current_edge,
                departure=departure,
                arrival=label[1],
                wait_before=departure - previous_arrival,
            )
        )
        label = previous_label

    reversed_steps.reverse()
    departure_time = reversed_steps[0].departure - reversed_steps[0].wait_before if reversed_steps else final_label[1]
    return Journey(source, target, departure_time, final_label[1], tuple(reversed_steps))


def _store_journey(
    cache: EvidenceCache | None,
    key: tuple[Any, ...],
    snapshot: Snapshot,
    dependencies: dict[tuple[str, str], int],
    journey: Journey | None,
) -> None:
    if cache is not None:
        cache.put("earliest_journey", key, snapshot.revision, dependencies, journey)


def _require_time(value: int, name: str) -> None:
    if not isinstance(value, int) or isinstance(value, bool):
        raise TypeError(f"{name} must be an integer")
