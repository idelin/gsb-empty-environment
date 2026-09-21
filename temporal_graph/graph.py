from __future__ import annotations

from copy import deepcopy
from dataclasses import dataclass
from typing import Callable, Iterable

from .model import EdgeRecord, HalfOpenInterval, VertexRecord


@dataclass(frozen=True)
class ChangeEvent:
    kind: str
    record_id: str
    old_version: int
    revision: int


class Snapshot:
    def __init__(
        self,
        revision: int,
        vertices: dict[str, VertexRecord],
        edges: dict[str, EdgeRecord],
        edge_versions: dict[str, dict[int, EdgeRecord]],
    ) -> None:
        self.revision = revision
        self._vertices = dict(vertices)
        self._edges = dict(edges)
        self._edge_versions = deepcopy(edge_versions)
        self._active_edge_ids = frozenset(self._edges)

    @property
    def id(self) -> int:
        return self.revision

    def vertex(self, vertex_id: str) -> VertexRecord:
        try:
            return self._vertices[vertex_id]
        except KeyError as exc:
            raise KeyError(f"unknown vertex: {vertex_id}") from exc

    def edge(self, edge_id: str) -> EdgeRecord:
        try:
            return self._edges[edge_id]
        except KeyError as exc:
            raise KeyError(f"unknown or withdrawn edge: {edge_id}") from exc

    def edge_at_version(self, edge_id: str, version: int) -> EdgeRecord:
        try:
            return self._edge_versions[edge_id][version]
        except KeyError as exc:
            raise KeyError(f"unknown edge version: {edge_id}@{version}") from exc

    def active_edge_ids(self) -> frozenset[str]:
        return self._active_edge_ids

    def vertex_ids(self) -> tuple[str, ...]:
        return tuple(sorted(self._vertices))

    def edges(self) -> tuple[EdgeRecord, ...]:
        return tuple(self._edges[edge_id] for edge_id in sorted(self._edges))

    def outgoing(self, vertex_id: str) -> tuple[EdgeRecord, ...]:
        self.vertex(vertex_id)
        return tuple(
            edge
            for edge in sorted(self._edges.values(), key=lambda record: record.edge_id)
            if edge.source == vertex_id
        )


class TemporalGraph:
    def __init__(self) -> None:
        self.revision = 0
        self._vertices: dict[str, VertexRecord] = {}
        self._edges: dict[str, EdgeRecord] = {}
        self._edge_versions: dict[str, dict[int, EdgeRecord]] = {}
        self._listeners: list[Callable[[ChangeEvent], None]] = []

    def add_listener(self, listener: Callable[[ChangeEvent], None]) -> None:
        self._listeners.append(listener)

    def declare_vertex(
        self,
        vertex_id: str,
        blocked: Iterable[HalfOpenInterval] = (),
    ) -> VertexRecord:
        _require_id(vertex_id, "vertex")
        blocked_tuple = tuple(blocked)
        existing = self._vertices.get(vertex_id)
        if existing is not None:
            if existing.blocked != blocked_tuple:
                raise ValueError("vertex blocked intervals are immutable after declaration")
            return existing

        record = VertexRecord(vertex_id, blocked_tuple, 1)
        self._vertices[vertex_id] = record
        self.revision += 1
        return record

    def add_edge(
        self,
        edge_id: str,
        source: str,
        target: str,
        window: HalfOpenInterval,
        duration: int,
    ) -> EdgeRecord:
        _require_id(edge_id, "edge")
        if edge_id in self._edge_versions:
            raise ValueError(f"edge already exists: {edge_id}")
        if source not in self._vertices:
            self._vertices[source] = VertexRecord(source)
        if target not in self._vertices:
            self._vertices[target] = VertexRecord(target)
        record = EdgeRecord(edge_id, source, target, window, duration, 1, True)
        self._edges[edge_id] = record
        self._edge_versions[edge_id] = {1: record}
        self.revision += 1
        return record

    def correct_edge(
        self,
        edge_id: str,
        window: HalfOpenInterval | None = None,
        duration: int | None = None,
    ) -> EdgeRecord:
        old = self._require_active_edge(edge_id)
        if window is None and duration is None:
            raise ValueError("nothing to correct")
        record = EdgeRecord(
            edge_id=edge_id,
            source=old.source,
            target=old.target,
            window=old.window if window is None else window,
            duration=old.duration if duration is None else duration,
            version=old.version + 1,
            active=True,
        )
        self._edges[edge_id] = record
        self._edge_versions[edge_id][record.version] = record
        self.revision += 1
        self._publish(ChangeEvent("edge", edge_id, old.version, self.revision))
        return record

    def withdraw_edge(self, edge_id: str) -> EdgeRecord:
        old = self._require_active_edge(edge_id)
        record = EdgeRecord(
            edge_id=old.edge_id,
            source=old.source,
            target=old.target,
            window=old.window,
            duration=old.duration,
            version=old.version + 1,
            active=False,
        )
        del self._edges[edge_id]
        self._edge_versions[edge_id][record.version] = record
        self.revision += 1
        self._publish(ChangeEvent("edge", edge_id, old.version, self.revision))
        return record

    def snapshot(self) -> Snapshot:
        return Snapshot(
            self.revision,
            self._vertices,
            self._edges,
            self._edge_versions,
        )

    def _require_active_edge(self, edge_id: str) -> EdgeRecord:
        try:
            record = self._edges[edge_id]
        except KeyError as exc:
            raise KeyError(f"unknown or withdrawn edge: {edge_id}") from exc
        return record

    def _publish(self, event: ChangeEvent) -> None:
        for listener in self._listeners:
            listener(event)


def _require_id(value: str, kind: str) -> None:
    if not isinstance(value, str) or not value:
        raise ValueError(f"{kind} id must be a non-empty string")
