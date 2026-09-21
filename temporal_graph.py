from __future__ import annotations

from bisect import bisect_left
from dataclasses import dataclass
from heapq import heappop, heappush
from typing import Callable, Dict, Iterable, List, Optional, Sequence, Set, Tuple, Union


class TemporalGraphError(ValueError):
    pass


@dataclass(frozen=True)
class EdgeVersion:
    edge_id: str
    source: str
    target: str
    start: int
    end: int
    duration: int
    revision: int
    active: bool

    def is_open_at(self, time: int) -> bool:
        return self.active and self.start <= time < self.end


@dataclass(frozen=True)
class Snapshot:
    snapshot_id: int
    edges: Dict[str, EdgeVersion]
    adjacency: Dict[str, Tuple[EdgeVersion, ...]]
    blocked: Dict[str, Tuple[Tuple[int, int], ...]]

    def edge(self, edge_id: str) -> EdgeVersion:
        try:
            return self.edges[edge_id]
        except KeyError as exc:
            raise TemporalGraphError(f"edge {edge_id!r} is not in snapshot {self.snapshot_id}") from exc


def normalize_intervals(intervals: Iterable[Sequence[int]]) -> Tuple[Tuple[int, int], ...]:
    values = sorted((int(left), int(right)) for left, right in intervals)
    merged: List[Tuple[int, int]] = []
    for left, right in values:
        if left >= right:
            continue
        if not merged or left > merged[-1][1]:
            merged.append((left, right))
        elif right > merged[-1][1]:
            merged[-1] = (merged[-1][0], right)
    return tuple(merged)


def interval_contains(intervals: Sequence[Tuple[int, int]], time: int) -> bool:
    index = bisect_left(intervals, (time + 1,)) - 1
    return index >= 0 and intervals[index][0] <= time < intervals[index][1]


class TemporalGraph:
    def __init__(
        self,
        vertices: Iterable[str] = (),
        blocked: Optional[Dict[str, Iterable[Sequence[int]]]] = None,
    ) -> None:
        self._history: List[EdgeVersion] = []
        self._edges: Dict[str, EdgeVersion] = {}
        self._vertices = {str(vertex) for vertex in vertices}
        self._blocked: Dict[str, Tuple[Tuple[int, int], ...]] = {}
        if blocked:
            for vertex, values in blocked.items():
                self._vertices.add(str(vertex))
                self._blocked[vertex] = normalize_intervals(values)
        self._snapshot_id = 0
        self._snapshots: Dict[int, Snapshot] = {0: self._build_snapshot(0)}
        self._listeners: List[Callable[[GraphChange], None]] = []

    @property
    def current_snapshot_id(self) -> int:
        return self._snapshot_id

    def snapshot(self, snapshot_id: Optional[int] = None) -> Snapshot:
        if snapshot_id is None:
            snapshot_id = self._snapshot_id
        try:
            return self._snapshots[snapshot_id]
        except KeyError as exc:
            raise TemporalGraphError(f"snapshot {snapshot_id!r} does not exist") from exc

    def history(self) -> Tuple[EdgeVersion, ...]:
        return tuple(self._history)

    def edge_history(self, edge_id: str) -> Tuple[EdgeVersion, ...]:
        return tuple(version for version in self._history if version.edge_id == edge_id)

    def add_edge(self, edge_id: str, source: str, target: str, start: int, end: int, duration: int) -> int:
        if edge_id in self._edges:
            raise TemporalGraphError(f"edge {edge_id!r} already exists; call correct_edge instead")
        version = self._make_version(edge_id, source, target, start, end, duration, 0, True)
        return self._commit([version], added_edges=(edge_id,), affected_vertices=(source, target))

    def correct_edge(
        self,
        edge_id: str,
        start: Optional[int] = None,
        end: Optional[int] = None,
        duration: Optional[int] = None,
    ) -> int:
        current = self._require_active_edge(edge_id)
        version = self._make_version(
            edge_id,
            current.source,
            current.target,
            current.start if start is None else start,
            current.end if end is None else end,
            current.duration if duration is None else duration,
            current.revision + 1,
            True,
        )
        affected_vertices = (current.source, current.target, version.source, version.target)
        return self._commit([version], changed_edges=(edge_id,), affected_vertices=affected_vertices)

    def withdraw_edge(self, edge_id: str) -> int:
        current = self._require_active_edge(edge_id)
        tombstone = EdgeVersion(
            edge_id,
            current.source,
            current.target,
            current.start,
            current.end,
            current.duration,
            current.revision + 1,
            False,
        )
        affected_vertices = (current.source, current.target)
        return self._commit([tombstone], changed_edges=(edge_id,), affected_vertices=affected_vertices)

    def _commit(
        self,
        versions: Iterable[EdgeVersion],
        added_edges: Sequence[str] = (),
        changed_edges: Sequence[str] = (),
        affected_vertices: Sequence[str] = (),
    ) -> int:
        for version in versions:
            self._history.append(version)
            self._edges[version.edge_id] = version
        self._snapshot_id += 1
        snapshot_id = self._snapshot_id
        self._snapshots[snapshot_id] = self._build_snapshot(snapshot_id)
        event = GraphChange(
            snapshot_id=snapshot_id,
            added_edges=tuple(added_edges),
            changed_edges=tuple(changed_edges),
            affected_vertices=tuple(dict.fromkeys(affected_vertices)),
        )
        for listener in list(self._listeners):
            listener(event)
        return snapshot_id

    def _build_snapshot(self, snapshot_id: int) -> Snapshot:
        edges = dict(self._edges)
        adjacency: Dict[str, List[EdgeVersion]] = {}
        vertices = set(self._vertices) | set(self._blocked)
        for version in edges.values():
            vertices.add(version.source)
            vertices.add(version.target)
            if version.active:
                adjacency.setdefault(version.source, []).append(version)
        for vertex in vertices:
            adjacency.setdefault(vertex, [])
        tuple_adjacency = {
            vertex: tuple(sorted(values, key=lambda item: (item.edge_id, item.revision)))
            for vertex, values in adjacency.items()
        }
        return Snapshot(snapshot_id, dict(edges), tuple_adjacency, dict(self._blocked))

    @staticmethod
    def _make_version(
        edge_id: str,
        source: str,
        target: str,
        start: int,
        end: int,
        duration: int,
        revision: int,
        active: bool,
    ) -> EdgeVersion:
        if not edge_id:
            raise TemporalGraphError("edge id must be a non-empty string")
        if not source or not target:
            raise TemporalGraphError("vertex ids must be non-empty strings")
        if end <= start:
            raise TemporalGraphError("validity interval must be non-empty and half-open [start, end)")
        if duration < 0:
            raise TemporalGraphError("edge duration must be non-negative")
        return EdgeVersion(
            str(edge_id), str(source), str(target), int(start), int(end), int(duration), int(revision), active
        )

    def _require_active_edge(self, edge_id: str) -> EdgeVersion:
        try:
            current = self._edges[edge_id]
        except KeyError as exc:
            raise TemporalGraphError(f"edge {edge_id!r} does not exist") from exc
        if not current.active:
            raise TemporalGraphError(f"edge {edge_id!r} has been withdrawn")
        return current

    def add_change_listener(self, listener: "GraphChangeListener") -> None:
        self._listeners.append(listener)


@dataclass(frozen=True)
class GraphChange:
    snapshot_id: int
    added_edges: Tuple[str, ...]
    changed_edges: Tuple[str, ...]
    affected_vertices: Tuple[str, ...]


GraphChangeListener = Callable[[GraphChange], None]


@dataclass(frozen=True)
class RideStep:
    edge_id: str
    revision: int
    source: str
    target: str
    depart: int
    arrive: int
    duration: int
    start: int
    end: int


@dataclass(frozen=True)
class WaitStep:
    vertex: str
    start: int
    end: int


JourneyEvent = Union[RideStep, WaitStep]


@dataclass(frozen=True)
class SimultaneousRoute:
    legs: Tuple[EdgeVersion, ...]
    interval: Tuple[Optional[int], Optional[int]]

    @property
    def vertices(self) -> Tuple[str, ...]:
        if not self.legs:
            return ()
        return (self.legs[0].source,) + tuple(edge.target for edge in self.legs)


@dataclass(frozen=True)
class TimedSimultaneousRoute:
    rides: Tuple[RideStep, ...]
    interval: Tuple[Optional[int], Optional[int]]

    @property
    def vertices(self) -> Tuple[str, ...]:
        if not self.rides:
            return ()
        return (self.rides[0].source,) + tuple(step.target for step in self.rides)


SimultaneousRouteEntry = Union[SimultaneousRoute, TimedSimultaneousRoute]


@dataclass(frozen=True)
class SimultaneousResult:
    kind: str
    source: str
    target: str
    snapshot_id: int
    routes: Tuple[SimultaneousRouteEntry, ...]


@dataclass(frozen=True)
class JourneyResult:
    kind: str
    source: str
    target: str
    snapshot_id: int
    requested_at: int
    arrival: Optional[int]
    events: Tuple[JourneyEvent, ...]

    @property
    def rides(self) -> Tuple[RideStep, ...]:
        return tuple(event for event in self.events if isinstance(event, RideStep))

    @property
    def waits(self) -> Tuple[WaitStep, ...]:
        return tuple(event for event in self.events if isinstance(event, WaitStep))


@dataclass(frozen=True)
class _Dependency:
    edge_revisions: Tuple[Tuple[str, int], ...]
    adjacency: Tuple[Tuple[str, Tuple[Tuple[str, int], ...]], ...]


@dataclass(frozen=True)
class _CacheEntry:
    snapshot_id: int
    dependency: _Dependency
    payload: object


@dataclass(frozen=True)
class CacheStats:
    simultaneous_entries: int
    journey_entries: int
    exact_simultaneous_entries: int
    exact_journey_entries: int


def validate_simultaneous_route(
    route: TimedSimultaneousRoute,
    source: str,
    target: str,
    at_time: int,
    snapshot: Snapshot,
) -> None:
    if not route.rides:
        if source != target:
            raise TemporalGraphError("empty simultaneous route can only connect a vertex to itself")
        return
    if route.rides[0].source != source or route.rides[-1].target != target:
        raise TemporalGraphError("simultaneous route endpoints do not match query")
    current = source
    for step in route.rides:
        record = snapshot.edge(step.edge_id)
        _assert_step_matches_record(step, record)
        if record.source != current or record.target != step.target:
            raise TemporalGraphError(f"ride on {step.edge_id!r} is not contiguous")
        if not record.is_open_at(at_time):
            raise TemporalGraphError(f"edge {step.edge_id!r} is not open at {at_time}")
        if step.depart != at_time:
            raise TemporalGraphError(f"ride on {step.edge_id!r} does not depart at common time {at_time}")
        current = step.target
    interval_left, interval_right = route.interval
    if interval_left is not None and at_time < interval_left:
        raise TemporalGraphError("route interval does not contain query time")
    if interval_right is not None and at_time >= interval_right:
        raise TemporalGraphError("route interval does not contain query time")


def validate_journey(result: JourneyResult, snapshot: Snapshot) -> None:
    if result.snapshot_id != snapshot.snapshot_id:
        raise TemporalGraphError("journey is bound to a different snapshot")
    if not result.rides and result.source != result.target:
        raise TemporalGraphError("non-empty journey requires at least one ride")
    current_vertex = result.source
    current_time = result.requested_at
    if interval_contains(snapshot.blocked.get(current_vertex, ()), current_time):
        raise TemporalGraphError(f"vertex {current_vertex!r} is blocked at requested time")
    for event in result.events:
        if isinstance(event, WaitStep):
            if event.vertex != current_vertex:
                raise TemporalGraphError("wait occurs at a different vertex")
            if event.start != current_time or event.end < event.start:
                raise TemporalGraphError("wait timing is invalid")
            _assert_not_blocked_during_wait(snapshot, event.vertex, event.start, event.end)
            current_time = event.end
        elif isinstance(event, RideStep):
            record = snapshot.edge(event.edge_id)
            _assert_step_matches_record(event, record)
            if record.source != current_vertex:
                raise TemporalGraphError(f"ride on {event.edge_id!r} does not start at current vertex")
            if event.depart != current_time:
                raise TemporalGraphError(f"ride on {event.edge_id!r} has a time gap")
            if interval_contains(snapshot.blocked.get(record.source, ()), event.depart):
                raise TemporalGraphError(f"cannot depart blocked vertex {record.source!r}")
            if interval_contains(snapshot.blocked.get(record.target, ()), event.arrive):
                raise TemporalGraphError(f"arrival at blocked vertex {record.target!r} is forbidden")
            current_vertex = record.target
            current_time = event.arrive
        else:
            raise TemporalGraphError("unknown journey event")
    if current_vertex != result.target or current_time != result.arrival:
        raise TemporalGraphError("journey does not end at advertised arrival")


def _assert_step_matches_record(step: RideStep, record: EdgeVersion) -> None:
    values = (step.revision, step.source, step.target, step.duration, step.start, step.end)
    expected = (record.revision, record.source, record.target, record.duration, record.start, record.end)
    if values != expected:
        raise TemporalGraphError(f"step for edge {step.edge_id!r} does not match its original edge record")
    if step.arrive != step.depart + step.duration:
        raise TemporalGraphError(f"ride on {step.edge_id!r} has incorrect arrival time")
    if not record.is_open_at(step.depart):
        raise TemporalGraphError(f"edge {step.edge_id!r} is not open at departure {step.depart}")


def _assert_not_blocked_during_wait(snapshot: Snapshot, vertex: str, start: int, end: int) -> None:
    for blocked_start, blocked_end in snapshot.blocked.get(vertex, ()):
        if start < blocked_end and end > blocked_start:
            raise TemporalGraphError(f"wait at {vertex!r} overlaps blocked interval [{blocked_start}, {blocked_end})")


def _departure_candidates(edge: EdgeVersion, ready: int, snapshot: Snapshot) -> Tuple[int, ...]:
    source_blocked = snapshot.blocked.get(edge.source, ())
    target_blocked = snapshot.blocked.get(edge.target, ())
    windows: List[Tuple[int, int]] = []
    cursor = ready
    for left, right in source_blocked:
        if right <= ready:
            continue
        if left > cursor:
            windows.append((cursor, min(edge.end, left)))
        cursor = max(cursor, right)
        if cursor >= edge.end:
            break
    if cursor < edge.end:
        windows.append((cursor, edge.end))

    candidates: Set[int] = set()
    for window_left, window_right in windows:
        first = max(window_left, edge.start)
        if first >= window_right or not edge.is_open_at(first):
            continue
        arrival = first + edge.duration
        if not interval_contains(target_blocked, arrival):
            candidates.add(first)
        for left, right in target_blocked:
            before = left - edge.duration
            if first <= before < window_right and edge.is_open_at(before):
                candidates.add(before)
            after = right - edge.duration
            if first <= after < window_right and edge.is_open_at(after):
                candidates.add(after)
    return tuple(
        sorted(
            time
            for time in candidates
            if edge.is_open_at(time)
            and not interval_contains(source_blocked, time)
            and not interval_contains(target_blocked, time + edge.duration)
        )
    )


class QueryEngine:
    def __init__(self, graph: TemporalGraph) -> None:
        self.graph = graph
        self._simultaneous_cache: Dict[Tuple[str, str], _CacheEntry] = {}
        self._journey_cache: Dict[Tuple[str, str, int], _CacheEntry] = {}
        self._exact_simultaneous: Dict[Tuple[int, str, str, int], SimultaneousResult] = {}
        self._exact_journey: Dict[Tuple[int, str, str, int], JourneyResult] = {}
        self.graph.add_change_listener(self._on_graph_change)

    def simultaneous_paths(self, source: str, target: str, snapshot_id: Optional[int] = None) -> SimultaneousResult:
        snapshot = self.graph.snapshot(snapshot_id)
        exact_key = ("all", snapshot.snapshot_id, source, target)
        if exact_key in self._exact_simultaneous:
            return self._exact_simultaneous[exact_key]
        cached = self._simultaneous_cache.get(("all", source, target))
        if cached is not None and self._entry_valid(cached, snapshot):
            result = self._bind_simultaneous(cached.payload, source, target, snapshot)
        else:
            result, dependency = self._compute_simultaneous(source, target, snapshot)
            if cached is not None:
                del self._simultaneous_cache[("all", source, target)]
            self._simultaneous_cache[("all", source, target)] = _CacheEntry(snapshot.snapshot_id, dependency, self._serializable_payload(result))
        self._exact_simultaneous[exact_key] = result
        return result

    def simultaneous_at(self, source: str, target: str, at_time: int, snapshot_id: Optional[int] = None) -> SimultaneousResult:
        all_paths = self.simultaneous_paths(source, target, snapshot_id)
        snapshot = self.graph.snapshot(all_paths.snapshot_id)
        routes = tuple(
            self._simultaneous_route_at(route, at_time)
            for route in all_paths.routes
            if isinstance(route, SimultaneousRoute) and _interval_contains(route.interval, at_time)
        )
        result = SimultaneousResult("simultaneous_paths_at_time", source, target, snapshot.snapshot_id, routes)
        for route in result.routes:
            validate_simultaneous_route(route, source, target, at_time, snapshot)
        return result

    @staticmethod
    def _simultaneous_route_at(route: SimultaneousRoute, at_time: int) -> TimedSimultaneousRoute:
        steps = tuple(
            RideStep(
                edge.edge_id,
                edge.revision,
                edge.source,
                edge.target,
                at_time,
                at_time + edge.duration,
                edge.duration,
                edge.start,
                edge.end,
            )
            for edge in route.legs
        )
        return TimedSimultaneousRoute(steps, route.interval)

    def earliest_journey(self, source: str, target: str, at_time: int, snapshot_id: Optional[int] = None) -> JourneyResult:
        snapshot = self.graph.snapshot(snapshot_id)
        exact_key = (snapshot.snapshot_id, source, target, at_time)
        if exact_key in self._exact_journey:
            return self._exact_journey[exact_key]
        cache_key = (source, target, at_time)
        cached = self._journey_cache.get(cache_key)
        if cached is not None and self._entry_valid(cached, snapshot):
            result = self._bind_journey(cached.payload, source, target, at_time, snapshot)
        else:
            result, dependency = self._compute_journey(source, target, at_time, snapshot)
            if cached is not None:
                del self._journey_cache[cache_key]
            self._journey_cache[cache_key] = _CacheEntry(snapshot.snapshot_id, dependency, self._serializable_payload(result))
        self._exact_journey[exact_key] = result
        return result

    def cache_stats(self) -> CacheStats:
        return CacheStats(
            len(self._simultaneous_cache),
            len(self._journey_cache),
            len(self._exact_simultaneous),
            len(self._exact_journey),
        )

    def _compute_simultaneous(self, source: str, target: str, snapshot: Snapshot) -> Tuple[SimultaneousResult, _Dependency]:
        self._require_vertex(source, snapshot)
        self._require_vertex(target, snapshot)
        if source == target:
            route: SimultaneousRouteEntry = SimultaneousRoute((), (None, None))
            result = SimultaneousResult("simultaneous_paths", source, target, snapshot.snapshot_id, (route,))
            return result, _Dependency((), ())
        routes: List[SimultaneousRoute] = []
        edge_signatures: Dict[str, int] = {}
        adjacency_signatures: Dict[str, Tuple[Tuple[str, int], ...]] = {}

        def visit(vertex: str, legs: List[EdgeVersion], lower: Optional[int], upper: Optional[int], visited: set) -> None:
            outgoing = snapshot.adjacency.get(vertex, ())
            adjacency_signatures[vertex] = tuple((edge.edge_id, edge.revision) for edge in outgoing)
            if vertex == target:
                routes.append(SimultaneousRoute(tuple(legs), (lower, upper)))
                return
            if vertex in visited:
                return
            next_visited = visited | {vertex}
            for edge in outgoing:
                edge_signatures[edge.edge_id] = edge.revision
                next_lower = edge.start if lower is None else max(lower, edge.start)
                next_upper = edge.end if upper is None else min(upper, edge.end)
                if next_lower < next_upper:
                    legs.append(edge)
                    visit(edge.target, legs, next_lower, next_upper, next_visited)
                    legs.pop()

        visit(source, [], None, None, set())
        routes.sort(key=lambda route: tuple(edge.edge_id for edge in route.legs))
        result = SimultaneousResult("simultaneous_paths", source, target, snapshot.snapshot_id, tuple(routes))
        dependency = _Dependency(
            tuple(sorted(edge_signatures.items())),
            tuple(sorted((vertex, signatures) for vertex, signatures in adjacency_signatures.items())),
        )
        return result, dependency

    def _compute_journey(self, source: str, target: str, at_time: int, snapshot: Snapshot) -> Tuple[JourneyResult, _Dependency]:
        self._require_vertex(source, snapshot)
        self._require_vertex(target, snapshot)
        if at_time < 0:
            raise TemporalGraphError("test oracle uses non-negative times; queries before zero are not supported")
        if interval_contains(snapshot.blocked.get(source, ()), at_time):
            result = JourneyResult("earliest_waiting_journey", source, target, snapshot.snapshot_id, at_time, None, ())
            return result, _Dependency((), ())
        if source == target:
            result = JourneyResult("earliest_waiting_journey", source, target, snapshot.snapshot_id, at_time, at_time, ())
            return result, _Dependency((), ())

        labels: Dict[str, List[int]] = {source: [at_time]}
        parents: Dict[Tuple[str, int], Tuple[int, Optional[EdgeVersion], int]] = {(source, at_time): (at_time, None, at_time)}
        queue: List[Tuple[int, str]] = [(at_time, source)]
        visited_vertices = {source}
        used_edges: Dict[str, int] = {}

        while queue:
            current_time, vertex = heappop(queue)
            if current_time not in labels.get(vertex, ()):
                continue
            if vertex == target:
                continue
            outgoing = snapshot.adjacency.get(vertex, ())
            visited_vertices.add(vertex)
            for edge in outgoing:
                used_edges[edge.edge_id] = edge.revision
                for depart in _departure_candidates(edge, current_time, snapshot):
                    arrive = depart + edge.duration
                    if depart >= current_time and not _wait_overlaps(
                        current_time, depart, snapshot.blocked.get(vertex, ())
                    ) and self._add_label(edge.target, arrive, labels, snapshot.blocked.get(edge.target, ())):
                        parents[(edge.target, arrive)] = (current_time, edge, depart)
                        heappush(queue, (arrive, edge.target))

        target_labels = labels.get(target, ())
        if not target_labels:
            result = JourneyResult("earliest_waiting_journey", source, target, snapshot.snapshot_id, at_time, None, ())
            return result, self._journey_dependency(visited_vertices, used_edges, snapshot)

        events: List[JourneyEvent] = []
        vertex = target
        current_arrival = min(target_labels)
        while (vertex, current_arrival) != (source, at_time):
            parent_time, edge, depart = parents[(vertex, current_arrival)]
            if edge is None:
                raise TemporalGraphError("internal journey reconstruction error")
            if depart < parent_time:
                raise TemporalGraphError("internal journey reconstruction error")
            ride = RideStep(edge.edge_id, edge.revision, edge.source, edge.target, depart, current_arrival, edge.duration, edge.start, edge.end)
            events.append(ride)
            if depart > parent_time:
                events.append(WaitStep(edge.source, parent_time, depart))
            vertex = edge.source
            current_arrival = parent_time
        events.reverse()
        result = JourneyResult(
            "earliest_waiting_journey",
            source,
            target,
            snapshot.snapshot_id,
            at_time,
            min(target_labels),
            tuple(events),
        )
        validate_journey(result, snapshot)
        return result, self._journey_dependency(set(labels) | visited_vertices, used_edges, snapshot)

    @staticmethod
    def _add_label(vertex: str, time: int, labels: Dict[str, List[int]], blocked: Sequence[Tuple[int, int]]) -> bool:
        current = labels.setdefault(vertex, [])
        for existing in list(current):
            if existing == time:
                return False
            if existing < time and not _wait_overlaps(existing, time, blocked):
                return False
        current.append(time)
        current.sort()
        return True

    @staticmethod
    def _journey_dependency(vertices: Iterable[str], used_edges: Dict[str, int], snapshot: Snapshot) -> _Dependency:
        adjacency: Dict[str, Tuple[Tuple[str, int], ...]] = {}
        edge_signatures = dict(used_edges)
        for vertex in vertices:
            outgoing = snapshot.adjacency.get(vertex, ())
            adjacency[vertex] = tuple((edge.edge_id, edge.revision) for edge in outgoing)
            for edge in outgoing:
                edge_signatures[edge.edge_id] = edge.revision
        return _Dependency(tuple(sorted(edge_signatures.items())), tuple(sorted(adjacency.items())))

    def _on_graph_change(self, change: GraphChange) -> None:
        changed_vertices = set(change.affected_vertices)
        self._invalidate(
            self._simultaneous_cache,
            set(change.added_edges) | set(change.changed_edges),
            changed_vertices,
        )
        self._invalidate(
            self._journey_cache,
            set(change.added_edges) | set(change.changed_edges),
            changed_vertices,
        )

    @staticmethod
    def _invalidate(cache: Dict[tuple, _CacheEntry], edge_ids: set, vertices: set) -> None:
        for key in list(cache):
            entry = cache[key]
            changed_edge = any(edge_id in edge_ids for edge_id, revision in entry.dependency.edge_revisions)
            changed_adjacency = any(vertex in vertices for vertex, signatures in entry.dependency.adjacency)
            if changed_edge or changed_adjacency:
                del cache[key]

    @staticmethod
    def _entry_valid(entry: _CacheEntry, snapshot: Snapshot) -> bool:
        for edge_id, revision in entry.dependency.edge_revisions:
            record = snapshot.edges.get(edge_id)
            if record is None or record.revision != revision:
                return False
        for vertex, signatures in entry.dependency.adjacency:
            outgoing = snapshot.adjacency.get(vertex, ())
            current = tuple((edge.edge_id, edge.revision) for edge in outgoing)
            if current != signatures:
                return False
        return True

    @staticmethod
    def _require_vertex(vertex: str, snapshot: Snapshot) -> None:
        if vertex not in snapshot.adjacency:
            raise TemporalGraphError(f"vertex {vertex!r} is not present in snapshot {snapshot.snapshot_id}")

    @staticmethod
    def _serializable_payload(result: object) -> object:
        return result

    @staticmethod
    def _bind_simultaneous(payload: object, source: str, target: str, snapshot: Snapshot) -> SimultaneousResult:
        if not isinstance(payload, SimultaneousResult):
            raise TemporalGraphError("cached simultaneous payload has wrong type")
        result = SimultaneousResult(payload.kind, source, target, snapshot.snapshot_id, payload.routes)
        return result

    @staticmethod
    def _bind_journey(payload: object, source: str, target: str, at_time: int, snapshot: Snapshot) -> JourneyResult:
        if not isinstance(payload, JourneyResult):
            raise TemporalGraphError("cached journey payload has wrong type")
        result = JourneyResult(payload.kind, source, target, snapshot.snapshot_id, at_time, payload.arrival, payload.events)
        validate_journey(result, snapshot)
        return result


def _interval_contains(interval: Tuple[Optional[int], Optional[int]], time: int) -> bool:
    left, right = interval
    return (left is None or time >= left) and (right is None or time < right)



def _wait_overlaps(start: int, end: int, blocked: Sequence[Tuple[int, int]]) -> bool:
    if end < start:
        return True
    for left, right in blocked:
        if start < right and end > left:
            return True
    return False
