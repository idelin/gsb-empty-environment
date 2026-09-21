from __future__ import annotations

from .graph import Snapshot
from .model import ConcurrentPath, HalfOpenInterval, Journey


class EvidenceError(AssertionError):
    pass


def verify_concurrent_path(
    snapshot: Snapshot,
    path: ConcurrentPath,
    source: str,
    target: str,
) -> None:
    if not path.steps:
        raise EvidenceError("path must contain at least one edge")
    window = HalfOpenInterval(-(10**18), 10**18)
    current = source
    for index, step in enumerate(path.steps):
        record = snapshot.edge_at_version(step.edge_id, step.version)
        if step != record:
            raise EvidenceError(f"step {index} does not match snapshot edge record")
        if snapshot.edge(step.edge_id) != record:
            raise EvidenceError(f"step {index} is not the edge active in this snapshot")
        if record.source != current:
            raise EvidenceError(f"step {index} source mismatch")
        next_window = window.intersect(record.window)
        if next_window is None:
            raise EvidenceError(f"step {index} has no common valid time")
        window = next_window
        current = record.target
    if current != target:
        raise EvidenceError("path target mismatch")
    if window != path.common_window or not path.common_window:
        raise EvidenceError("reported common window is not the verified intersection")
    endpoints = (path.common_window.start, path.common_window.end - 1)
    for step in path.steps:
        for time in endpoints:
            if not snapshot.edge_at_version(step.edge_id, step.version).window.contains(time):
                raise EvidenceError("endpoint segment check failed")


def verify_journey(
    snapshot: Snapshot,
    journey: Journey,
    source: str,
    target: str,
    start_time: int,
    horizon: int,
) -> None:
    snapshot.vertex(source)
    snapshot.vertex(target)
    if journey.source != source:
        raise EvidenceError("invalid journey source")
    if journey.target != target:
        raise EvidenceError("invalid journey target")
    if journey.departure != start_time:
        raise EvidenceError("journey departure differs from requested start")
    if journey.arrival > horizon:
        raise EvidenceError("journey arrives after horizon")
    current_vertex = journey.source
    current_time = start_time
    if snapshot.vertex(current_vertex).blocked_at(current_time):
        raise EvidenceError("journey starts inside a blocked vertex interval")

    for index, step in enumerate(journey.steps):
        record = snapshot.edge_at_version(step.edge.edge_id, step.edge.version)
        if step.edge != record:
            raise EvidenceError(f"step {index} does not match snapshot edge record")
        if record.source != current_vertex:
            raise EvidenceError(f"step {index} source mismatch")
        if step.wait_before != step.departure - current_time or step.wait_before < 0:
            raise EvidenceError(f"step {index} has inconsistent wait")
        if not snapshot.vertex(current_vertex).open_through(current_time, step.departure + 1):
            raise EvidenceError(f"step {index} waits while vertex is blocked")
        if not record.window.contains(step.departure):
            raise EvidenceError(f"step {index} departs outside edge window")
        if step.arrival != step.departure + record.duration:
            raise EvidenceError(f"step {index} ignores edge duration")
        if snapshot.vertex(record.target).blocked_at(step.arrival):
            raise EvidenceError(f"step {index} arrives while target is blocked")
        current_vertex = record.target
        current_time = step.arrival

    if current_vertex != journey.target or current_time != journey.arrival:
        raise EvidenceError("journey target or final arrival mismatch")
