"""帕累托旅程查询：最早到达 x 最少换乘。

算法：按乘坐次数分轮的连接扫描（每轮新增一次乘车，轮内扫描全部连接并做
步行边闭包），得到 (到达时刻, 换乘数) 的帕累托前沿；随后用带预算的 DFS
在时间展开图上重构出达到每个前沿标签的具体旅程（同标签并列旅程均保留）。

换乘规则：在站 s 于时刻 a 到达后，可乘坐发车时刻 >= a + transfer_time(s)
的其他班次；同班次续乘不限；起点首次上车不限换乘时间。
"""
from __future__ import annotations

from dataclasses import dataclass

from .model import Timetable

INF = float("inf")
_WALK = object()  # 步行后的“上一班次”标记


@dataclass(frozen=True)
class Segment:
    kind: str            # "trip" | "walk"
    src: str
    dst: str
    dep: int
    arr: int
    trip_id: str | None = None
    edge_id: str | None = None


@dataclass
class Journey:
    segments: list[Segment]

    @property
    def departure(self) -> int:
        return self.segments[0].dep

    @property
    def arrival(self) -> int:
        return self.segments[-1].arr

    @property
    def boardings(self) -> int:
        return sum(1 for s in self.segments if s.kind == "trip")

    @property
    def transfers(self) -> int:
        return max(0, self.boardings - 1)

    @property
    def trip_ids(self) -> list[str]:
        return [s.trip_id for s in self.segments if s.kind == "trip"]

    @property
    def label(self) -> tuple[int, int]:
        return (self.arrival, self.transfers)


def pareto_filter(labels) -> list[tuple[int, int]]:
    """(到达, 换乘) 标签的帕累托前沿：按到达升序，仅保留换乘严格更优者。"""
    best: list[tuple[int, int]] = []
    for t, k in sorted(set(labels)):
        if not best or k < best[-1][1]:
            best.append((t, k))
    return best


class QueryEngine:
    def __init__(self, timetable: Timetable) -> None:
        self.tt = timetable

    # ---- 对外接口 ----
    def query(self, origin: str, dest: str, depart_after: int,
              max_transfers: int = 2, max_journeys_per_label: int = 8) -> list[Journey]:
        journeys: list[Journey] = []
        for arr, transfers in self.pareto_frontier(origin, dest, depart_after, max_transfers):
            journeys.extend(self._reconstruct(origin, dest, depart_after,
                                              arr, transfers, max_journeys_per_label))
        journeys.sort(key=lambda j: (j.arrival, j.transfers))
        return journeys

    def pareto_frontier(self, origin: str, dest: str, depart_after: int,
                        max_transfers: int = 2) -> list[tuple[int, int]]:
        arrivals = {origin: depart_after}
        self._closure(arrivals)
        labels: list[tuple[int, int]] = []
        if dest in arrivals:
            labels.append((arrivals[dest], 0))
        for boardings in range(1, max_transfers + 2):
            first = boardings == 1
            arrivals = self._round(arrivals, origin, depart_after, first)
            if dest in arrivals:
                labels.append((arrivals[dest], boardings - 1))
        return pareto_filter(labels)

    # ---- 连接扫描 ----
    def _closure(self, arr: dict) -> None:
        changed = True
        while changed:
            changed = False
            for v, t in list(arr.items()):
                for e in self.tt.graph.out_edges(v):
                    nt = t + e.walk_time
                    if nt < arr.get(e.dst, INF):
                        arr[e.dst] = nt
                        changed = True

    def _round(self, prev: dict, origin: str, depart_after: int, first_round: bool) -> dict:
        base = dict(prev)
        self._closure(base)
        cur = dict(base)
        on_trip: set[str] = set()
        # 单趟按发车时刻扫描：新的到达改进不早于当前扫描位置，
        # 不会使已扫描连接变为可乘，故无需重扫（重扫会导致回溯搭乘）。
        for c in self.tt.active_connections():
            if c.trip_id not in on_trip:
                a = base.get(c.dep_stop)
                if a is None:
                    continue
                if first_round and c.dep_stop == origin and a == depart_after:
                    ok = c.dep >= a
                else:
                    ok = c.dep >= a + self.tt.transfer_time(c.dep_stop)
                if not ok:
                    continue
                on_trip.add(c.trip_id)
            if c.arr < cur.get(c.arr_stop, INF):
                cur[c.arr_stop] = c.arr
                stack = [c.arr_stop]
                while stack:  # 内联步行松弛
                    v = stack.pop()
                    for e in self.tt.graph.out_edges(v):
                        nt = cur[v] + e.walk_time
                        if nt < cur.get(e.dst, INF):
                            cur[e.dst] = nt
                            stack.append(e.dst)
        return cur

    # ---- 旅程重构（时间展开 DFS，含并列旅程） ----
    def _reconstruct(self, origin, dest, depart_after, target_arr, target_transfers, cap):
        conns_by_stop: dict[str, list] = {}
        for c in self.tt.active_connections():
            conns_by_stop.setdefault(c.dep_stop, []).append(c)
        for lst in conns_by_stop.values():
            lst.sort(key=lambda c: (c.dep, c.trip_id, c.seq))
        results: list[Journey] = []
        seen: set = set()
        on_path: set = set()
        budget = [200_000]

        def dfs(stop, time, last_trip, boardings, path):
            if len(results) >= cap or budget[0] <= 0:
                return
            budget[0] -= 1
            transfers = max(0, boardings - 1)
            if stop == dest and path and time == target_arr and transfers == target_transfers:
                journey = self._to_journey(path)
                key = tuple((s.kind, s.trip_id, s.edge_id, s.src, s.dst, s.dep, s.arr)
                            for s in journey.segments)
                if key not in seen:
                    seen.add(key)
                    results.append(journey)
                return
            if time > target_arr or transfers > target_transfers:
                return
            state = (stop, time, last_trip, boardings)
            if state in on_path:
                return
            on_path.add(state)
            for c in conns_by_stop.get(stop, []):
                if c.dep > target_arr:
                    break
                if last_trip is None or last_trip == c.trip_id:
                    ok = c.dep >= time
                else:
                    ok = c.dep >= time + self.tt.transfer_time(stop)
                if not ok:
                    continue
                nb = boardings if last_trip == c.trip_id else boardings + 1
                dfs(c.arr_stop, c.arr, c.trip_id, nb, path + [("trip", c)])
            for e in self.tt.graph.out_edges(stop):
                dfs(e.dst, time + e.walk_time, _WALK, boardings, path + [("walk", e, time)])
            on_path.discard(state)

        dfs(origin, depart_after, None, 0, [])
        return results

    @staticmethod
    def _to_journey(path) -> Journey:
        segments: list[Segment] = []
        for item in path:
            if item[0] == "trip":
                c = item[1]
                if segments and segments[-1].kind == "trip" and segments[-1].trip_id == c.trip_id:
                    prev = segments[-1]
                    segments[-1] = Segment("trip", prev.src, c.arr_stop, prev.dep, c.arr,
                                           trip_id=c.trip_id)
                else:
                    segments.append(Segment("trip", c.dep_stop, c.arr_stop, c.dep, c.arr,
                                            trip_id=c.trip_id))
            else:
                _, e, t = item
                segments.append(Segment("walk", e.src, e.dst, t, t + e.walk_time,
                                        edge_id=e.edge_id))
        return Journey(segments)
