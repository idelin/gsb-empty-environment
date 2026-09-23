"""数据模型：图（字符串 ID、孤立点、多重边）与离散班次表。

时间约定：整数分钟，自第 0 日 00:00 起算；跨日班次用 >=1440 的时刻表示。
所有时间区间均为左闭右开 [start, end)。
"""
from __future__ import annotations

from dataclasses import dataclass

MINUTES_PER_DAY = 24 * 60


def fmt_time(t: int) -> str:
    day, rem = divmod(t, MINUTES_PER_DAY)
    s = f"{rem // 60:02d}:{rem % 60:02d}"
    return s if day == 0 else f"+{day}d {s}"


@dataclass(frozen=True)
class Edge:
    edge_id: str
    src: str
    dst: str
    walk_time: int


class Graph:
    """顶点与边均使用字符串 ID；保留孤立点，允许同一对顶点间的多重边。"""

    def __init__(self) -> None:
        self.vertices: dict[str, None] = {}
        self.edges: dict[str, Edge] = {}
        self._out: dict[str, list[str]] = {}

    def add_vertex(self, vertex_id: str) -> None:
        self.vertices.setdefault(vertex_id)

    def add_edge(self, edge_id: str, src: str, dst: str, walk_time: int) -> None:
        if edge_id in self.edges:
            raise ValueError(f"duplicate edge id: {edge_id}")
        if walk_time < 0:
            raise ValueError("walk_time must be >= 0")
        self.add_vertex(src)
        self.add_vertex(dst)
        self.edges[edge_id] = Edge(edge_id, src, dst, walk_time)
        self._out.setdefault(src, []).append(edge_id)

    def out_edges(self, vertex_id: str) -> list[Edge]:
        return [self.edges[e] for e in self._out.get(vertex_id, [])]

    def isolated_vertices(self) -> list[str]:
        used = {e.src for e in self.edges.values()} | {e.dst for e in self.edges.values()}
        return [v for v in self.vertices if v not in used]


@dataclass(frozen=True)
class Connection:
    """班次相邻两站间的一段可乘坐连接。"""

    trip_id: str
    dep_stop: str
    arr_stop: str
    dep: int
    arr: int
    seq: int


@dataclass
class Trip:
    trip_id: str
    stops: list[str]
    departures: list[int]
    arrivals: list[int]
    capacity: int
    cancelled: bool = False
    version: int = 1

    def connections(self) -> list[Connection]:
        return [
            Connection(self.trip_id, self.stops[i], self.stops[i + 1],
                       self.departures[i], self.arrivals[i + 1], i)
            for i in range(len(self.stops) - 1)
        ]


class Timetable:
    """离散班次表：每站换乘时间、班次容量、跨日运行、取消与更正。"""

    def __init__(self, graph: Graph) -> None:
        self.graph = graph
        self.transfer_times: dict[str, int] = {}
        self.trips: dict[str, Trip] = {}
        self._listeners: list = []

    def set_transfer_time(self, stop: str, minutes: int) -> None:
        if minutes < 0:
            raise ValueError("transfer time must be >= 0")
        self.transfer_times[stop] = minutes

    def transfer_time(self, stop: str) -> int:
        return self.transfer_times.get(stop, 0)

    @staticmethod
    def _validate(stops, departures, arrivals, capacity) -> None:
        if not (len(stops) == len(departures) == len(arrivals)) or len(stops) < 2:
            raise ValueError("stops/departures/arrivals length mismatch")
        if capacity < 0:
            raise ValueError("capacity must be >= 0")
        for i in range(len(stops)):
            if arrivals[i] > departures[i]:
                raise ValueError("arrival after departure at the same stop")
            if i + 1 < len(stops) and departures[i] > arrivals[i + 1]:
                raise ValueError("trip times must be non-decreasing")

    def add_trip(self, trip_id: str, stops, departures, arrivals, capacity: int) -> Trip:
        if trip_id in self.trips:
            raise ValueError(f"duplicate trip id: {trip_id}")
        self._validate(stops, departures, arrivals, capacity)
        for s in stops:
            self.graph.add_vertex(s)
        trip = Trip(trip_id, list(stops), list(departures), list(arrivals), capacity)
        self.trips[trip_id] = trip
        return trip

    def cancel_trip(self, trip_id: str) -> None:
        trip = self.trips[trip_id]
        trip.cancelled = True
        trip.version += 1
        self._notify(trip_id)

    def update_trip(self, trip_id: str, *, departures=None, arrivals=None, capacity=None) -> None:
        """更正班次（时刻/容量），版本号递增并使相关证据失效。"""
        trip = self.trips[trip_id]
        new_dep = list(departures) if departures is not None else trip.departures
        new_arr = list(arrivals) if arrivals is not None else trip.arrivals
        new_cap = capacity if capacity is not None else trip.capacity
        self._validate(trip.stops, new_dep, new_arr, new_cap)
        trip.departures, trip.arrivals, trip.capacity = new_dep, new_arr, new_cap
        trip.version += 1
        self._notify(trip_id)

    def add_listener(self, fn) -> None:
        self._listeners.append(fn)

    def _notify(self, trip_id: str) -> None:
        for fn in self._listeners:
            fn(trip_id)

    def active_connections(self) -> list[Connection]:
        conns = []
        for trip in self.trips.values():
            if not trip.cancelled:
                conns.extend(trip.connections())
        conns.sort(key=lambda c: (c.dep, c.trip_id, c.seq))
        return conns
