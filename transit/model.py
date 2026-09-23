"""Core data model: network multigraph, trips, timetable, journeys.

Time is an integer minute counter (may exceed 1440 for cross-day trips).
All time intervals are left-closed / right-open: [start, end).
"""
from __future__ import annotations

from dataclasses import dataclass


@dataclass(frozen=True)
class Vertex:
    id: str
    transfer_time: int = 0


@dataclass(frozen=True)
class Edge:
    id: str
    src: str
    dst: str
    walk_time: int


class Network:
    """Directed multigraph with string IDs.

    Isolated vertices are preserved and parallel edges (same endpoints,
    distinct IDs) are allowed.
    """

    def __init__(self) -> None:
        self.vertices: dict[str, Vertex] = {}
        self.edges: dict[str, Edge] = {}

    def add_vertex(self, vertex_id: str, transfer_time: int = 0) -> Vertex:
        if transfer_time < 0:
            raise ValueError("transfer_time must be >= 0")
        vertex = Vertex(vertex_id, transfer_time)
        self.vertices[vertex_id] = vertex
        return vertex

    def add_edge(self, edge_id: str, src: str, dst: str, walk_time: int) -> Edge:
        if src not in self.vertices or dst not in self.vertices:
            raise KeyError(f"unknown edge endpoint: {src!r} -> {dst!r}")
        if walk_time < 0:
            raise ValueError("walk_time must be >= 0")
        edge = Edge(edge_id, src, dst, walk_time)
        self.edges[edge_id] = edge
        return edge

    def transfer_time(self, station: str) -> int:
        return self.vertices[station].transfer_time

    def shortest_walks(self) -> dict[str, dict[str, int | None]]:
        """All-pairs shortest walking times (Floyd-Warshall).

        Parallel edges collapse to their minimum; unreachable pairs are None.
        """
        ids = list(self.vertices)
        dist: dict[str, dict[str, int | None]] = {
            a: {b: (0 if a == b else None) for b in ids} for a in ids
        }
        for edge in self.edges.values():
            cur = dist[edge.src][edge.dst]
            if cur is None or edge.walk_time < cur:
                dist[edge.src][edge.dst] = edge.walk_time
        for k in ids:
            row_k = dist[k]
            for a in ids:
                row_a = dist[a]
                if row_a[k] is None:
                    continue
                via = row_a[k]
                for b in ids:
                    if row_k[b] is None:
                        continue
                    cand = via + row_k[b]
                    if row_a[b] is None or cand < row_a[b]:
                        row_a[b] = cand
        return dist


@dataclass(frozen=True)
class Stop:
    station: str
    arrival: int
    departure: int


@dataclass
class Trip:
    id: str
    stops: tuple[Stop, ...]
    capacity: int
    cancelled: bool = False

    def __post_init__(self) -> None:
        if len(self.stops) < 2:
            raise ValueError("a trip needs at least two stops")
        if self.capacity < 0:
            raise ValueError("capacity must be >= 0")
        for stop in self.stops:
            if stop.arrival > stop.departure:
                raise ValueError("stop interval must satisfy arrival <= departure")
        for first, second in zip(self.stops, self.stops[1:]):
            if first.departure > second.arrival:
                raise ValueError("trip stop times must be non-decreasing")

    @property
    def num_segments(self) -> int:
        return len(self.stops) - 1


class Timetable:
    """Versioned trip store. Every mutation bumps ``version`` so cached
    evidence keyed on the version is invalidated automatically."""

    def __init__(self, network: Network) -> None:
        self.network = network
        self.trips: dict[str, Trip] = {}
        self.version = 0

    def add_trip(self, trip: Trip) -> Trip:
        for stop in trip.stops:
            if stop.station not in self.network.vertices:
                raise KeyError(f"unknown station {stop.station!r}")
        self.trips[trip.id] = trip
        self.version += 1
        return trip

    def cancel_trip(self, trip_id: str) -> None:
        trip = self.trips[trip_id]
        self.trips[trip_id] = Trip(trip.id, trip.stops, trip.capacity, cancelled=True)
        self.version += 1

    def correct_trip(self, trip_id: str, *, stops=None, capacity=None) -> Trip:
        old = self.trips[trip_id]
        new = Trip(
            id=old.id,
            stops=tuple(stops) if stops is not None else old.stops,
            capacity=old.capacity if capacity is None else capacity,
            cancelled=old.cancelled,
        )
        self.trips[trip_id] = new
        self.version += 1
        return new

    def active_trips(self) -> list[Trip]:
        return [t for t in self.trips.values() if not t.cancelled]


@dataclass(frozen=True)
class RideLeg:
    trip_id: str
    board_station: str
    board_time: int
    alight_station: str
    alight_time: int


@dataclass(frozen=True)
class WalkLeg:
    src: str
    dst: str
    start: int
    end: int


class Journey:
    def __init__(self, legs) -> None:
        if not legs:
            raise ValueError("a journey needs at least one leg")
        self.legs = tuple(legs)

    @property
    def ride_legs(self) -> list[RideLeg]:
        return [leg for leg in self.legs if isinstance(leg, RideLeg)]

    @property
    def arrival_time(self) -> int:
        last = self.legs[-1]
        return last.alight_time if isinstance(last, RideLeg) else last.end

    @property
    def num_transfers(self) -> int:
        return max(0, len(self.ride_legs) - 1)

    def trip_ids(self) -> list[str]:
        return [leg.trip_id for leg in self.ride_legs]

    def __repr__(self) -> str:
        parts = []
        for leg in self.legs:
            if isinstance(leg, RideLeg):
                parts.append(
                    f"{leg.board_station}-[{leg.trip_id} {leg.board_time}->{leg.alight_time}]->{leg.alight_station}"
                )
            else:
                parts.append(f"{leg.src}-[walk {leg.start}->{leg.end}]->{leg.dst}")
        return f"Journey(arr={self.arrival_time}, transfers={self.num_transfers}: " + " ".join(parts) + ")"
