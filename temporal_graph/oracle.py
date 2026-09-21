from __future__ import annotations

import heapq
from typing import Optional

from .graph import Snapshot
from .model import EdgeRecord, Journey, JourneyStep


def earliest_journey_expanded(
    snapshot: Snapshot,
    source: str,
    target: str,
    start_time: int,
    horizon: int,
) -> Optional[Journey]:
    snapshot.vertex(source)
    snapshot.vertex(target)
    if start_time > horizon:
        raise ValueError("start_time must be <= horizon")
    if snapshot.vertex(source).blocked_at(start_time):
        return None
    if source == target:
        return Journey(source, target, start_time, start_time, ())

    nodes: set[tuple[str, int]] = {(source, start_time)}
    for vertex in snapshot.vertex_ids():
        nodes.update((vertex, time) for time in range(start_time, horizon + 1))

    arcs: dict[tuple[str, int], list[tuple[tuple[str, int], str, EdgeRecord | None, int]]] = {
        node: [] for node in nodes
    }
    for vertex, time in sorted(nodes):
        if time < horizon and not snapshot.vertex(vertex).blocked_at(time + 1):
            arcs[(vertex, time)].append(((vertex, time + 1), "wait", None, time))
        for edge in snapshot.outgoing(vertex):
            arrival = time + edge.duration
            if (
                edge.window.contains(time)
                and arrival <= horizon
                and not snapshot.vertex(edge.target).blocked_at(arrival)
            ):
                arcs[(vertex, time)].append(((edge.target, arrival), "edge", edge, time))

    start = (source, start_time)
    distance = {start: 0}
    previous: dict[
        tuple[str, int],
        tuple[tuple[str, int], str, EdgeRecord | None, int],
    ] = {}
    queue: list[tuple[int, tuple[str, int]]] = [(0, start)]

    while queue:
        current_distance, node = heapq.heappop(queue)
        if current_distance != distance[node]:
            continue
        for next_node, kind, edge, departure in arcs[node]:
            weight = 0 if kind == "wait" else next_node[1] - node[1]
            new_distance = current_distance + weight
            if new_distance < distance.get(next_node, 10**100):
                distance[next_node] = new_distance
                previous[next_node] = (node, kind, edge, departure)
                heapq.heappush(queue, (new_distance, next_node))

    candidates = [
        (node, dist)
        for node, dist in distance.items()
        if node[0] == target and not snapshot.vertex(target).blocked_at(node[1])
    ]
    if not candidates:
        return None
    final_node, _ = min(candidates, key=lambda item: (item[0][1], item[1]))

    reversed_steps: list[JourneyStep] = []
    waits = 0
    node = final_node
    while node != start:
        prior, kind, edge, departure = previous[node]
        if kind == "wait":
            waits += 1
        elif edge is not None:
            reversed_steps.append(
                JourneyStep(
                    edge=snapshot.edge(edge.edge_id),
                    departure=departure,
                    arrival=node[1],
                    wait_before=waits,
                )
            )
            waits = 0
        node = prior
    reversed_steps.reverse()
    return Journey(source, target, start_time, final_node[1], tuple(reversed_steps))
