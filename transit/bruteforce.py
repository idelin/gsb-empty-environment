"""小规模时间展开穷举：与 QueryEngine 交叉验证帕累托前沿。

在时间展开状态 (站, 上一班次) 上做多标签 Dijkstra，标签为 (时刻, 乘车次数)，
逐状态做帕累托剪枝；终点标签换算为 (到达时刻, 换乘数) 后取前沿。
"""
from __future__ import annotations

import heapq
import itertools

from .model import Timetable
from .query import pareto_filter

_WALK = object()


def brute_force_frontier(timetable: Timetable, origin: str, dest: str,
                         depart_after: int, max_transfers: int = 2) -> list[tuple[int, int]]:
    max_boardings = max_transfers + 1
    conns_by_stop: dict[str, list] = {}
    for c in timetable.active_connections():
        conns_by_stop.setdefault(c.dep_stop, []).append(c)

    labels: dict = {}
    counter = itertools.count()
    pq = [(depart_after, 0, next(counter), origin, None)]
    dest_labels: list[tuple[int, int]] = []

    while pq:
        t, b, _, stop, last = heapq.heappop(pq)
        key = (stop, last)
        if any(t2 <= t and b2 <= b for t2, b2 in labels.get(key, [])):
            continue
        labels.setdefault(key, []).append((t, b))
        if stop == dest:
            dest_labels.append((t, max(0, b - 1)))
            continue  # 更晚回到终点只会被支配
        for c in conns_by_stop.get(stop, []):
            if last is None or last == c.trip_id:
                ok = c.dep >= t
            else:
                ok = c.dep >= t + timetable.transfer_time(stop)
            if not ok:
                continue
            nb = b if last == c.trip_id else b + 1
            if nb > max_boardings:
                continue
            heapq.heappush(pq, (c.arr, nb, next(counter), c.arr_stop, c.trip_id))
        for e in timetable.graph.out_edges(stop):
            heapq.heappush(pq, (t + e.walk_time, b, next(counter), e.dst, _WALK))

    return pareto_filter(dest_labels)
